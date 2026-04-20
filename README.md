# cfmd — Confluence ⇄ Markdown Sync CLI

A Go binary that pulls a Confluence page to a local markdown file, lets you
edit it in the editor of your choice, and pushes it back with a diff preview
and confirmation step. Built to be safe on pre-existing pages: anything we
can't losslessly represent as markdown round-trips untouched.

> **Status:** v1 complete. Converters, CLI, REST client, diff workflow, and
> cache all implemented. 290 unit tests pass. See `Limitations` at the bottom
> for what's deliberately not supported.

## Design principles

1. **Never garble an existing page.** Every construct that cfmd cannot
   represent cleanly in markdown is preserved as a visible `cfmd-raw` fenced
   code block (or, for a chosen subset, as HTML). On push the block is
   emitted byte-for-byte. You can pull → edit anywhere → push and the bits of
   the page you didn't touch remain identical.
2. **No silent operations.** `push` and `pull` show a diff and require
   confirmation. A standalone `cfmd diff` command previews either direction.
3. **Authority is the Atlassian docs.** Feature matrix below cites the exact
   Atlassian page that documents each macro's storage format.

## Representation tiers

Every storage-format construct lands in exactly one of these tiers:

| Tier | How it looks in your .md | Editable as prose? | Preview friendly? |
|------|--------------------------|---------------------|-------------------|
| **Markdown** | Native markdown syntax | Yes | Yes |
| **HTML** | Native HTML5 element (`<details>`, etc.) | Yes (body) | Yes (renders as the real widget) |
| **Passthrough** | ` ```cfmd-raw ` fenced code block with the raw XML inside | Only by editing the XML | Renders as a neutral code box |

## Macro and construct support matrix

Round-trip fidelity — i.e., whether pull → edit elsewhere on the page → push
leaves this construct identical — is **guaranteed for every row in this
table**, regardless of tier. The "Tier" column is about how editable the
construct is in markdown form; it is not about fidelity.

### Non-macro constructs

| Construct | Tier | Markdown form (on pull) | Notes |
|-----------|------|-------------------------|-------|
| Headings `<h1>`…`<h6>` | Markdown | `# …`–`###### …` | |
| Paragraph `<p>` | Markdown | plain line | |
| Bold `<strong>` / `<b>` | Markdown | `**text**` | |
| Italic `<em>` / `<i>` | Markdown | `*text*` | |
| Strikethrough `<span style="text-decoration: line-through">` | Markdown | `~~text~~` | GFM |
| Inline code `<code>` | Markdown | `` `text` `` | |
| Blockquote `<blockquote>` | Markdown | `> text` | |
| Horizontal rule `<hr/>` | Markdown | `---` | |
| Bullet list `<ul>` / `<li>` | Markdown | `- item` | |
| Ordered list `<ol>` / `<li>` | Markdown | `1. item` | |
| Table `<table>`/`<tr>`/`<th>`/`<td>` | Markdown | GFM pipe table | |
| Hyperlink `<a href>` | Markdown | `[text](url)` | |
| Image `<ac:image>` with **only** `ac:alt` and a simple child | Markdown | `![alt](src)` | Any of `ac:width`, `ac:height`, `ac:align`, `ac:border`, `ac:thumbnail`, `ac:class`, `ac:style`, `ac:title`, `ac:vspace`, `ac:hspace` → passthrough. See [Atlassian image docs][storage-format]. |
| Image `<ac:image>` with extra attributes | Passthrough | `cfmd-raw` fence | Preserves width/height/align/… exactly |
| Link `<ac:link>` to attachment with plain-text body | Markdown | `[text](filename)` | Attachment name only |
| Link `<ac:link>` to `<ri:page>`, `<ri:user>`, `<ri:space>`, `<ri:blog-post>` | Passthrough (inline) | raw XML inline | Markdown cannot represent "link by page title" |

[storage-format]: https://confluence.atlassian.com/doc/confluence-storage-format-790796544.html

### Built-in macros

Drawn from Atlassian's [complete macro list][macro-list] (74 entries). Entries
without a row are in the "Passthrough — not yet modelled" group below.

[macro-list]: https://confluence.atlassian.com/doc/macros-139387.html

| Macro | `ac:name` | Tier | Markdown form | Condition for friendly conversion |
|-------|-----------|------|---------------|------------------------------------|
| Code Block | `code` | Markdown | ```` ```lang\n…\n``` ```` | Only if the sole `<ac:parameter>` is `language`. Any of `title`, `theme`, `linenumbers`, `firstline`, `collapse` → passthrough. ([docs](https://confluence.atlassian.com/doc/code-block-macro-139390.html)) |
| Info | `info` | Markdown | `> [!NOTE]` (GFM alert) | Only if no `icon`/`title` parameter. Else passthrough. ([docs](https://confluence.atlassian.com/conf59/info-tip-note-and-warning-macros-792499127.html)) |
| Warning | `warning` | Markdown | `> [!WARNING]` | Same condition |
| Tip | `tip` | Markdown | `> [!TIP]` | Same condition |
| Note | `note` | Markdown | `> [!IMPORTANT]` | Same condition |
| Expand | `expand` | HTML | `<details><summary>title</summary>body</details>` | Always — body is rendered recursively as markdown. Falls back to passthrough if the body contains a nested `<details>`. |
| Status | `status` | Passthrough | `cfmd-raw` fence | Inline color chip with `colour`/`title`/`subtle` params |
| Panel | `panel` | Passthrough | `cfmd-raw` fence | Custom colours/borders can't survive markdown sanitizers |
| Table of Contents | `toc` | Passthrough | `cfmd-raw` fence | Content is dynamically generated by Confluence |
| Children Display | `children` | Passthrough | `cfmd-raw` fence | Dynamic |
| Include Page | `include` | Passthrough | `cfmd-raw` fence | Dynamic |
| Excerpt | `excerpt` | Passthrough | `cfmd-raw` fence | Rich-text-body macro |
| Excerpt Include | `excerpt-include` | Passthrough | `cfmd-raw` fence | Dynamic |
| Page Properties | `details` | Passthrough | `cfmd-raw` fence | |
| Page Properties Report | `detailssummary` | Passthrough | `cfmd-raw` fence | Dynamic |
| Section | `section` | Passthrough | `cfmd-raw` fence | Layout |
| Column | `column` | Passthrough | `cfmd-raw` fence | Layout |
| Anchor | `anchor` | Passthrough | `cfmd-raw` fence | |
| Attachments | `attachments` | Passthrough | `cfmd-raw` fence | Dynamic list |
| Blog Posts | `blog-posts` | Passthrough | `cfmd-raw` fence | Dynamic |
| Change History | `change-history` | Passthrough | `cfmd-raw` fence | Dynamic |
| Chart | `chart` | Passthrough | `cfmd-raw` fence | 39 parameters |
| Cheese | `cheese` | Passthrough | `cfmd-raw` fence | |
| Content by Label | `contentbylabel` | Passthrough | `cfmd-raw` fence | Dynamic |
| Content by User | `content-by-user` | Passthrough | `cfmd-raw` fence | Dynamic |
| Content Report Table | `content-report-table` | Passthrough | `cfmd-raw` fence | Dynamic |
| Contributors | `contributors` | Passthrough | `cfmd-raw` fence | Dynamic |
| Contributors Summary | `contributors-summary` | Passthrough | `cfmd-raw` fence | Dynamic |
| Create Space Button | `create-space-button` | Passthrough | `cfmd-raw` fence | Action |
| Favourite Pages | `favpages` | Passthrough | `cfmd-raw` fence | Dynamic |
| Gadget | `gadget` | Passthrough | `cfmd-raw` fence | External |
| Gallery | `gallery` | Passthrough | `cfmd-raw` fence | Dynamic |
| Global Reports | `global-reports` | Passthrough | `cfmd-raw` fence | Dynamic |
| HTML | `html` | Passthrough | `cfmd-raw` fence | Raw HTML |
| HTML Include | `html-include` | Passthrough | `cfmd-raw` fence | External |
| IM Presence | `im` | Passthrough | `cfmd-raw` fence | |
| Jira Issues | `jiraissues` | Passthrough | `cfmd-raw` fence | External |
| JUnit Report | `junitreport` | Passthrough | `cfmd-raw` fence | External |
| Labels List | `listlabels` | Passthrough | `cfmd-raw` fence | Dynamic |
| Livesearch | `livesearch` | Passthrough | `cfmd-raw` fence | Dynamic |
| Loremipsum | `loremipsum` | Passthrough | `cfmd-raw` fence | |
| Multimedia | `multimedia` | Passthrough | `cfmd-raw` fence | Video/audio |
| Noformat | `noformat` | Passthrough | `cfmd-raw` fence | Use a plain code fence instead |
| Office Excel / Word / PowerPoint | `viewxls` / `viewdoc` / `viewppt` | Passthrough | `cfmd-raw` fence | External file |
| PDF | `viewpdf` | Passthrough | `cfmd-raw` fence | |
| Popular Labels | `popular-labels` | Passthrough | `cfmd-raw` fence | Dynamic |
| Profile Picture | `profile-picture` | Passthrough | `cfmd-raw` fence | Dynamic |
| Recently Updated | `recently-updated` | Passthrough | `cfmd-raw` fence | Dynamic |
| Recently Updated Dashboard | `recently-updated-dashboard` | Passthrough | `cfmd-raw` fence | Dynamic |
| Recently Used Labels | `recently-used-labels` | Passthrough | `cfmd-raw` fence | Dynamic |
| Related Labels | `related-labels` | Passthrough | `cfmd-raw` fence | Dynamic |
| Roadmap Planner | `roadmap` | Passthrough | `cfmd-raw` fence | Interactive |
| RSS Feed | `rss` | Passthrough | `cfmd-raw` fence | External |
| Search Results | `livesearch` / `search` | Passthrough | `cfmd-raw` fence | Dynamic |
| Space Attachments | `space-attachments` | Passthrough | `cfmd-raw` fence | Dynamic |
| Space Details | `space-details` | Passthrough | `cfmd-raw` fence | Dynamic |
| Spaces List | `spaces-list` | Passthrough | `cfmd-raw` fence | Dynamic |
| Task Report | `tasks-report-macro` | Passthrough | `cfmd-raw` fence | Dynamic |
| Team Calendar | `team-calendars` | Passthrough | `cfmd-raw` fence | External |
| User List | `listusers` | Passthrough | `cfmd-raw` fence | Dynamic |
| User Profile | `profile` | Passthrough | `cfmd-raw` fence | Dynamic |
| View File | `view-file` | Passthrough | `cfmd-raw` fence | External |
| Widget Connector | `widget` | Passthrough | `cfmd-raw` fence | External |

### Unknown macros (Marketplace add-ons, custom macros, future macros)

Any `<ac:structured-macro ac:name="…">` whose name is not in the list above is
emitted as a `cfmd-raw` fence. No data loss, no special handling needed — the
XML round-trips byte-for-byte.

## Adding friendly support for a new macro

If you use a macro heavily and want to edit its body as clean markdown,
the extension point is `internal/convert/storage2md/parser.go`'s
`renderStructuredMacro`. Follow the pattern the Expand macro uses
(HTML mapping with recursive body conversion) or the info-alert pattern
(markdown mapping with "is this simple enough to convert?" gate).

Contributions are welcome for:

- Panel with title/bg-color → `<details open>` with inline style (if GitHub
  preview tolerates it)
- Info/Warning/Tip/Note with titles → `<details open><summary>[!NOTE] Title</summary>body</details>`
- Status → `` `[STATUS: colour title]` ``

## Diff workflow

`cfmd push` and `cfmd pull` never commit changes without showing a diff and
getting a confirmation.

- `cfmd push file.md` fetches the current remote, renders it back to markdown
  using `storage2md` (so you're comparing markdown to markdown, not storage
  XML to markdown), shows a unified diff between that and your local body,
  and prompts `Apply these changes to Confluence? [y/N]`.
- `cfmd pull <id>` fetches the remote, renders it to markdown, shows a diff
  against the existing local file (if any), and prompts
  `Overwrite local file with these changes? [y/N]`.
- `cfmd diff file.md` is a standalone no-op that renders both the expected
  diff-for-push and the diff-for-pull without touching either side.
- `--yes` / `-y` skips confirmation; `--dry-run` shows the diff and exits
  with code 0 always.

For IntelliJ integration, `cfmd diff file.md --launch-intellij` will write
the two sides to temp files and shell out to
`idea diff <tmp-left> <tmp-right>` so the diff opens in IntelliJ's native
three-pane viewer. (Requires the IntelliJ CLI `idea` launcher.)

## Installation

### Prebuilt binaries (recommended)

Download the archive for your OS/arch from the
[Releases](https://github.com/aravindarc/cfmd/releases) page and put the
`cfmd` (or `cfmd.exe`) binary somewhere on your `PATH`.

```bash
# macOS (Apple Silicon)
curl -L -o cfmd.tar.gz https://github.com/aravindarc/cfmd/releases/latest/download/cfmd-darwin-arm64.tar.gz
tar -xzf cfmd.tar.gz && sudo mv cfmd /usr/local/bin/

# macOS (Intel)
curl -L -o cfmd.tar.gz https://github.com/aravindarc/cfmd/releases/latest/download/cfmd-darwin-amd64.tar.gz
tar -xzf cfmd.tar.gz && sudo mv cfmd /usr/local/bin/

# Linux (amd64)
curl -L -o cfmd.tar.gz https://github.com/aravindarc/cfmd/releases/latest/download/cfmd-linux-amd64.tar.gz
tar -xzf cfmd.tar.gz && sudo mv cfmd /usr/local/bin/

# Linux (arm64)
curl -L -o cfmd.tar.gz https://github.com/aravindarc/cfmd/releases/latest/download/cfmd-linux-arm64.tar.gz
tar -xzf cfmd.tar.gz && sudo mv cfmd /usr/local/bin/
```

On **Windows**, download `cfmd-windows-amd64.zip` (or `-arm64.zip`), extract
`cfmd.exe`, and move it to a directory on your `PATH` (e.g.
`C:\Users\<you>\bin\`).

### Build from source

```bash
git clone https://github.com/aravindarc/cfmd
cd cfmd
go build -o cfmd ./cmd/cfmd
```

Requires Go 1.21+.

## Configuration (environment variables only)

cfmd reads all configuration from the environment. No YAML, no keyring, no
interactive setup beyond `cfmd init`, which writes a `.env` template.

| Variable | Required | Description |
|----------|----------|-------------|
| `CFMD_BASE_URL` | yes (for push/pull) | Confluence Cloud wiki URL, e.g. `https://yourco.atlassian.net/wiki` (no trailing slash) |
| `CFMD_USERNAME` | yes | Account email for Basic auth |
| `CFMD_TOKEN` | yes | Atlassian API token |
| `CFMD_DEFAULT_SPACE` | no | Space key used when a file's frontmatter omits `space` |
| `CFMD_DEFAULT_PARENT_ID` | no | Parent page id used when a file's frontmatter omits `parent_id` |
| `CFMD_TIMEOUT_SECONDS` | no (default 30) | Per-request HTTP timeout |
| `CFMD_CACHE_DIR` | no | Override cache directory (default: `$XDG_CACHE_HOME/cfmd` or `~/.cache/cfmd`) |
| `CFMD_ALLOW_INSECURE_TLS` | no | Set to `true` to skip cert verification (debugging only) |

Create an API token at
[id.atlassian.com](https://id.atlassian.com/manage-profile/security/api-tokens).

### Using a `.env` file

Run `cfmd init` in your working directory to write an annotated template:

```
CFMD_BASE_URL=https://yourco.atlassian.net/wiki
CFMD_USERNAME=you@company.com
CFMD_TOKEN=...
```

cfmd reads `.env` from the current directory on startup and fills in any env
var that is not already set. Values in the real environment take precedence.

**Add `.env` to your `.gitignore` — it contains an API token.**

## Command reference

| Command | Description |
|---------|-------------|
| `cfmd init` | Writes a `.env` template. |
| `cfmd push <file>` | Pushes a cfmd-managed markdown file to Confluence. Shows a diff and prompts before committing. `--yes` skips the prompt; `--dry-run` shows the diff and exits; `--force` ignores version mismatches. |
| `cfmd pull <id-or-url>` | Pulls a Confluence page to a local markdown file. Shows a diff vs any existing local file and prompts before overwriting. Same flags as push. `--out <path>` overrides the default slugified filename. |
| `cfmd diff <file>` | Shows the diff between a local file and its remote page without modifying either. Exit code 0 = identical, 1 = differ. |
| `cfmd status <file>` | Prints local vs remote version, and qualitative state: `in_sync`, `local_ahead`, `remote_ahead`, `diverged`. |
| `cfmd convert md-to-storage <file>` | Local-only: render markdown to storage-format XHTML on stdout. |
| `cfmd convert storage-to-md <file>` | Local-only: render storage-format XHTML to markdown (reads stdin if file is `-`). |

### Common flags

- `-y`, `--yes` — skip confirmation prompts
- `--dry-run` — show what would happen, make no changes
- `--force` — proceed despite version mismatch (push) or unsaved local changes (pull)
- `--idea` — in addition to printing the diff, launch IntelliJ's native diff viewer via the `idea` CLI launcher (must be on `PATH`; Install from IntelliJ via **Tools → Create Command-line Launcher**)
- `-v`, `--verbose` — debug logging to stderr

### Diff workflow and IntelliJ integration

Every `push` and `pull` writes both sides of the diff to
`<cache_dir>/pages/<page_id>/diff.local.md` and `diff.remote.md`, then prints
a unified diff, then shows a line like:

```
Diff files written:
  local  → diff.local.md
  remote → diff.remote.md
Open in IntelliJ: idea diff /Users/you/.cache/cfmd/pages/12345/diff.local.md /Users/you/.cache/cfmd/pages/12345/diff.remote.md
```

In IntelliJ's built-in terminal the file paths render as clickable OSC-8
hyperlinks (click to open the file in the editor). Copy-paste the
`idea diff` line to open the native three-pane diff viewer. Or pass `--idea`
to have cfmd run that command for you.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success (or no diff for `cfmd diff`) |
| 1 | Generic failure, user cancel, or diff present (for `cfmd diff`) |
| 2 | Version conflict — remote has advanced since last sync |
| 3 | Authentication failure (401/403) |
| 4 | Other network / API error |

## File format

A cfmd-managed markdown file starts with HTML-comment frontmatter. Example:

```markdown
<!-- cfmd:page_id: 123456789 -->
<!-- cfmd:space: ENG -->
<!-- cfmd:title: Payment Service Redesign -->
<!-- cfmd:parent_id: 987654321 -->
<!-- cfmd:version: 12 -->
<!-- cfmd:last_synced: 2026-04-21T10:30:00Z -->

# Payment Service Redesign
…
```

On first push (no `page_id`), the page is created and the file is rewritten
with `page_id`, `version`, and `last_synced` injected.

## Build and test

```bash
go build -o cfmd ./cmd/cfmd
go test ./...                    # runs ~290 unit + golden + round-trip tests
go test ./... -run TestGolden -update   # rewrite golden files after intentional changes
go vet ./...
```

## Limitations (deliberate, v1)

- **No automatic attachment upload.** Local images (`![](./img.png)`) are
  converted to `<ac:image><ri:attachment .../></ac:image>` tags that expect
  the file to already exist as a page attachment. Upload manually via the
  Confluence UI or add a script that calls `POST /rest/api/content/{id}/child/attachment`.
- **No internal-link rewriting.** A markdown link to another cfmd-managed
  file won't auto-convert to the target page's Confluence URL. Either edit
  the URL manually or rely on Confluence's text search.
- **No bulk push/pull.** Use a shell loop: `for f in docs/*.md; do cfmd push -y $f; done`.
- **No comment preservation.** Page comments are not synced.
- **Single HTML-mapped macro.** Only Expand is converted to `<details>`. Every
  other macro with no markdown equivalent is preserved via `cfmd-raw` fence.

## Provenance for the macro table

The macro names and parameter lists come directly from Atlassian's official
storage-format documentation:

- [Confluence Storage Format][storage-format] (images, links, namespaces)
- [Confluence Storage Format for Macros][storage-format-macros] (37 macros
  with parameters)
- [Macros index][macro-list] (74 macros total)
- [Info/Tip/Note/Warning macros][info-macros]
- [Code Block macro][code-macro]
- [Status macro][status-macro]

[storage-format-macros]: https://confluence.atlassian.com/pages/viewpage.action?pageId=329980084
[info-macros]: https://confluence.atlassian.com/conf59/info-tip-note-and-warning-macros-792499127.html
[code-macro]: https://confluence.atlassian.com/doc/code-block-macro-139390.html
[status-macro]: https://confluence.atlassian.com/conf59/status-macro-792499207.html
