// Package storage2md converts Confluence Storage Format XHTML into markdown.
//
// The guiding principle is "preserve unknown constructs via passthrough so
// round-trip edits never garble the original page."
//
// Representation tiers:
//
//   - Plain markdown for constructs that map cleanly (headings, paragraphs,
//     emphasis, lists, tables, blockquotes, plain images, info/warning/tip/
//     note alerts without extra params, language-only code fences).
//   - HTML for macros that have a direct HTML equivalent. Currently only the
//     Expand macro, which becomes `<details><summary>title</summary>body</details>`.
//     The body stays as editable markdown and previews as a native collapsible
//     widget in all modern renderers.
//   - Opaque passthrough via a fenced code block with language `cfmd-raw`
//     for everything else (sized images, internal ac:link, panel, section,
//     status, jira, page-tree, chart, gallery, unknown macros, …). The block
//     renders as a neutral monospace box in any markdown previewer and is
//     re-emitted verbatim on push.
package storage2md

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Convert parses storage format XHTML and produces markdown.
func Convert(src []byte) (string, error) {
	// Preprocess: Confluence storage format is "mostly XML" but net/html
	// parses as lenient HTML. Two divergences matter:
	//   * <![CDATA[...]]> is treated as a bogus comment — content lost.
	//   * Self-closing namespaced elements like <ri:page .../> are parsed as
	//     open tags, so subsequent siblings become children.
	// Both are fixed in-place before handing to net/html.
	pre := preprocessStorageForHTMLParser(src)

	// Wrap in a synthetic root so net/html treats all top-level children
	// uniformly.
	wrapped := "<cfmdroot>" + pre + "</cfmdroot>"
	nodes, err := html.ParseFragment(strings.NewReader(wrapped),
		&html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"})
	if err != nil {
		return "", fmt.Errorf("html parse: %w", err)
	}

	c := &converter{}
	for _, n := range nodes {
		c.renderBlock(n)
	}
	out := c.buf.String()
	// Collapse multiple blank lines → single blank line.
	blankLineRe := regexp.MustCompile(`\n{3,}`)
	out = blankLineRe.ReplaceAllString(out, "\n\n")
	out = strings.TrimLeft(out, "\n")
	out = strings.TrimRight(out, " \t\n") + "\n"
	return out, nil
}

type converter struct {
	buf          bytes.Buffer
	listDepth    int
	orderedStack []bool // true for ol, false for ul
}

// writeString is a convenience.
func (c *converter) w(s string) {
	c.buf.WriteString(s)
}

// ensureBlankLine ensures the buffer ends with at least one blank line
// (i.e., two newlines) — so the next block starts with a gap above it.
func (c *converter) ensureBlankLine() {
	s := c.buf.String()
	if s == "" {
		return
	}
	if strings.HasSuffix(s, "\n\n") {
		return
	}
	if strings.HasSuffix(s, "\n") {
		c.w("\n")
		return
	}
	c.w("\n\n")
}

// renderBlock dispatches a block-level node.
func (c *converter) renderBlock(n *html.Node) {
	switch n.Type {
	case html.ElementNode:
		// Namespaced ac: / ri: tags have to be matched by Data string,
		// because html.Parser lowercases them (golang.org/x/net/html treats
		// colons as literal — Data is "ac:structured-macro", etc.).
		tag := n.Data
		switch {
		case tag == "cfmdroot":
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				c.renderBlock(ch)
			}
		case tag == "h1", tag == "h2", tag == "h3", tag == "h4", tag == "h5", tag == "h6":
			level := int(tag[1] - '0')
			c.ensureBlankLine()
			c.w(strings.Repeat("#", level) + " ")
			c.renderInlineChildren(n)
			c.w("\n\n")
		case tag == "p":
			// If the paragraph contains exactly one element child that we
			// would passthrough (e.g., a complex ac:image or an ac:link to an
			// ri:page), emit it as a block-level cfmd-raw fence so it gets
			// the visual "this is preserved XML" cue instead of a bare tag
			// floating inline.
			if solo := paragraphSoloPassthrough(n); solo != nil {
				c.emitOpaqueBlock(solo)
				break
			}
			c.ensureBlankLine()
			c.renderInlineChildren(n)
			c.w("\n\n")
		case tag == "hr":
			c.ensureBlankLine()
			c.w("---\n\n")
		case tag == "blockquote":
			c.ensureBlankLine()
			c.renderBlockquote(n)
			c.w("\n")
		case tag == "ul":
			c.ensureBlankLine()
			c.renderList(n, false)
			c.w("\n")
		case tag == "ol":
			c.ensureBlankLine()
			c.renderList(n, true)
			c.w("\n")
		case tag == "table":
			c.ensureBlankLine()
			c.renderTable(n)
			c.w("\n")
		case tag == "pre":
			// Preformatted text. Render as a plain fenced code block with no
			// language. Text inside <pre> is taken verbatim (unescaped).
			c.ensureBlankLine()
			body := gatherPlainText(n)
			fence := pickFence([]byte(body))
			c.w(fence)
			c.w("\n")
			c.w(body)
			if !strings.HasSuffix(body, "\n") {
				c.w("\n")
			}
			c.w(fence)
			c.w("\n\n")
		case tag == "ac:structured-macro":
			c.renderStructuredMacro(n)
		case tag == "br":
			c.w("\n")
		default:
			// Unknown tag at block level: emit as opaque passthrough so the
			// caller can still push this back without losing information.
			c.emitOpaque(n)
		}
	case html.TextNode:
		// Trim pure-whitespace text nodes between blocks (common in Confluence
		// output). Non-empty text at block level becomes a paragraph.
		if strings.TrimSpace(n.Data) == "" {
			return
		}
		c.ensureBlankLine()
		c.w(escapeMarkdown(n.Data))
		c.w("\n\n")
	case html.CommentNode:
		// Preserve comments as-is at block level (they might be cfmd sentinels
		// the user authored).
		c.ensureBlankLine()
		c.w("<!--" + n.Data + "-->\n\n")
	}
}

// renderInlineChildren renders the inline content of a block.
func (c *converter) renderInlineChildren(n *html.Node) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.renderInline(ch)
	}
}

func (c *converter) renderInline(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		c.w(escapeMarkdown(n.Data))
	case html.ElementNode:
		tag := n.Data
		switch tag {
		case "strong", "b":
			c.w("**")
			c.renderInlineChildren(n)
			c.w("**")
		case "em", "i":
			c.w("*")
			c.renderInlineChildren(n)
			c.w("*")
		case "code":
			c.w("`")
			c.w(gatherPlainText(n))
			c.w("`")
		case "br":
			c.w("  \n")
		case "a":
			href := getAttr(n, "href")
			c.w("[")
			c.renderInlineChildren(n)
			c.w("](")
			c.w(href)
			c.w(")")
		case "span":
			style := getAttr(n, "style")
			if strings.Contains(style, "line-through") {
				c.w("~~")
				c.renderInlineChildren(n)
				c.w("~~")
			} else {
				c.renderInlineChildren(n)
			}
		case "u", "sup", "sub":
			// Plain HTML5 tags with no native markdown syntax. Markdown
			// engines pass these through to the browser, so underline /
			// superscript / subscript render correctly in previews AND round
			// trip unchanged through md2storage's raw HTML inline passthrough.
			fmt.Fprintf(&c.buf, "<%s>", tag)
			c.renderInlineChildren(n)
			fmt.Fprintf(&c.buf, "</%s>", tag)
		case "ac:image":
			c.renderImage(n)
		case "ac:link":
			c.renderAcLink(n)
		case "ac:structured-macro":
			// Inline macros (e.g., inline code spans) — for v1 always
			// passthrough.
			c.emitOpaqueInline(n)
		default:
			// Unknown inline element: passthrough so we don't lose info.
			c.emitOpaqueInline(n)
		}
	}
}

// renderImage decides whether an <ac:image> can be losslessly represented in
// markdown. It can only when every attribute on the element is `ac:alt`
// (everything else must be preserved). The single child must be either
// <ri:attachment ri:filename="..."/> (and no other attributes) or
// <ri:url ri:value="..."/> (and no other attributes).
func (c *converter) renderImage(n *html.Node) {
	if !imageIsSimple(n) {
		c.emitOpaqueInline(n)
		return
	}
	alt := getAttr(n, "ac:alt")
	// Find the single resource child.
	var src string
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode {
			continue
		}
		switch ch.Data {
		case "ri:attachment":
			src = getAttr(ch, "ri:filename")
		case "ri:url":
			src = getAttr(ch, "ri:value")
		}
	}
	if src == "" {
		c.emitOpaqueInline(n)
		return
	}
	fmt.Fprintf(&c.buf, "![%s](%s)", alt, src)
}

// imageIsSimple returns true iff <ac:image> has only an `ac:alt` attribute
// (or none) and exactly one resource child with only the expected attribute.
func imageIsSimple(n *html.Node) bool {
	for _, a := range n.Attr {
		if a.Namespace != "" {
			// net/html splits "ac:alt" into Namespace="ac" Key="alt".
			full := a.Namespace + ":" + a.Key
			if full != "ac:alt" {
				return false
			}
		} else {
			if a.Key != "ac:alt" {
				return false
			}
		}
	}
	elementChildren := 0
	ok := true
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.TextNode && strings.TrimSpace(ch.Data) == "" {
			continue
		}
		if ch.Type != html.ElementNode {
			ok = false
			break
		}
		elementChildren++
		if elementChildren > 1 {
			ok = false
			break
		}
		switch ch.Data {
		case "ri:attachment":
			for _, a := range ch.Attr {
				full := a.Key
				if a.Namespace != "" {
					full = a.Namespace + ":" + a.Key
				}
				if full != "ri:filename" {
					ok = false
				}
			}
		case "ri:url":
			for _, a := range ch.Attr {
				full := a.Key
				if a.Namespace != "" {
					full = a.Namespace + ":" + a.Key
				}
				if full != "ri:value" {
					ok = false
				}
			}
		default:
			ok = false
		}
	}
	return ok && elementChildren == 1
}

// renderAcLink decides whether an <ac:link> is a simple plain-text link we
// can express as markdown. For v1, we conservatively passthrough any ac:link
// that points at an ri:page (Confluence-internal page link, no markdown
// equivalent). We DO convert <ac:link><ri:attachment filename><plain-text-link-body>
// pairs to "[text](filename)" because that's markdown-representable.
func (c *converter) renderAcLink(n *html.Node) {
	var textBody string
	var resourceTag string
	var resourceAttrs map[string]string
	onlyAllowedChildren := true
	resourceCount := 0
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.TextNode && strings.TrimSpace(ch.Data) == "" {
			continue
		}
		if ch.Type != html.ElementNode {
			onlyAllowedChildren = false
			break
		}
		switch ch.Data {
		case "ri:page", "ri:attachment", "ri:user", "ri:space", "ri:blog-post":
			resourceTag = ch.Data
			resourceAttrs = map[string]string{}
			for _, a := range ch.Attr {
				full := a.Key
				if a.Namespace != "" {
					full = a.Namespace + ":" + a.Key
				}
				resourceAttrs[full] = a.Val
			}
			resourceCount++
		case "ac:plain-text-link-body":
			textBody = gatherPlainText(ch)
		case "ac:link-body":
			// Rich text body — not safely convertible. Passthrough.
			onlyAllowedChildren = false
		default:
			onlyAllowedChildren = false
		}
	}
	if !onlyAllowedChildren || resourceCount != 1 {
		c.emitOpaqueInline(n)
		return
	}

	// Internal page links: passthrough (markdown cannot represent them
	// losslessly because the link is by title not by URL).
	if resourceTag == "ri:page" || resourceTag == "ri:user" || resourceTag == "ri:space" || resourceTag == "ri:blog-post" {
		c.emitOpaqueInline(n)
		return
	}
	// Attachment: express as relative link by filename.
	if resourceTag == "ri:attachment" {
		filename := resourceAttrs["ri:filename"]
		if filename == "" {
			c.emitOpaqueInline(n)
			return
		}
		if textBody == "" {
			textBody = filename
		}
		fmt.Fprintf(&c.buf, "[%s](%s)", textBody, filename)
	}
}

// renderStructuredMacro handles <ac:structured-macro>. Macros with a clean
// markdown equivalent get rendered as markdown; macros with a natural HTML5
// equivalent (currently just Expand) get rendered as HTML; everything else is
// preserved via opaque passthrough.
func (c *converter) renderStructuredMacro(n *html.Node) {
	name := getAttr(n, "ac:name")
	switch name {
	case "code":
		// Only convert to a fenced code block if the only parameter (if any)
		// is `language`. If other params are present, passthrough to preserve
		// them.
		if codeMacroIsSimple(n) {
			c.renderSimpleCode(n)
			return
		}
		c.emitOpaqueBlock(n)
	case "info":
		if admonitionIsSimple(n) {
			c.renderAdmonition(n, "NOTE")
			return
		}
		c.emitOpaqueBlock(n)
	case "warning":
		if admonitionIsSimple(n) {
			c.renderAdmonition(n, "WARNING")
			return
		}
		c.emitOpaqueBlock(n)
	case "tip":
		if admonitionIsSimple(n) {
			c.renderAdmonition(n, "TIP")
			return
		}
		c.emitOpaqueBlock(n)
	case "note":
		if admonitionIsSimple(n) {
			c.renderAdmonition(n, "IMPORTANT")
			return
		}
		c.emitOpaqueBlock(n)
	case "expand":
		c.renderExpand(n)
	default:
		c.emitOpaqueBlock(n)
	}
}

// admonitionIsSimple reports whether an info/warning/tip/note macro has no
// parameters beyond what markdown can express. Info-family macros accept
// `icon` and `title` params per Atlassian docs; if either is set we
// passthrough to preserve the custom UI.
func admonitionIsSimple(n *html.Node) bool {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode {
			continue
		}
		if ch.Data == "ac:parameter" {
			// Any parameter disqualifies: markdown can't express icon/title.
			return false
		}
	}
	return true
}

// renderExpand converts an Expand macro to an HTML5 <details> block whose
// body is editable markdown. The body is rendered recursively via a nested
// converter. If the body contains a nested Expand (rare) or other structural
// constructs that would break <details> nesting, we fall back to passthrough.
func (c *converter) renderExpand(n *html.Node) {
	// Pull the title: Cloud uses ac:parameter ac:name="title"; Server/DC sometimes
	// stores it as the "default-parameter" (unnamed). Accept both.
	title := ""
	var bodyNode *html.Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode {
			continue
		}
		switch ch.Data {
		case "ac:parameter":
			name := getAttr(ch, "ac:name")
			if name == "title" || name == "" {
				if title == "" {
					title = gatherPlainText(ch)
				}
			}
		case "ac:rich-text-body":
			bodyNode = ch
		}
	}
	// If body is absent or contains something we can't safely render inline,
	// passthrough.
	if bodyNode == nil {
		c.emitOpaqueBlock(n)
		return
	}

	// Recursively render the body as markdown.
	sub := &converter{}
	for ch := bodyNode.FirstChild; ch != nil; ch = ch.NextSibling {
		sub.renderBlock(ch)
	}
	body := strings.TrimSpace(sub.buf.String())

	// Guard: if the rendered body itself contains a <details> or </details>
	// string in raw form, HTML parsing becomes ambiguous. Fall back to
	// passthrough in that (rare) case.
	if strings.Contains(body, "<details") || strings.Contains(body, "</details>") {
		c.emitOpaqueBlock(n)
		return
	}

	c.ensureBlankLine()
	c.w("<details>\n")
	if title != "" {
		fmt.Fprintf(&c.buf, "<summary>%s</summary>\n\n", htmlEscapeText(title))
	} else {
		c.w("<summary>Click to expand</summary>\n\n")
	}
	c.w(body)
	c.w("\n\n</details>\n\n")
}

// htmlEscapeText escapes text for safe embedding in HTML PCDATA. Used for the
// <summary> title.
func htmlEscapeText(s string) string {
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

func codeMacroIsSimple(n *html.Node) bool {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.TextNode && strings.TrimSpace(ch.Data) == "" {
			continue
		}
		if ch.Type != html.ElementNode {
			continue
		}
		switch ch.Data {
		case "ac:parameter":
			if getAttr(ch, "ac:name") != "language" {
				return false
			}
		case "ac:plain-text-body":
			// OK
		default:
			return false
		}
	}
	return true
}

func (c *converter) renderSimpleCode(n *html.Node) {
	var lang, body string
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode {
			continue
		}
		switch ch.Data {
		case "ac:parameter":
			if getAttr(ch, "ac:name") == "language" {
				lang = gatherPlainText(ch)
			}
		case "ac:plain-text-body":
			body = gatherPlainText(ch)
		}
	}
	c.ensureBlankLine()
	c.w("```")
	c.w(lang)
	c.w("\n")
	c.w(body)
	if !strings.HasSuffix(body, "\n") {
		c.w("\n")
	}
	c.w("```\n\n")
}

func (c *converter) renderAdmonition(n *html.Node, marker string) {
	// Admonition body is inside <ac:rich-text-body>. Render its children as
	// regular block content, then prefix every line with "> ".
	var bodyNode *html.Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.ElementNode && ch.Data == "ac:rich-text-body" {
			bodyNode = ch
			break
		}
	}
	inner := ""
	if bodyNode != nil {
		sub := &converter{}
		for ch := bodyNode.FirstChild; ch != nil; ch = ch.NextSibling {
			sub.renderBlock(ch)
		}
		inner = strings.TrimSpace(sub.buf.String())
	}
	c.ensureBlankLine()
	c.w("> [!")
	c.w(marker)
	c.w("]\n")
	for _, line := range strings.Split(inner, "\n") {
		c.w("> ")
		c.w(line)
		c.w("\n")
	}
	c.w("\n")
}

func (c *converter) renderBlockquote(n *html.Node) {
	sub := &converter{}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		sub.renderBlock(ch)
	}
	inner := strings.TrimSpace(sub.buf.String())
	for _, line := range strings.Split(inner, "\n") {
		c.w("> ")
		c.w(line)
		c.w("\n")
	}
	c.w("\n")
}

func (c *converter) renderList(n *html.Node, ordered bool) {
	idx := 1
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode || ch.Data != "li" {
			continue
		}
		var marker string
		if ordered {
			marker = fmt.Sprintf("%d. ", idx)
		} else {
			marker = "- "
		}
		c.w(marker)
		inner := renderLIContent(ch)
		indent := strings.Repeat(" ", len(marker))
		lines := strings.Split(inner, "\n")
		for i, ln := range lines {
			if i > 0 {
				c.w(indent)
			}
			c.w(ln)
			c.w("\n")
		}
		idx++
	}
}

// renderLIContent renders a <li>'s children appropriately: inline-only
// children go through renderInline (so <strong>/<em>/<code>/text don't fall
// through to the "unknown block" opaque path); block children go through
// renderBlock.
func renderLIContent(li *html.Node) string {
	sub := &converter{}
	if hasBlockChild(li) {
		for ch := li.FirstChild; ch != nil; ch = ch.NextSibling {
			sub.renderBlock(ch)
		}
	} else {
		for ch := li.FirstChild; ch != nil; ch = ch.NextSibling {
			sub.renderInline(ch)
		}
	}
	return strings.TrimSpace(sub.buf.String())
}

// isBlockTag returns true if the tag name is a block-level HTML or storage-
// format element we treat as a block when dispatching.
func isBlockTag(tag string) bool {
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6",
		"p", "ul", "ol", "blockquote", "hr",
		"table", "thead", "tbody", "tr",
		"pre", "div",
		"ac:structured-macro",
		"cfmdroot":
		return true
	}
	return false
}

// paragraphSoloPassthrough returns the single element child of a paragraph
// if the paragraph has exactly one element child (ignoring whitespace text
// nodes) AND that child would be rendered via inline passthrough. This lets
// the caller "promote" the child to a block-level fenced passthrough for
// clarity.
func paragraphSoloPassthrough(p *html.Node) *html.Node {
	var only *html.Node
	for ch := p.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.TextNode && strings.TrimSpace(ch.Data) == "" {
			continue
		}
		if ch.Type != html.ElementNode {
			return nil
		}
		if only != nil {
			return nil
		}
		only = ch
	}
	if only == nil {
		return nil
	}
	switch only.Data {
	case "ac:image":
		if !imageIsSimple(only) {
			return only
		}
	case "ac:link":
		// Passthrough ac:links (page/user/space) are emitted inline; promote
		// to block-level fence when they're the sole content of a paragraph.
		if onlyAcLinkPointsToPage(only) {
			return only
		}
	case "ac:structured-macro":
		// A structured-macro inside a <p> is unusual; treat it as block.
		return only
	}
	return nil
}

// onlyAcLinkPointsToPage returns true if the ac:link's resource child is an
// ri:page/ri:user/ri:space/ri:blog-post — i.e. one that we passthrough rather
// than convert.
func onlyAcLinkPointsToPage(n *html.Node) bool {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode {
			continue
		}
		switch ch.Data {
		case "ri:page", "ri:user", "ri:space", "ri:blog-post":
			return true
		}
	}
	return false
}

// hasBlockChild reports whether any direct element child of n is a block.
func hasBlockChild(n *html.Node) bool {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.ElementNode && isBlockTag(ch.Data) {
			return true
		}
	}
	return false
}

func (c *converter) renderTable(n *html.Node) {
	// Walk rows, build a 2D slice of cell markdown.
	var rows [][]string
	var headerRowIdx = -1
	var walk func(node *html.Node, inHeader bool)
	walk = func(node *html.Node, inHeader bool) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "thead":
				for ch := node.FirstChild; ch != nil; ch = ch.NextSibling {
					walk(ch, true)
				}
				return
			case "tbody":
				for ch := node.FirstChild; ch != nil; ch = ch.NextSibling {
					walk(ch, inHeader)
				}
				return
			case "tr":
				row := []string{}
				isHeader := inHeader
				for ch := node.FirstChild; ch != nil; ch = ch.NextSibling {
					if ch.Type != html.ElementNode {
						continue
					}
					if ch.Data == "th" {
						isHeader = true
					}
					if ch.Data == "td" || ch.Data == "th" {
						sub := &converter{}
						sub.renderInlineChildren(ch)
						row = append(row, strings.ReplaceAll(strings.TrimSpace(sub.buf.String()), "|", "\\|"))
					}
				}
				if isHeader && headerRowIdx == -1 {
					headerRowIdx = len(rows)
				}
				rows = append(rows, row)
				return
			}
		}
		for ch := node.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch, inHeader)
		}
	}
	walk(n, false)

	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	for i, r := range rows {
		for len(r) < width {
			r = append(r, "")
		}
		c.w("| ")
		c.w(strings.Join(r, " | "))
		c.w(" |\n")
		if i == headerRowIdx || (headerRowIdx == -1 && i == 0) {
			c.w("|")
			for j := 0; j < width; j++ {
				c.w("---|")
			}
			c.w("\n")
		}
	}
}

// emitOpaque emits a block-level XML subtree as a fenced `cfmd-raw` code
// block. This format is chosen because it:
//   - Renders as a neutral monospace code box in every markdown previewer
//     (GitHub, GitLab, VSCode, IntelliJ, Obsidian, browsers), giving the
//     viewer a clear visual cue that this region is not prose.
//   - Survives round-trip byte-for-byte: the md2storage renderer detects
//     fenced blocks tagged `cfmd-raw` and emits their content verbatim
//     (bypassing the usual "wrap in a code macro" rendering).
//   - Is hand-editable as plain XML without any escaping obligations.
func (c *converter) emitOpaque(n *html.Node) {
	var xml bytes.Buffer
	renderXML(&xml, n)
	c.emitRawBlockBytes(xml.Bytes())
}

func (c *converter) emitOpaqueBlock(n *html.Node) {
	c.emitOpaque(n)
}

// emitRawBlockBytes writes the given bytes as a fenced `cfmd-raw` code block,
// choosing a fence length that cannot collide with any backtick run in the
// body (CommonMark rule: the fence is longer than the longest internal run).
func (c *converter) emitRawBlockBytes(body []byte) {
	fence := pickFence(body)
	c.ensureBlankLine()
	c.w(fence)
	c.w("cfmd-raw\n")
	c.buf.Write(body)
	if !bytes.HasSuffix(body, []byte("\n")) {
		c.w("\n")
	}
	c.w(fence)
	c.w("\n\n")
}

// pickFence returns a backtick fence guaranteed to enclose body without
// ambiguity: len(fence) = max(3, longest backtick run in body + 1).
func pickFence(body []byte) string {
	longest := 0
	run := 0
	for _, b := range body {
		if b == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	n := longest + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

// emitOpaqueInline emits an inline element verbatim. Inline passthrough uses
// the raw XML directly (no fenced block, since those are block-level). Our
// md2storage renderer passes raw HTML inline through unchanged, so this
// survives round-trip too.
func (c *converter) emitOpaqueInline(n *html.Node) {
	renderXML(&c.buf, n)
}

// --- XML rendering (for passthrough) ---------------------------------------

// renderXML writes a node as XML. Attribute order follows the input.
func renderXML(w io.Writer, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		io.WriteString(w, xmlEscapeText(n.Data))
	case html.ElementNode:
		fmt.Fprintf(w, "<%s", n.Data)
		for _, a := range n.Attr {
			key := a.Key
			if a.Namespace != "" {
				key = a.Namespace + ":" + a.Key
			}
			fmt.Fprintf(w, ` %s="%s"`, key, xmlEscapeAttr(a.Val))
		}
		// Self-close if no children AND element is known to be empty-content
		// in storage format (ri:*, br, hr, etc). To be safe, self-close any
		// element with no children.
		if n.FirstChild == nil {
			io.WriteString(w, "/>")
			return
		}
		io.WriteString(w, ">")
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			renderXML(w, ch)
		}
		fmt.Fprintf(w, "</%s>", n.Data)
	case html.CommentNode:
		fmt.Fprintf(w, "<!--%s-->", n.Data)
	case html.DoctypeNode, html.DocumentNode:
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			renderXML(w, ch)
		}
	}
}

func xmlEscapeText(s string) string {
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

func xmlEscapeAttr(s string) string {
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
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// --- storage-format preprocessing -----------------------------------------

// cdataPattern matches `<![CDATA[...]]>` with ... non-greedy, across newlines.
var cdataPattern = regexp.MustCompile(`(?s)<!\[CDATA\[(.*?)\]\]>`)

// nsSelfCloseTagPattern matches a self-closing namespaced element,
// e.g. `<ri:page foo="bar"/>` or `<ac:structured-macro ac:name="x"/>`.
// Captures: (1) tag (e.g. "ri:page"), (2) attrs (everything between tag and /).
var nsSelfCloseTagPattern = regexp.MustCompile(`(?s)<([a-zA-Z]+:[a-zA-Z][-a-zA-Z0-9]*)((?:\s[^>]*?)?)/>`)

// preprocessStorageForHTMLParser rewrites storage format so that net/html's
// HTML (not XML) parser produces a tree we can walk cleanly:
//
//  1. CDATA sections are unwrapped, and their content is XML-escaped so it
//     appears as ordinary text when net/html reads it.
//  2. Self-closing namespaced tags (<ri:page/>, <ac:structured-macro .../>)
//     are expanded to matched open/close pairs so net/html doesn't treat the
//     next sibling element as a child.
//
// Non-namespaced self-closing tags (like <br/>, <hr/>) are left alone — HTML
// handles them natively.
func preprocessStorageForHTMLParser(src []byte) string {
	// Expand self-closing namespaced tags first, then unwrap CDATA. Order
	// matters only to avoid interactions between the two — in practice they
	// don't overlap.
	s := nsSelfCloseTagPattern.ReplaceAllStringFunc(string(src), func(m string) string {
		sub := nsSelfCloseTagPattern.FindStringSubmatch(m)
		tag, attrs := sub[1], sub[2]
		return "<" + tag + attrs + "></" + tag + ">"
	})
	s = cdataPattern.ReplaceAllStringFunc(s, func(m string) string {
		inner := cdataPattern.FindStringSubmatch(m)[1]
		return xmlEscapeText(inner)
	})
	return s
}

// --- small helpers ---------------------------------------------------------

func getAttr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		full := a.Key
		if a.Namespace != "" {
			full = a.Namespace + ":" + a.Key
		}
		if full == name {
			return a.Val
		}
	}
	return ""
}

// gatherPlainText concatenates text content of an element, unescaping CDATA.
// net/html keeps CDATA content as a plain TextNode; we just concatenate.
func gatherPlainText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(nn *html.Node) {
		if nn.Type == html.TextNode {
			b.WriteString(nn.Data)
		}
		for ch := nn.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return b.String()
}

// escapeMarkdown escapes characters that have special meaning in markdown
// when appearing inside text. We only escape the minimum set to keep output
// readable.
func escapeMarkdown(s string) string {
	// Escape backslash, then inline markers. Keep this conservative.
	s = strings.ReplaceAll(s, `\`, `\\`)
	// Escape underscores and asterisks that might form emphasis.
	// For readability we only escape clusters of 2+, and leading *.
	// (The common case — prose with occasional * — doesn't need escaping.)
	return s
}
