// Package md2storage converts markdown (with GFM extensions) into
// Confluence Storage Format XHTML.
//
// It is implemented as a goldmark custom renderer. The public API is
// Convert(input) -> (storage xhtml, error).
package md2storage

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Convert turns markdown into Confluence storage format.
//
// The pipeline is:
//  1. Extract `<details>...</details>` blocks into placeholders. Each is
//     converted to an Expand macro: the <summary> becomes the title parameter
//     and the body is recursively converted to storage format as markdown.
//  2. Extract opaque-passthrough blocks — both the legacy HTML-comment form
//     (`<!-- cfmd:raw:begin --> ... <!-- cfmd:raw:end -->`, accepted for
//     backward compatibility) and the new fenced form (```cfmd-raw fences).
//     Goldmark would otherwise re-escape the XML inside them.
//  3. Run the preprocessed source through goldmark with our custom renderer.
//  4. Splice the raw/expand blocks back into the output.
func Convert(src []byte) (string, error) {
	preprocessed, expandBlocks, err := extractDetailsBlocks(src)
	if err != nil {
		return "", err
	}
	preprocessed, rawBlocks := extractRawBlocks(preprocessed)
	preprocessed, fencedRawBlocks := extractFencedRawBlocks(preprocessed)

	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM, // tables, strikethrough, task lists, linkify
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(
				util.Prioritized(&storageRenderer{}, 100),
			),
		),
	)

	reader := text.NewReader(preprocessed)
	doc := md.Parser().Parse(reader)

	var out bytes.Buffer
	if err := md.Renderer().Render(&out, preprocessed, doc); err != nil {
		return "", err
	}
	result := out.String()
	result = restoreFencedRawBlocks(result, fencedRawBlocks)
	result = restoreRawBlocks(result, rawBlocks)
	result = restoreExpandBlocks(result, expandBlocks)
	result = normalizeOutput(result)
	return result, nil
}

// rawBlockPattern matches the `<!-- cfmd:raw:begin -->\n...\n<!-- cfmd:raw:end -->`
// sentinel used to mark storage-format XML that must survive round-trip
// untouched.
var rawBlockPattern = regexp.MustCompile(`(?s)<!--\s*cfmd:raw:begin\s*-->\s*\n(.*?)\n\s*<!--\s*cfmd:raw:end\s*-->`)

func extractRawBlocks(src []byte) ([]byte, []string) {
	var blocks []string
	replaced := rawBlockPattern.ReplaceAllFunc(src, func(m []byte) []byte {
		inner := rawBlockPattern.FindSubmatch(m)[1]
		idx := len(blocks)
		blocks = append(blocks, strings.TrimRight(string(inner), "\n"))
		// Placeholder: a fenced HTML comment on its own line. Goldmark will
		// emit it unchanged as an HTML block.
		return []byte(fmt.Sprintf("<!--CFMD_RAW_%d-->", idx))
	})
	return replaced, blocks
}

// placeholderPattern matches the placeholder emitted by extractRawBlocks.
var placeholderPattern = regexp.MustCompile(`<!--CFMD_RAW_(\d+)-->`)

func restoreRawBlocks(s string, blocks []string) string {
	return placeholderPattern.ReplaceAllStringFunc(s, func(m string) string {
		sub := placeholderPattern.FindStringSubmatch(m)
		var idx int
		fmt.Sscanf(sub[1], "%d", &idx)
		if idx < 0 || idx >= len(blocks) {
			return m
		}
		return blocks[idx]
	})
}

// normalizeOutput trims trailing whitespace and collapses runs of blank lines.
func normalizeOutput(s string) string {
	s = strings.TrimLeft(s, "\n")
	s = strings.TrimRight(s, " \t\n")
	return s
}

// --- cfmd-raw fenced code block extraction --------------------------------

// extractFencedRawBlocks walks src line by line, finds fenced code blocks
// whose info string is "cfmd-raw", extracts their body, and replaces the
// whole block (including its fences) with a placeholder HTML comment on its
// own line.
//
// Go's regexp package lacks backreferences, so we can't express "match a
// fence N backticks long, then later a fence of the same length" in a single
// regex. A line scanner is clearer and handles any fence length.
func extractFencedRawBlocks(src []byte) ([]byte, []string) {
	var blocks []string
	lines := strings.Split(string(src), "\n")
	var out strings.Builder
	i := 0
	for i < len(lines) {
		line := lines[i]
		openFence, ok := parseCfmdRawFence(line)
		if !ok {
			out.WriteString(line)
			if i < len(lines)-1 {
				out.WriteByte('\n')
			}
			i++
			continue
		}
		// Find the matching closing fence: same length of backticks, possibly
		// indented up to 3 spaces, with only optional trailing whitespace.
		j := i + 1
		var bodyLines []string
		closed := false
		for ; j < len(lines); j++ {
			if isMatchingCloseFence(lines[j], openFence) {
				closed = true
				break
			}
			bodyLines = append(bodyLines, lines[j])
		}
		if !closed {
			// Malformed — no closing fence. Emit verbatim so the user sees it.
			out.WriteString(line)
			out.WriteByte('\n')
			i++
			continue
		}
		idx := len(blocks)
		blocks = append(blocks, strings.Join(bodyLines, "\n"))
		// Emit placeholder on its own line. Blank lines around ensure goldmark
		// treats it as its own block.
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "<!--CFMD_FENCED_RAW_%d-->\n", idx)
		i = j + 1
	}
	s := out.String()
	if strings.HasSuffix(s, "\n") && !strings.HasSuffix(string(src), "\n") {
		s = strings.TrimRight(s, "\n")
	}
	return []byte(s), blocks
}

// parseCfmdRawFence returns the opening fence (a string of backticks) if the
// line is a valid opener `\`{3,}cfmd-raw[ws]*$` with up to 3 leading spaces.
func parseCfmdRawFence(line string) (string, bool) {
	// Strip up to 3 leading spaces.
	lead := 0
	for lead < 3 && lead < len(line) && line[lead] == ' ' {
		lead++
	}
	rest := line[lead:]
	// Count backticks.
	ticks := 0
	for ticks < len(rest) && rest[ticks] == '`' {
		ticks++
	}
	if ticks < 3 {
		return "", false
	}
	info := strings.TrimSpace(rest[ticks:])
	if info != "cfmd-raw" {
		return "", false
	}
	return strings.Repeat("`", ticks), true
}

// isMatchingCloseFence returns true if the line is a closing fence of the
// same length as the opener, with optional leading indent up to 3 spaces and
// optional trailing whitespace and no info string.
func isMatchingCloseFence(line, opener string) bool {
	lead := 0
	for lead < 3 && lead < len(line) && line[lead] == ' ' {
		lead++
	}
	rest := line[lead:]
	if !strings.HasPrefix(rest, opener) {
		return false
	}
	tail := rest[len(opener):]
	// Reject if there are more backticks immediately (longer fence).
	if len(tail) > 0 && tail[0] == '`' {
		return false
	}
	if strings.TrimSpace(tail) != "" {
		return false
	}
	return true
}

var fencedRawPlaceholderPattern = regexp.MustCompile(`<!--CFMD_FENCED_RAW_(\d+)-->`)

func restoreFencedRawBlocks(s string, blocks []string) string {
	return fencedRawPlaceholderPattern.ReplaceAllStringFunc(s, func(m string) string {
		sub := fencedRawPlaceholderPattern.FindStringSubmatch(m)
		var idx int
		fmt.Sscanf(sub[1], "%d", &idx)
		if idx < 0 || idx >= len(blocks) {
			return m
		}
		return blocks[idx]
	})
}

// --- <details> block extraction (Expand macro mapping) --------------------

// detailsBlockPattern is a coarse matcher. We find the outermost
// `<details ...>` ... `</details>` spans at the block level and hand-parse the
// interior. Nested details are handled by a stateful scanner in
// extractDetailsBlocks.
//
// The scanner recognizes `<details>` and `<details ...attrs>` as an opener;
// `</details>` as a closer; with depth tracking.

func extractDetailsBlocks(src []byte) ([]byte, []string, error) {
	text := string(src)
	var out strings.Builder
	var blocks []string

	i := 0
	for i < len(text) {
		start := findDetailsOpen(text, i)
		if start < 0 {
			out.WriteString(text[i:])
			break
		}
		// Copy content up to the opener.
		out.WriteString(text[i:start])

		// Find the matching </details> respecting nesting.
		end, err := findDetailsClose(text, start)
		if err != nil {
			return nil, nil, err
		}
		segment := text[start:end] // includes both <details...> and </details>
		expandXML, err := convertDetailsToExpand(segment)
		if err != nil {
			return nil, nil, err
		}
		idx := len(blocks)
		blocks = append(blocks, expandXML)
		// Emit a placeholder on its own line so goldmark treats it as an HTML
		// block.
		out.WriteString("\n<!--CFMD_EXPAND_")
		fmt.Fprintf(&out, "%d", idx)
		out.WriteString("-->\n")
		i = end
	}
	return []byte(out.String()), blocks, nil
}

var detailsOpenPattern = regexp.MustCompile(`(?i)<details(\s[^>]*)?>`)
var detailsClosePattern = regexp.MustCompile(`(?i)</details\s*>`)
var summaryPattern = regexp.MustCompile(`(?is)<summary(?:\s[^>]*)?>(.*?)</summary>`)

// findDetailsOpen returns the byte index of the next `<details>` opening tag
// at or after `from`, or -1.
func findDetailsOpen(text string, from int) int {
	loc := detailsOpenPattern.FindStringIndex(text[from:])
	if loc == nil {
		return -1
	}
	return from + loc[0]
}

// findDetailsClose walks forward from the opener at `from`, tracking nesting,
// and returns the index just past the matching `</details>` tag.
func findDetailsClose(text string, from int) (int, error) {
	// Start scanning after the opening tag itself.
	openLoc := detailsOpenPattern.FindStringIndex(text[from:])
	if openLoc == nil {
		return 0, fmt.Errorf("internal: details opener missing")
	}
	pos := from + openLoc[1]
	depth := 1
	for pos < len(text) {
		nextOpen := detailsOpenPattern.FindStringIndex(text[pos:])
		nextClose := detailsClosePattern.FindStringIndex(text[pos:])
		if nextClose == nil {
			return 0, fmt.Errorf("unterminated <details> block starting at byte %d", from)
		}
		closeAbs := pos + nextClose[1]
		if nextOpen != nil && pos+nextOpen[0] < pos+nextClose[0] {
			depth++
			pos = pos + nextOpen[1]
			continue
		}
		depth--
		if depth == 0 {
			return closeAbs, nil
		}
		pos = closeAbs
	}
	return 0, fmt.Errorf("unterminated <details> block starting at byte %d", from)
}

// convertDetailsToExpand takes a full `<details ...>...</details>` segment
// and returns the equivalent storage-format Expand macro XML. The body
// between <summary> and </details> is converted from markdown to storage
// format recursively.
func convertDetailsToExpand(segment string) (string, error) {
	// Strip the outer <details...> and </details> tags.
	openLoc := detailsOpenPattern.FindStringIndex(segment)
	if openLoc == nil {
		return "", fmt.Errorf("no <details> opener")
	}
	inner := segment[openLoc[1]:]
	// Trim the trailing </details>.
	closeLoc := detailsClosePattern.FindAllStringIndex(inner, -1)
	if len(closeLoc) == 0 {
		return "", fmt.Errorf("no </details> closer")
	}
	// Use the last close match (matches the outer one after stripping).
	last := closeLoc[len(closeLoc)-1]
	inner = inner[:last[0]]

	// Extract <summary>...</summary> as the title; strip it from the body.
	title := ""
	if m := summaryPattern.FindStringSubmatchIndex(inner); m != nil {
		title = strings.TrimSpace(inner[m[2]:m[3]])
		// Remove the summary from the body.
		inner = inner[:m[0]] + inner[m[1]:]
	}
	bodyMD := strings.TrimSpace(inner)

	// Recursively convert the body from markdown to storage.
	var bodyStorage string
	if bodyMD != "" {
		conv, err := Convert([]byte(bodyMD))
		if err != nil {
			return "", fmt.Errorf("expand body: %w", err)
		}
		bodyStorage = conv
	}

	var b strings.Builder
	b.WriteString(`<ac:structured-macro ac:name="expand">`)
	if title != "" && title != "Click to expand" {
		fmt.Fprintf(&b, `<ac:parameter ac:name="title">%s</ac:parameter>`, xmlEscape(unescapeHTMLEntities(title)))
	}
	b.WriteString(`<ac:rich-text-body>`)
	b.WriteString(bodyStorage)
	b.WriteString(`</ac:rich-text-body></ac:structured-macro>`)
	return b.String(), nil
}

// unescapeHTMLEntities decodes the small set of HTML entities the storage2md
// converter may have emitted in a <summary>: &amp; &lt; &gt;.
func unescapeHTMLEntities(s string) string {
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return s
}

var expandPlaceholderPattern = regexp.MustCompile(`<!--CFMD_EXPAND_(\d+)-->`)

func restoreExpandBlocks(s string, blocks []string) string {
	return expandPlaceholderPattern.ReplaceAllStringFunc(s, func(m string) string {
		sub := expandPlaceholderPattern.FindStringSubmatch(m)
		var idx int
		fmt.Sscanf(sub[1], "%d", &idx)
		if idx < 0 || idx >= len(blocks) {
			return m
		}
		return blocks[idx]
	})
}

// storageRenderer implements renderer.NodeRenderer. It emits Confluence
// storage format XHTML for every goldmark AST node.
type storageRenderer struct{}

func (r *storageRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	// Blocks
	reg.Register(ast.KindDocument, r.renderDocument)
	reg.Register(ast.KindHeading, r.renderHeading)
	reg.Register(ast.KindParagraph, r.renderParagraph)
	reg.Register(ast.KindBlockquote, r.renderBlockquote)
	reg.Register(ast.KindList, r.renderList)
	reg.Register(ast.KindListItem, r.renderListItem)
	reg.Register(ast.KindThematicBreak, r.renderThematicBreak)
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedCodeBlock)
	reg.Register(ast.KindCodeBlock, r.renderCodeBlock)
	reg.Register(ast.KindHTMLBlock, r.renderHTMLBlock)

	// Inlines
	reg.Register(ast.KindText, r.renderText)
	reg.Register(ast.KindString, r.renderString)
	reg.Register(ast.KindEmphasis, r.renderEmphasis)
	reg.Register(ast.KindCodeSpan, r.renderCodeSpan)
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindImage, r.renderImage)
	reg.Register(ast.KindAutoLink, r.renderAutoLink)
	reg.Register(ast.KindRawHTML, r.renderRawHTML)

	// GFM
	reg.Register(extast.KindStrikethrough, r.renderStrikethrough)
	reg.Register(extast.KindTable, r.renderTable)
	reg.Register(extast.KindTableHeader, r.renderTableHeader)
	reg.Register(extast.KindTableRow, r.renderTableRow)
	reg.Register(extast.KindTableCell, r.renderTableCell)
	reg.Register(extast.KindTaskCheckBox, r.renderTaskCheckBox)
}

// --- Blocks ----------------------------------------------------------------

func (r *storageRenderer) renderDocument(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderHeading(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	h := n.(*ast.Heading)
	if entering {
		fmt.Fprintf(w, "<h%d>", h.Level)
	} else {
		fmt.Fprintf(w, "</h%d>\n", h.Level)
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderParagraph(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	// Suppress <p> wrappers inside tight list items.
	if _, ok := n.Parent().(*ast.ListItem); ok {
		if parentList, ok := n.Parent().Parent().(*ast.List); ok && parentList.IsTight {
			return ast.WalkContinue, nil
		}
	}
	if entering {
		_, _ = w.WriteString("<p>")
	} else {
		_, _ = w.WriteString("</p>\n")
	}
	return ast.WalkContinue, nil
}

// admonitionRegexp matches "[!TYPE]" at the very start of a blockquote's first
// paragraph, per GFM alerts.
var admonitionRegexp = regexp.MustCompile(`(?s)^\s*\[!(NOTE|WARNING|TIP|IMPORTANT|CAUTION)\][ \t]*\n?`)

// macroForAlert maps a GFM alert type to the Confluence macro name.
func macroForAlert(kind string) string {
	switch kind {
	case "NOTE":
		return "info"
	case "WARNING", "CAUTION":
		return "warning"
	case "TIP":
		return "tip"
	case "IMPORTANT":
		return "note"
	}
	return ""
}

func (r *storageRenderer) renderBlockquote(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if kind, ok := detectAlert(n, src); ok {
		if entering {
			macro := macroForAlert(kind)
			// Render children to a buffer, then strip the alert marker.
			var buf bytes.Buffer
			bw := bufio.NewWriter(&buf)
			for child := n.FirstChild(); child != nil; child = child.NextSibling() {
				renderSubtree(bw, child, src)
			}
			_ = bw.Flush()
			body := buf.String()
			// Strip the "[!TYPE]" marker (and optional trailing newline) from
			// the first text content. We strip inside a `<p>...</p>` wrapper
			// so we don't accidentally mangle a `<p>` tag itself.
			body = stripAlertMarkerFromFirstP(body)
			fmt.Fprintf(w, `<ac:structured-macro ac:name="%s"><ac:rich-text-body>`, macro)
			_, _ = w.WriteString(body)
			_, _ = w.WriteString(`</ac:rich-text-body></ac:structured-macro>` + "\n")
			return ast.WalkSkipChildren, nil
		}
		// Skip the closing </blockquote> that would otherwise be emitted.
		return ast.WalkContinue, nil
	}
	if entering {
		_, _ = w.WriteString("<blockquote>")
	} else {
		_, _ = w.WriteString("</blockquote>\n")
	}
	return ast.WalkContinue, nil
}

// stripAlertMarkerFromFirstP removes the leading "[!TYPE]" marker from the
// first `<p>...</p>` in body. It is a simple regex replace because the alert
// marker always lives at the start of the first paragraph text.
func stripAlertMarkerFromFirstP(body string) string {
	// Match the first occurrence of `<p>[!TYPE]...` and strip the marker
	// inside the <p>. Use (?s) so `.` matches newlines, but limit by requiring
	// the match end before the </p>.
	re := regexp.MustCompile(`<p>\s*\[!(NOTE|WARNING|TIP|IMPORTANT|CAUTION)\][ \t]*\n?`)
	return re.ReplaceAllString(body, "<p>")
}

func detectAlert(n ast.Node, src []byte) (string, bool) {
	p, ok := n.FirstChild().(*ast.Paragraph)
	if !ok {
		return "", false
	}
	txt := gatherText(p, src)
	m := admonitionRegexp.FindStringSubmatch(txt)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// renderSubtree runs the full renderer over a subtree. It walks the node tree
// dispatching to the same render methods as the main renderer.
func renderSubtree(w util.BufWriter, n ast.Node, src []byte) {
	walker := &subtreeWalker{w: w, src: src}
	_ = ast.Walk(n, walker.visit)
}

type subtreeWalker struct {
	w   util.BufWriter
	src []byte
}

func (sw *subtreeWalker) visit(n ast.Node, entering bool) (ast.WalkStatus, error) {
	r := &storageRenderer{}
	switch n.Kind() {
	case ast.KindHeading:
		return r.renderHeading(sw.w, sw.src, n, entering)
	case ast.KindParagraph:
		return r.renderParagraph(sw.w, sw.src, n, entering)
	case ast.KindBlockquote:
		return r.renderBlockquote(sw.w, sw.src, n, entering)
	case ast.KindList:
		return r.renderList(sw.w, sw.src, n, entering)
	case ast.KindListItem:
		return r.renderListItem(sw.w, sw.src, n, entering)
	case ast.KindThematicBreak:
		return r.renderThematicBreak(sw.w, sw.src, n, entering)
	case ast.KindFencedCodeBlock:
		return r.renderFencedCodeBlock(sw.w, sw.src, n, entering)
	case ast.KindCodeBlock:
		return r.renderCodeBlock(sw.w, sw.src, n, entering)
	case ast.KindHTMLBlock:
		return r.renderHTMLBlock(sw.w, sw.src, n, entering)
	case ast.KindText:
		return r.renderText(sw.w, sw.src, n, entering)
	case ast.KindString:
		return r.renderString(sw.w, sw.src, n, entering)
	case ast.KindEmphasis:
		return r.renderEmphasis(sw.w, sw.src, n, entering)
	case ast.KindCodeSpan:
		return r.renderCodeSpan(sw.w, sw.src, n, entering)
	case ast.KindLink:
		return r.renderLink(sw.w, sw.src, n, entering)
	case ast.KindImage:
		return r.renderImage(sw.w, sw.src, n, entering)
	case ast.KindAutoLink:
		return r.renderAutoLink(sw.w, sw.src, n, entering)
	case ast.KindRawHTML:
		return r.renderRawHTML(sw.w, sw.src, n, entering)
	case extast.KindStrikethrough:
		return r.renderStrikethrough(sw.w, sw.src, n, entering)
	case extast.KindTable:
		return r.renderTable(sw.w, sw.src, n, entering)
	case extast.KindTableHeader:
		return r.renderTableHeader(sw.w, sw.src, n, entering)
	case extast.KindTableRow:
		return r.renderTableRow(sw.w, sw.src, n, entering)
	case extast.KindTableCell:
		return r.renderTableCell(sw.w, sw.src, n, entering)
	case extast.KindTaskCheckBox:
		return r.renderTaskCheckBox(sw.w, sw.src, n, entering)
	default:
		return ast.WalkContinue, nil
	}
}

// gatherText concatenates the text content of a node's descendants.
func gatherText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := c.(*ast.Text); ok {
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() {
				b.WriteByte('\n')
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

func (r *storageRenderer) renderList(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	l := n.(*ast.List)
	tag := "ul"
	if l.IsOrdered() {
		tag = "ol"
	}
	if entering {
		fmt.Fprintf(w, "<%s>", tag)
	} else {
		fmt.Fprintf(w, "</%s>\n", tag)
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderListItem(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<li>")
	} else {
		_, _ = w.WriteString("</li>")
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderThematicBreak(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<hr/>\n")
	}
	return ast.WalkContinue, nil
}

// escapeCDATA splits the body across two CDATA sections wherever "]]>" appears.
func escapeCDATA(body []byte) []byte {
	return bytes.ReplaceAll(body, []byte("]]>"), []byte("]]]]><![CDATA[>"))
}

func (r *storageRenderer) renderFencedCodeBlock(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	block := n.(*ast.FencedCodeBlock)
	lang := ""
	if ln := block.Language(src); ln != nil {
		lang = string(ln)
	}
	var body bytes.Buffer
	for i := 0; i < block.Lines().Len(); i++ {
		line := block.Lines().At(i)
		body.Write(line.Value(src))
	}
	_, _ = w.WriteString(`<ac:structured-macro ac:name="code">`)
	if lang != "" {
		fmt.Fprintf(w, `<ac:parameter ac:name="language">%s</ac:parameter>`, xmlAttrEscape(lang))
	}
	_, _ = w.WriteString(`<ac:plain-text-body><![CDATA[`)
	_, _ = w.Write(escapeCDATA(body.Bytes()))
	_, _ = w.WriteString(`]]></ac:plain-text-body></ac:structured-macro>` + "\n")
	return ast.WalkSkipChildren, nil
}

func (r *storageRenderer) renderCodeBlock(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	block := n.(*ast.CodeBlock)
	var body bytes.Buffer
	for i := 0; i < block.Lines().Len(); i++ {
		line := block.Lines().At(i)
		body.Write(line.Value(src))
	}
	_, _ = w.WriteString(`<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[`)
	_, _ = w.Write(escapeCDATA(body.Bytes()))
	_, _ = w.WriteString(`]]></ac:plain-text-body></ac:structured-macro>` + "\n")
	return ast.WalkSkipChildren, nil
}

func (r *storageRenderer) renderHTMLBlock(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	block := n.(*ast.HTMLBlock)
	for i := 0; i < block.Lines().Len(); i++ {
		line := block.Lines().At(i)
		_, _ = w.Write(line.Value(src))
	}
	if block.HasClosure() {
		cl := block.ClosureLine
		_, _ = w.Write(cl.Value(src))
	}
	return ast.WalkSkipChildren, nil
}

// --- Inlines ---------------------------------------------------------------

func (r *storageRenderer) renderText(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	t := n.(*ast.Text)
	seg := t.Segment
	writeEscaped(w, seg.Value(src))
	if t.HardLineBreak() {
		_, _ = w.WriteString("<br/>")
	} else if t.SoftLineBreak() {
		_ = w.WriteByte('\n')
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderString(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	s := n.(*ast.String)
	writeEscaped(w, s.Value)
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderEmphasis(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	e := n.(*ast.Emphasis)
	tag := "em"
	if e.Level == 2 {
		tag = "strong"
	}
	if entering {
		fmt.Fprintf(w, "<%s>", tag)
	} else {
		fmt.Fprintf(w, "</%s>", tag)
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderCodeSpan(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<code>")
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				writeEscaped(w, t.Segment.Value(src))
			}
		}
		_, _ = w.WriteString("</code>")
		return ast.WalkSkipChildren, nil
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderLink(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	link := n.(*ast.Link)
	if entering {
		fmt.Fprintf(w, `<a href="%s"`, xmlAttrEscape(string(link.Destination)))
		if len(link.Title) > 0 {
			fmt.Fprintf(w, ` title="%s"`, xmlAttrEscape(string(link.Title)))
		}
		_, _ = w.WriteString(">")
	} else {
		_, _ = w.WriteString("</a>")
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderImage(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	img := n.(*ast.Image)
	dest := string(img.Destination)
	alt := gatherText(img, src)

	// Remote URL → ri:url
	if strings.HasPrefix(dest, "http://") || strings.HasPrefix(dest, "https://") {
		if alt != "" {
			fmt.Fprintf(w, `<ac:image ac:alt="%s"><ri:url ri:value="%s"/></ac:image>`,
				xmlAttrEscape(alt), xmlAttrEscape(dest))
		} else {
			fmt.Fprintf(w, `<ac:image><ri:url ri:value="%s"/></ac:image>`, xmlAttrEscape(dest))
		}
		return ast.WalkSkipChildren, nil
	}

	// Local path → attachment reference; rely on push-side upload using basename.
	base := dest
	if idx := strings.LastIndexAny(base, "/\\"); idx >= 0 {
		base = base[idx+1:]
	}
	if alt != "" {
		fmt.Fprintf(w, `<ac:image ac:alt="%s"><ri:attachment ri:filename="%s"/></ac:image>`,
			xmlAttrEscape(alt), xmlAttrEscape(base))
	} else {
		fmt.Fprintf(w, `<ac:image><ri:attachment ri:filename="%s"/></ac:image>`, xmlAttrEscape(base))
	}
	return ast.WalkSkipChildren, nil
}

func (r *storageRenderer) renderAutoLink(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	al := n.(*ast.AutoLink)
	url := string(al.URL(src))
	label := string(al.Label(src))
	if al.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(url, "mailto:") {
		url = "mailto:" + url
	}
	fmt.Fprintf(w, `<a href="%s">%s</a>`, xmlAttrEscape(url), xmlEscape(label))
	return ast.WalkSkipChildren, nil
}

func (r *storageRenderer) renderRawHTML(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	raw := n.(*ast.RawHTML)
	for i := 0; i < raw.Segments.Len(); i++ {
		seg := raw.Segments.At(i)
		_, _ = w.Write(seg.Value(src))
	}
	return ast.WalkSkipChildren, nil
}

// --- GFM -------------------------------------------------------------------

func (r *storageRenderer) renderStrikethrough(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString(`<span style="text-decoration: line-through;">`)
	} else {
		_, _ = w.WriteString(`</span>`)
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderTable(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<table><tbody>")
	} else {
		_, _ = w.WriteString("</tbody></table>\n")
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderTableHeader(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<tr>")
	} else {
		_, _ = w.WriteString("</tr>")
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderTableRow(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<tr>")
	} else {
		_, _ = w.WriteString("</tr>")
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderTableCell(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	tag := "td"
	if _, ok := n.Parent().(*extast.TableHeader); ok {
		tag = "th"
	}
	if entering {
		fmt.Fprintf(w, "<%s>", tag)
	} else {
		fmt.Fprintf(w, "</%s>", tag)
	}
	return ast.WalkContinue, nil
}

func (r *storageRenderer) renderTaskCheckBox(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	tc := n.(*extast.TaskCheckBox)
	if tc.IsChecked {
		_, _ = w.WriteString(`<input type="checkbox" checked="checked"/> `)
	} else {
		_, _ = w.WriteString(`<input type="checkbox"/> `)
	}
	return ast.WalkContinue, nil
}

// --- Helpers ---------------------------------------------------------------

// writeEscaped writes text content XML-escaped (for PCDATA).
func writeEscaped(w util.BufWriter, p []byte) {
	for _, b := range p {
		switch b {
		case '&':
			_, _ = w.WriteString("&amp;")
		case '<':
			_, _ = w.WriteString("&lt;")
		case '>':
			_, _ = w.WriteString("&gt;")
		default:
			_ = w.WriteByte(b)
		}
	}
}

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func xmlAttrEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
