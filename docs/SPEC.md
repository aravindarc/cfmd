# `cfmd` — Confluence ⇄ Markdown Sync CLI

**Build spec for a Go binary that syncs Confluence pages with local markdown files.**

This document is a technical specification intended as input for an AI coding agent (Claude Code). It describes what to build, how to build it, and in what order. It is deliberately opinionated. Where a decision is noted as "flex," the agent may choose; everywhere else, follow the spec.

---

## 1. Goal and scope

Build a single Go binary, `cfmd`, that:

1. **Pushes** a local `.md` file to a Confluence page (creating or updating it).
2. **Pulls** a Confluence page to a local `.md` file.
3. **Detects conflicts** when the remote page has changed since the last sync.
4. **Integrates with IntelliJ** via External Tools / Run Configurations so a developer can push or pull the currently open file with a keyboard shortcut.

Non-goals (for v1):
- No git integration. No CI. The tool runs locally, invoked manually.
- No GUI. CLI only.
- No support for Confluence Server/Data Center specifics beyond "Confluence Cloud REST API v1." Target Atlassian Cloud.
- No full-fidelity round-trip of every Confluence macro. Support a defined subset; preserve unknown macros as opaque passthrough.

---

## 2. High-level architecture

```
┌──────────────┐       ┌──────────────┐       ┌─────────────────┐
│  IntelliJ    │──────▶│  cfmd binary │──────▶│  Confluence     │
│  (External   │  exec │  (Go)        │  HTTP │  Cloud REST API │
│   Tools)     │       │              │       │                 │
└──────────────┘       └──────┬───────┘       └─────────────────┘
                              │
                              ▼
                       ┌──────────────┐
                       │  Local FS    │
                       │  .md files   │
                       │  + cache dir │
                       └──────────────┘
```

### Key insight: Confluence's native format

Confluence does **not** store pages as markdown. Every page is stored in **Confluence Storage Format** — XHTML with Atlassian-specific elements:

```xml
<h1>Payment Service</h1>
<p>Handles <strong>card</strong> payments.</p>
<ac:structured-macro ac:name="info">
  <ac:rich-text-body><p>Launching Q2</p></ac:rich-text-body>
</ac:structured-macro>
<ac:structured-macro ac:name="code">
  <ac:parameter ac:name="language">python</ac:parameter>
  <ac:plain-text-body><![CDATA[def charge(): ...]]></ac:plain-text-body>
</ac:structured-macro>
```

The REST API accepts and returns this format in a `body.storage.value` field. **Every push does Markdown → Storage Format conversion. Every pull does Storage Format → Markdown conversion.** These two converters are the heart of the tool.

---

## 3. Package layout

```
cfmd/
├── cmd/cfmd/main.go            # Entry point, wires cobra commands
├── internal/
│   ├── cli/                    # Cobra command definitions
│   │   ├── root.go
│   │   ├── push.go
│   │   ├── pull.go
│   │   ├── status.go
│   │   └── init.go
│   ├── config/                 # Config loading (file + env)
│   │   └── config.go
│   ├── confluence/             # REST client
│   │   ├── client.go
│   │   ├── types.go
│   │   └── errors.go
│   ├── frontmatter/            # HTML-comment metadata parsing
│   │   └── frontmatter.go
│   ├── convert/
│   │   ├── md2storage/         # Markdown → storage format
│   │   │   ├── renderer.go
│   │   │   └── macros.go
│   │   └── storage2md/         # Storage format → markdown
│   │       ├── parser.go
│   │       └── macros.go
│   └── cache/                  # Last-synced snapshots for conflict detection
│       └── cache.go
├── testdata/                   # Golden files for converter tests
│   ├── md2storage/
│   └── storage2md/
├── go.mod
├── go.sum
└── README.md
```

### Dependencies

- `github.com/spf13/cobra` — CLI framework
- `github.com/spf13/viper` — config loading (YAML + env + flag merge)
- `github.com/yuin/goldmark` — markdown parser/renderer (extensible AST, good custom renderer support)
- `golang.org/x/net/html` — tolerant HTML parsing for storage format
- `github.com/zalando/go-keyring` — OS keyring for storing API tokens (macOS Keychain, Windows Credential Manager, Linux Secret Service)
- Standard library for HTTP, JSON, filesystem

---

## 4. File format

A `cfmd`-managed markdown file has HTML-comment frontmatter at the top. HTML comments are chosen (over YAML `---`) because they render as invisible in markdown previews and survive paste-into-Confluence if someone does that manually.

```markdown
<!-- cfmd:page_id: 123456789 -->
<!-- cfmd:space: ENG -->
<!-- cfmd:title: Payment Service Redesign -->
<!-- cfmd:parent_id: 987654321 -->
<!-- cfmd:version: 12 -->
<!-- cfmd:last_synced: 2026-04-21T10:30:00Z -->

# Payment Service Redesign

The payment service handles card and bank transfer payments...
```

### Field semantics

| Field | Required for push | Required for pull output | Notes |
|-------|-------------------|--------------------------|-------|
| `page_id` | No (auto-created if missing) | Yes | Integer, Confluence's page ID |
| `space` | Yes | Yes | Space key, e.g. `ENG` |
| `title` | Yes | Yes | Page title |
| `parent_id` | No | Yes | If absent on create, page goes under space root |
| `version` | Yes (if page_id set) | Yes | Remote version at last sync; used for conflict detection |
| `last_synced` | No | Yes | ISO 8601 timestamp |

### On first push of a new file

If `page_id` is missing:
1. `cfmd` creates the page via `POST /rest/api/content`
2. `cfmd` rewrites the file in place, injecting `page_id`, `version`, and `last_synced` into the frontmatter

This in-place rewrite is important: the user's next push will use the ID, not re-create the page.

---

## 5. Configuration

### File location (XDG-compliant)

- Linux/macOS: `~/.config/cfmd/config.yaml`
- Windows: `%APPDATA%\cfmd\config.yaml`

### Format (YAML)

```yaml
base_url: https://yourco.atlassian.net/wiki
username: you@company.com
# token is NOT stored here by default; see "Secrets" below
default_space: ENG
default_parent_id: 987654321
timeout_seconds: 30
cache_dir: ~/.cache/cfmd      # optional; default per XDG
```

### Secrets

**Do not store the API token in `config.yaml` by default.** Order of lookup for the token:

1. Env var `CFMD_TOKEN`
2. OS keyring (via `go-keyring`, service name `cfmd`, key = `username`)
3. Config file field `token` (only if user explicitly opts in with `allow_plaintext_token: true`)
4. Prompt interactively

`cfmd login` writes the token to the OS keyring.

### Env var overrides

Every config key can be overridden by an env var with `CFMD_` prefix and uppercase: `CFMD_BASE_URL`, `CFMD_DEFAULT_SPACE`, etc. Viper handles this.

---

## 6. CLI specification

Use `cobra`. Root command shows help. Global flags:

- `--config <path>` — override config file location
- `--verbose, -v` — debug logging to stderr
- `--dry-run` — for `push`, show what would happen without calling the API

### `cfmd init`

Interactive setup. Prompts for base URL, username, token, default space. Writes `config.yaml` and stores the token in the keyring. Verifies credentials with `GET /rest/api/user/current`.

### `cfmd push <file.md>`

1. Read file.
2. Parse frontmatter. Validate required fields given presence/absence of `page_id`.
3. Convert markdown body → storage format XHTML.
4. If `page_id` is empty:
   - `POST /rest/api/content` to create. Rewrite file with returned `id` and `version`.
5. If `page_id` is set:
   - `GET /rest/api/content/{id}?expand=version` to check remote version.
   - If `remote.version > frontmatter.version`: **conflict.** Bail unless `--force`. Print diff summary.
   - Else: `PUT /rest/api/content/{id}` with body and `version.number = remote.version + 1`.
   - On success, update frontmatter `version` and `last_synced`.
6. Save a snapshot of the pushed storage format to `<cache_dir>/<page_id>.xhtml` for future conflict detection.

Flags:
- `--force` — overwrite remote even on version mismatch
- `--message <msg>` — Confluence version comment

Exit codes:
- `0` success
- `1` generic failure
- `2` conflict detected (exit without pushing)
- `3` auth failure
- `4` network/API error

### `cfmd pull <page-id-or-url> [--out <file.md>]`

Accepts either:
- A page ID: `cfmd pull 123456789`
- A full URL: `cfmd pull https://yourco.atlassian.net/wiki/spaces/ENG/pages/123456789/Payment+Service`

(Parse the URL with a regex to extract the ID.)

1. `GET /rest/api/content/{id}?expand=body.storage,version,space,ancestors`
2. Convert `body.storage.value` → markdown.
3. Build frontmatter from response fields.
4. Determine output path:
   - If `--out` given: use it.
   - Else: slugify `title` (e.g. `Payment Service Redesign` → `payment-service-redesign.md`) and write to CWD.
5. If output file already exists and has matching `page_id` frontmatter: treat as re-pull; check if local was modified since `last_synced`. If so, warn and require `--force`.
6. Save snapshot to cache.

Flags:
- `--out <path>` — output file
- `--force` — overwrite local changes

### `cfmd status <file.md>`

Without modifying anything, report:
- Frontmatter page ID, space, title, local version.
- Current remote version.
- Whether local file has changed since last sync (hash body against cached snapshot after re-conversion).
- Whether remote has changed since last sync.
- One of four states: `in_sync`, `local_ahead`, `remote_ahead`, `diverged`.

Exit code: `0` in_sync, `1` local_ahead, `2` remote_ahead, `3` diverged.

---

## 7. Confluence REST client

### Auth

HTTP Basic: `Authorization: Basic base64(username:token)`.

### Endpoints used

| Action | Method | Path |
|--------|--------|------|
| Current user (verify auth) | GET | `/rest/api/user/current` |
| Get page | GET | `/rest/api/content/{id}?expand=body.storage,version,space,ancestors` |
| Create page | POST | `/rest/api/content` |
| Update page | PUT | `/rest/api/content/{id}` |
| Search by title | GET | `/rest/api/content?spaceKey={space}&title={title}&expand=version` |
| Upload attachment | POST (multipart) | `/rest/api/content/{id}/child/attachment` |

### Create page body shape

```json
{
  "type": "page",
  "title": "Payment Service Redesign",
  "space": { "key": "ENG" },
  "ancestors": [ { "id": "987654321" } ],
  "body": {
    "storage": {
      "value": "<h1>Payment Service Redesign</h1>...",
      "representation": "storage"
    }
  }
}
```

### Update page body shape

```json
{
  "id": "123456789",
  "type": "page",
  "title": "Payment Service Redesign",
  "space": { "key": "ENG" },
  "version": { "number": 13, "message": "Updated via cfmd" },
  "body": {
    "storage": {
      "value": "<h1>...</h1>",
      "representation": "storage"
    }
  }
}
```

### Error handling

- 401/403 → return typed `AuthError`
- 404 → `NotFoundError`
- 409 → `ConflictError` (Confluence returns this when version number doesn't match)
- 429 → retry with exponential backoff, respect `Retry-After` header. Max 3 retries.
- 5xx → retry up to 2 times.

### Client sketch

```go
// internal/confluence/client.go
type Client struct {
    baseURL    string
    username   string
    token      string
    httpClient *http.Client
}

func New(cfg *config.Config) *Client { /* ... */ }

func (c *Client) GetPage(ctx context.Context, id string) (*Page, error)
func (c *Client) CreatePage(ctx context.Context, p *PageCreate) (*Page, error)
func (c *Client) UpdatePage(ctx context.Context, id string, p *PageUpdate) (*Page, error)
func (c *Client) UploadAttachment(ctx context.Context, pageID, filename string, data io.Reader) (*Attachment, error)

// Page represents the relevant slice of the REST response
type Page struct {
    ID      string
    Title   string
    Space   Space
    Version Version
    Body    Body
    Ancestors []Ancestor
}
```

---

## 8. Conversion: Markdown → Storage Format

Use `goldmark` with a **custom renderer** that emits storage format instead of HTML. Goldmark's `renderer.NodeRenderer` interface is designed exactly for this.

### Mapping table

| Markdown | Storage format |
|----------|----------------|
| `# H1` ... `###### H6` | `<h1>` ... `<h6>` |
| `**bold**` | `<strong>` |
| `*italic*` | `<em>` |
| `~~strike~~` | `<span style="text-decoration: line-through">` |
| `` `code` `` | `<code>` |
| ```` ```lang\n...\n``` ```` | `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">lang</ac:parameter><ac:plain-text-body><![CDATA[...]]></ac:plain-text-body></ac:structured-macro>` |
| `> quote` | `<blockquote>` |
| `- item` / `1. item` | `<ul><li>` / `<ol><li>` |
| `[text](url)` | `<a href="url">text</a>` |
| `![alt](img.png)` | See "Images" below |
| `\| table \|` | `<table><tbody><tr><td>` (GFM tables only) |
| `---` (HR) | `<hr/>` |
| `> [!NOTE]` ... | Info macro (see "GFM alerts" below) |
| `> [!WARNING]` ... | Warning macro |
| `> [!TIP]` ... | Tip macro |
| `> [!IMPORTANT]` ... | Note macro |
| `> [!CAUTION]` ... | Warning macro |

### GFM alerts → Confluence macros

```markdown
> [!NOTE]
> Launching Q2
```

becomes

```xml
<ac:structured-macro ac:name="info">
  <ac:rich-text-body><p>Launching Q2</p></ac:rich-text-body>
</ac:structured-macro>
```

Implement as a goldmark AST transformation: walk blockquote nodes, detect the `[!TYPE]` marker in the first paragraph, swap for a synthetic `AdmonitionNode`, render that as the macro.

### Code blocks

```go
func (r *Renderer) renderFencedCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
    if !entering {
        return ast.WalkContinue, nil
    }
    block := node.(*ast.FencedCodeBlock)
    lang := string(block.Language(source))
    var body bytes.Buffer
    for i := 0; i < block.Lines().Len(); i++ {
        line := block.Lines().At(i)
        body.Write(line.Value(source))
    }
    w.WriteString(`<ac:structured-macro ac:name="code">`)
    if lang != "" {
        fmt.Fprintf(w, `<ac:parameter ac:name="language">%s</ac:parameter>`, lang)
    }
    w.WriteString(`<ac:plain-text-body><![CDATA[`)
    w.Write(body.Bytes())
    w.WriteString(`]]></ac:plain-text-body></ac:structured-macro>`)
    return ast.WalkSkipChildren, nil
}
```

CDATA escaping: if the body contains `]]>`, split it across two CDATA sections: `]]]]><![CDATA[>`. Handle this in a helper.

### Images

Local relative paths (`![diagram](./diagram.png)`) must be uploaded as attachments first:

1. Before rendering, walk the AST and collect all local image paths.
2. After rendering (or during, with a two-pass approach), upload each to `/rest/api/content/{page_id}/child/attachment`.
3. In the rendered output, replace with Confluence attachment reference:
   ```xml
   <ac:image><ri:attachment ri:filename="diagram.png"/></ac:image>
   ```

For v1, if `page_id` is not yet known (first push), defer attachment upload: create the page with placeholder image tags, then upload attachments, then do a second PUT to replace placeholders. Document this two-step behavior in the push command.

Remote URLs (`![x](https://...)`) render as `<ac:image><ri:url ri:value="..."/></ac:image>` — no upload needed.

### Passthrough raw HTML

Allow raw HTML in markdown to pass through to storage format as-is. Goldmark's `html.WithUnsafe()` is needed. This lets users embed Confluence macros directly:

```markdown
<!-- An info panel -->
<ac:structured-macro ac:name="info">
  <ac:rich-text-body><p>Raw macro preserved.</p></ac:rich-text-body>
</ac:structured-macro>
```

### Escaping

- XML-escape all text: `&` → `&amp;`, `<` → `&lt;`, `>` → `&gt;`, `"` → `&quot;` in attributes.
- Don't double-escape inside CDATA (but do the `]]>` split above).

---

## 9. Conversion: Storage Format → Markdown

No great off-the-shelf Go library handles Confluence macros. Build a custom parser using `golang.org/x/net/html`. The storage format is almost valid XML but not always; `net/html` is tolerant.

### Approach

1. Parse input with `html.Parse(strings.NewReader("<root>" + input + "</root>"))`.
2. Walk the tree. For each node, dispatch to a handler based on tag name.
3. Emit markdown to a `strings.Builder`.

### Mapping (reverse of §8)

Special-case the Atlassian-namespaced tags:

| Storage format tag | Markdown |
|--------------------|----------|
| `<ac:structured-macro ac:name="code">` | Fenced code block with language from `<ac:parameter>` |
| `<ac:structured-macro ac:name="info">` | `> [!NOTE]` |
| `<ac:structured-macro ac:name="warning">` | `> [!WARNING]` |
| `<ac:structured-macro ac:name="tip">` | `> [!TIP]` |
| `<ac:structured-macro ac:name="note">` | `> [!IMPORTANT]` |
| `<ac:structured-macro ac:name="...">` (unknown) | Opaque passthrough (see below) |
| `<ac:image><ri:attachment .../></ac:image>` | `![](attachment-url)` with URL derived from page + filename |
| `<ac:link><ri:page .../></ac:link>` | `[title](confluence-page-url)` |

### Opaque passthrough for unknown macros

Round-trip fidelity for macros we don't model is critical. For any `<ac:structured-macro>` we don't recognize, emit the raw XML as an HTML comment block in the markdown:

```markdown
<!-- cfmd:raw:begin -->
<ac:structured-macro ac:name="jira">
  <ac:parameter ac:name="key">PROJ-123</ac:parameter>
</ac:structured-macro>
<!-- cfmd:raw:end -->
```

On push, the markdown converter recognizes this sentinel pair and emits the raw XML verbatim into the output. This is what makes round-tripping work for features we don't explicitly support.

### Whitespace

Storage format has more whitespace latitude than markdown. Strip leading/trailing whitespace per block. Collapse runs of blank lines to a single blank line.

---

## 10. Conflict detection

### Cache directory layout

```
~/.cache/cfmd/
  pages/
    123456789/
      last_remote.xhtml    # storage format at last sync
      last_local.md        # local file body at last sync (body only, no frontmatter)
      meta.json            # {version, synced_at, etag}
```

### Push conflict detection

Before PUT:
1. `GET` remote page, check version.
2. If `remote.version != frontmatter.version`: **remote changed**.
   - Optionally, compare `last_remote.xhtml` with current remote storage. If identical, it's a spurious version bump (nothing actually changed — can proceed). If different, it's a real conflict.
3. On real conflict: exit with code 2 and print:
   ```
   CONFLICT: remote version 15, local last-synced version 12.
   Remote changes since last sync:
     - Title: "Payment Service Redesign" → "Payments v2 Design"
     - Content length: 4823 → 5891 chars
   
   Options:
     cfmd pull <file> --force     # overwrite local with remote
     cfmd push <file> --force     # overwrite remote with local
     cfmd status <file>           # show detail
   ```

### Pull conflict detection

If output file exists with matching `page_id`:
1. Hash current file body. Compare to `last_local.md`.
2. If different, local has unsaved changes. Require `--force` or `--out` to a different path.

---

## 11. IntelliJ integration

### Approach A: External Tools (recommended for file-aware actions)

`Settings → Tools → External Tools → +`

**Push current file:**
- Name: `cfmd: Push current file`
- Program: `/usr/local/bin/cfmd` (or wherever `which cfmd` lands)
- Arguments: `push $FilePath$`
- Working directory: `$FileDir$`
- Open console: ✓
- Synchronize files after execution: ✓  (so IntelliJ reloads the file after frontmatter is updated)

**Pull page (by ID, prompt for input):**
- Name: `cfmd: Pull page`
- Program: `/usr/local/bin/cfmd`
- Arguments: `pull $Prompt$ --out $FileDir$/$Prompt$.md`
- Working directory: `$ProjectFileDir$`

Wait — IntelliJ's `$Prompt$` is used once per occurrence. Second option: make the pull command smart enough to slugify its own filename so `cfmd pull 123` without `--out` just works, and use:
- Arguments: `pull $Prompt$`
- Working directory: `$ProjectFileDir$/docs`  (or wherever)

**Status:**
- Name: `cfmd: Status`
- Program: `/usr/local/bin/cfmd`
- Arguments: `status $FilePath$`

### Keymap bindings

`Settings → Keymap → External Tools → cfmd`:
- Push current file: `⌘⇧U` (macOS) / `Ctrl+Shift+U` (Linux/Windows)
- Pull page: `⌘⇧P` (note: conflicts with Find Action — pick something else or override intentionally)
- Status: `⌘⇧S` (also conflicts — pick another)

Suggested non-conflicting defaults: `Ctrl+Alt+Shift+U` for push, `Ctrl+Alt+Shift+P` for pull.

### Approach B: Shell Script run configurations

For users who prefer per-project, committed-to-repo run configs (they live in `.idea/runConfigurations/`):

- `Run → Edit Configurations → + → Shell Script`
- Script text: `cfmd push "$1"`
- Script options: (path to the file as first arg)
- Interpreter: `/bin/bash`

These show up in the green "Run" dropdown and can be shared via VCS. Less dynamic than External Tools (no `$FilePath$` macro) but nicer UX for fixed workflows.

### Approach C: File Watcher plugin

If the user wants automatic push on save:
- Install the `File Watchers` plugin (bundled in IDEA Ultimate, separate download for Community).
- New watcher, file type: Markdown, scope: project, program: `cfmd`, arguments: `push $FilePath$`.
- Downside: every save pushes, which is noisy. Don't recommend by default.

---

## 12. Implementation plan

Build in phases. Each phase is independently testable and usable.

### Phase 1 — MVP (push only, simple conversion)
- [ ] Project skeleton, `go.mod`, cobra root command
- [ ] Config loading (YAML + env, no keyring yet — env var only)
- [ ] Confluence client: `GetPage`, `UpdatePage`, `CreatePage`
- [ ] Frontmatter parser/writer (HTML comments)
- [ ] md2storage renderer covering: headings, bold, italic, links, lists, code blocks, inline code, blockquotes, HR, basic tables, paragraphs
- [ ] `cfmd push <file>` command with create-or-update logic
- [ ] Version check before update (abort on mismatch, no diff yet)
- [ ] Unit tests: converter golden files, frontmatter round-trip

**Acceptance:** can push a new markdown file, have the page created, edit the file, push again, see the update in Confluence. Works end-to-end from CLI.

### Phase 2 — Pull
- [ ] `storage2md` parser covering the same subset as Phase 1
- [ ] Opaque passthrough for unknown macros (`<!-- cfmd:raw:begin -->` sentinels)
- [ ] `cfmd pull <id-or-url>` command
- [ ] URL parsing to extract page ID
- [ ] Slugified output filename
- [ ] Unit tests: storage2md golden files, round-trip (md → storage → md) identity on supported subset

**Acceptance:** can pull a real page, get sensible markdown, edit it, push it back without losing anything.

### Phase 3 — Conflict handling & status
- [ ] Local cache implementation (`~/.cache/cfmd/pages/<id>/`)
- [ ] `cfmd status` command
- [ ] Three-way conflict detection in push
- [ ] Pretty diff output on conflict
- [ ] `--force` flag for push and pull

**Acceptance:** modify a page in the Confluence UI, then try to push local changes; see a clear conflict message.

### Phase 4 — Polish & advanced features
- [ ] `cfmd init` interactive setup
- [ ] `cfmd login` for keyring token storage
- [ ] GFM alerts ↔ info/warning/tip macros
- [ ] Image attachments (local paths in markdown → uploaded attachments)
- [ ] Mermaid code blocks → rendered PNG attachments (optional; cop-out: leave as code block with language `mermaid`, Confluence has a Mermaid macro in some setups)
- [ ] Retry logic with exponential backoff
- [ ] `--dry-run` for push
- [ ] Homebrew formula / install script

### Phase 5 (future, not v1)
- Bulk pull of a whole space
- `cfmd watch` daemon mode
- Comment preservation on update
- Labels / metadata sync

---

## 13. Testing strategy

### Unit tests: converters

Golden-file tests. Structure:

```
testdata/md2storage/
  headings/
    input.md
    expected.xhtml
  code_block/
    input.md
    expected.xhtml
  alerts/
    input.md
    expected.xhtml
  ...
```

Test runner reads `input.md`, runs it through the converter, compares to `expected.xhtml`. Update mode: `go test -update` rewrites golden files.

### Round-trip tests

For every supported construct, assert that `md → storage → md` is idempotent (modulo whitespace). Catches asymmetries in the two converters.

### Integration tests (optional, gated)

Behind a build tag `integration`:
- `CFMD_TEST_BASE_URL`, `CFMD_TEST_TOKEN`, `CFMD_TEST_SPACE` from env
- Create a page, pull it, modify, push, verify, delete
- Skip if env vars missing

### Manual test script

A `scripts/smoke.sh` that exercises the full CLI against a test space. Useful for the developer, not CI.

---

## 14. Security notes

1. **Never log tokens.** Sanitize all logging; redact `Authorization` header.
2. **Config file permissions.** When writing `config.yaml`, `chmod 0600`.
3. **Token lookup order** (documented in §5) prefers env/keyring over plaintext config.
4. **TLS.** Always verify certs. Provide `--insecure-skip-tls-verify` only behind a config flag, not CLI flag, and warn prominently.
5. **URL allowlisting.** When pulling by URL, verify the URL's host matches `config.base_url`'s host. Prevents accidentally fetching from a malicious URL a user was tricked into using.

---

## 15. Main.go sketch

```go
// cmd/cfmd/main.go
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"

    "github.com/yourorg/cfmd/internal/cli"
)

func main() {
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    if err := cli.NewRootCommand().ExecuteContext(ctx); err != nil {
        os.Exit(mapErrorToExitCode(err))
    }
}

func mapErrorToExitCode(err error) int {
    // See §6 exit codes
    // ...
    return 1
}
```

```go
// internal/cli/root.go
package cli

import (
    "github.com/spf13/cobra"
    "github.com/spf13/viper"
)

func NewRootCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "cfmd",
        Short: "Sync Confluence pages with local markdown files",
    }
    cmd.PersistentFlags().String("config", "", "config file path")
    cmd.PersistentFlags().BoolP("verbose", "v", false, "verbose output")
    _ = viper.BindPFlag("verbose", cmd.PersistentFlags().Lookup("verbose"))

    cmd.AddCommand(newPushCommand())
    cmd.AddCommand(newPullCommand())
    cmd.AddCommand(newStatusCommand())
    cmd.AddCommand(newInitCommand())
    cmd.AddCommand(newLoginCommand())
    return cmd
}
```

---

## 16. README checklist (for the shipped binary)

- [ ] Install: `go install github.com/yourorg/cfmd/cmd/cfmd@latest`
- [ ] Quick start: `cfmd init` → edit a file → `cfmd push file.md`
- [ ] Frontmatter reference (§4)
- [ ] IntelliJ setup (§11, with screenshots)
- [ ] Supported markdown constructs (§8 table)
- [ ] How macros round-trip (§9 passthrough)
- [ ] Troubleshooting: auth errors, conflict resolution, where the cache lives
- [ ] Limitations (no comment preservation, no attachment deletion, etc.)

---

## 17. Open questions for the implementer

1. **Page path resolution.** Should `cfmd` support pushing by path (e.g. `ENG > Architecture > Payments`) instead of only by ID? Useful but adds complexity. Recommend: not in v1.
2. **Multi-file push.** `cfmd push docs/` to push a whole directory? Recommend: not in v1 — use shell globbing and a loop.
3. **Emoji.** `:smile:` shortcodes — Confluence has emoji but the storage format differs. Recommend: pass through as Unicode, don't try to convert to `<ac:emoticon>`.
4. **Internal links.** `[other doc](./other.md)` where `other.md` is also a `cfmd`-managed file. Could rewrite to the Confluence page URL on push. Recommend: v1 leaves as a relative link (breaks on Confluence side). v2 resolves by walking sibling files for frontmatter IDs.

---

## 18. Done criteria for v1

- [ ] `cfmd push new-file.md` creates a page and rewrites the file with its ID.
- [ ] `cfmd push existing-file.md` updates the page with a version bump.
- [ ] `cfmd pull 12345 --out doc.md` produces a readable, editable markdown file.
- [ ] Push then pull then push produces no unintended diff on Confluence (round-trip stable for supported subset).
- [ ] Version conflict is detected and halts push with exit code 2.
- [ ] One IntelliJ External Tool entry invokes push on the current file via keyboard shortcut.
- [ ] Tests pass, `go vet` clean, `golangci-lint run` clean.
- [ ] Binary is ≤ 15 MB, starts in < 50 ms.

---

*End of original spec.*

---

## Appendix A — Amendments

This section logs every deviation from the original spec made during
implementation, with date and rationale. The original spec text above is kept
as-is for historical context; the **current contract is the amendments +
original where not amended**. The README is always kept in sync with the
current contract.

### A1. Passthrough format changed from HTML-comment sentinels to `cfmd-raw` fenced code block (2026-04-21)

**Original spec (§9):** Round-trip fidelity for macros we don't model is
preserved by wrapping the raw XML in
`<!-- cfmd:raw:begin -->` / `<!-- cfmd:raw:end -->` HTML-comment sentinels.

**Amended:** Passthrough blocks are written as fenced code blocks with the
language tag `cfmd-raw`:

````markdown
```cfmd-raw
<ac:structured-macro ac:name="expand">
  <ac:parameter ac:name="title">Click me</ac:parameter>
  <ac:rich-text-body><p>hidden content</p></ac:rich-text-body>
</ac:structured-macro>
```
````

**Why:** The HTML-comment form was invisible in markdown previews, which
caused the `<ac:...>` content between the comments to render as stray
paragraphs (because browsers treat unknown tags as transparent wrappers). The
fenced-block form renders as a neutral monospace code box in every markdown
previewer (GitHub, GitLab, VSCode, IntelliJ, Obsidian, browsers via any
CommonMark renderer), giving a clear visual "this is preserved XML, don't
edit casually" cue, and still round-trips byte-for-byte.

**Backward compatibility:** The old HTML-comment form is still accepted on
push. Only the pull direction changed.

**Fence length:** Picked to exceed the longest backtick run in the body
(CommonMark rule). Default is 3; bumped to 4+ automatically when content
contains triple backticks.

### A2. Expand macro gets an HTML5 representation (2026-04-21)

**Original spec:** The Expand macro fell under "opaque passthrough for
unknown macros" because markdown has no native collapsible-section syntax.

**Amended:** Expand (`<ac:structured-macro ac:name="expand">`) is converted
to an HTML5 `<details>/<summary>` pair in the markdown file:

```markdown
<details>
<summary>Click me</summary>

Hidden body content, **fully editable as markdown**, can include
lists, code blocks, and other block constructs.

</details>
```

On push, the `<details>` block is recognized, `<summary>` becomes the
Expand's title parameter, and the body is recursively converted through the
md → storage pipeline. Nesting: at most one level in v1 (a nested `<details>`
inside a `<details>` triggers passthrough fallback).

**Why:** `<details>/<summary>` renders as a real collapsible disclosure
widget in GitHub, GitLab, VSCode, IntelliJ, Obsidian, Bear, Typora, and
browsers directly. The body inside is native markdown, so users edit Expand
bodies the same way they edit any other region.

**Other HTML-mapped macros:** None in v1. Candidates for later: Info/Warning
with titles (`<details open><summary>[!NOTE] Title</summary>body</details>`),
Panel. Deliberately deferred to avoid over-engineering before real usage.

### A3. Preserve-unknown-attributes rule for images (2026-04-21)

**Original spec (§8):** Images emit minimal `<ac:image>` tags.

**Amended:** An `<ac:image>` is converted to `![alt](src)` markdown *only if*
its only attribute is `ac:alt` and its only child is a simple
`<ri:url>` or `<ri:attachment>`. Any of the other 10 documented attributes
(`ac:width`, `ac:height`, `ac:align`, `ac:border`, `ac:class`, `ac:style`,
`ac:title`, `ac:thumbnail`, `ac:vspace`, `ac:hspace` — see
[Atlassian docs](https://confluence.atlassian.com/doc/confluence-storage-format-790796544.html))
triggers passthrough so the image's dimensions and layout survive a
round-trip.

**Why:** The original "emit minimal form" rule would re-render a 600×400
image as an unsized image after a round-trip, snapping it to default
dimensions. Users would see their layouts broken after trivial text edits
elsewhere on the page. The preserve-attributes rule makes round-trip
strictly safe at the cost of visibility: images with attributes show up in
the markdown as `cfmd-raw` fences, not `![alt](src)`, which is honest about
the limitation.

### A4. Friendly code/info/warning/tip/note conversions are gated by parameter absence (2026-04-21)

**Original spec (§8, §9):** Code blocks always become fenced; info/warning/
etc. always become GFM alerts.

**Amended:** Only converted to markdown if the macro has no parameters other
than the ones markdown can express:

- `code`: converted to fenced block *only* if the sole parameter is
  `language`. Any of `title`, `theme`, `linenumbers`, `firstline`, `collapse`
  triggers passthrough.
- `info`, `warning`, `tip`, `note`: converted to GFM alert *only* if there
  are no parameters. Any `icon` or `title` triggers passthrough.

**Why:** Same reason as A3. A code block with `linenumbers=true` is visually
distinct in Confluence; losing that on round-trip breaks the page. An info
panel with a custom title would lose its title on round-trip.

### A5. Diff-before-push/pull is the default workflow (2026-04-21)

**Original spec (§6):** `cfmd push` and `cfmd pull` operate immediately. A
`--dry-run` flag exists for push.

**Amended:** Both `push` and `pull` show a diff and prompt for confirmation
by default. `--yes`/`-y` skips confirmation for automation use; `--dry-run`
always exits without modifying anything after showing the diff. A dedicated
`cfmd diff <file>` command shows the preview without any side effect. An
`--launch-intellij` flag opens the diff in IntelliJ's native three-pane
viewer via the `idea diff` CLI.

**Why:** Confluence edits are not cheap to undo (there's a version history
but no transactional rollback to before-your-push). A confirmation step
catches mistakes (wrong file targeted, conversion surprise, lost edit)
before they become a published page edit.

**Diff is markdown-to-markdown, not XML-to-markdown:** When previewing a
push, the remote's storage format is pulled and rendered through
`storage2md` so the user sees a markdown diff, not an XML diff. Same for
pull.

### A6. Macro support table is authoritative and lives in the README (2026-04-21)

A complete macro support matrix, drawn from Atlassian's official macro
documentation, is maintained in `README.md`. It lists every documented
built-in macro with its `ac:name`, its support tier (Markdown / HTML /
Passthrough), and the conditions for friendly conversion. Any change to
support coverage requires a table update in the same commit.

### A7. `docs/SPEC.md` + `README.md` are both sources of truth, with different scope (2026-04-21)

- `docs/SPEC.md` (this file): **design intent**. Frozen body above, dated
  amendments here.
- `README.md`: **operational reference**. Install, usage, macro table,
  diff workflow, troubleshooting.
- Any change that affects behavior must update both.

### A8. Bearer auth for Confluence Data Center / Server Personal Access Tokens (2026-04-21, v0.1.1)

**Original spec (§7):** Auth is HTTP Basic with
`Authorization: Basic base64(username:token)`. Target is Atlassian Cloud.

**Amended:** Confluence has two completely different hosting models with
different auth schemes, and cfmd now supports both:

- **Atlassian Cloud** (`*.atlassian.net`): HTTP Basic with
  `base64(email:api-token)`. Token from
  `id.atlassian.com/manage-profile/security/api-tokens`. This is the v0.1.0
  behavior — unchanged and still the default.
- **Confluence Data Center / Server** (self-hosted): HTTP **Bearer** with
  the raw Personal Access Token from the Confluence UI
  (Profile → Personal Access Tokens). Username is not used.

Selected via a new env var `CFMD_AUTH_MODE` with values `basic` (default,
Cloud) or `bearer` (DC). In bearer mode, `CFMD_USERNAME` is optional.

**Why:** v0.1.0 was built against the original spec's Cloud-only target.
First real-world tester was on a DC instance; Basic auth with a DC PAT
always 401s because DC expects `Authorization: Bearer <token>`. The two
schemes have different wire formats, not just different credential sources,
so a single code path cannot cover both.

**Base URL note:** DC installations usually do *not* include the `/wiki`
suffix that Cloud requires. Common DC forms:
`https://wiki.company.com`, `https://wiki.company.com/confluence`,
`https://docs.company.com`. The rule is: whatever prefix your browser URL
has **before** `/display/`, `/spaces/`, or `/pages/`.

**Backward compatibility:** `CFMD_AUTH_MODE` defaults to `basic` when
unset, so existing v0.1.0 `.env` files continue to work unchanged on Cloud.
