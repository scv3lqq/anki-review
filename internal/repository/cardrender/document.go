// Package cardrender turns untrusted card HTML into a passive document with authenticated media.
// Callers must also send a restrictive CSP and use a script-disabled sandboxed iframe.
package cardrender

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const maxDocumentBytes = 2 << 20

// MediaName accepts a single portable filename, never a path or a URL.
func MediaName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 255 && utf8.ValidString(name) && !strings.ContainsAny(name, `/\:`) && strings.IndexFunc(name, unicode.IsControl) < 0
}

// Document parses and reconstructs a standalone document; raw card HTML never enters the parent DOM.
func Document(raw string) string {
	const start = `<!doctype html><html class="nightMode night_mode"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'none'; connect-src 'none'; style-src 'unsafe-inline'; img-src 'self'; media-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'"></head><body class="card nightMode night_mode">`
	// The isolated frame needs its own theme, appended to override native white card canvases.
	const end = `<style>:root { color-scheme: dark !important; background: transparent !important; } body.card { background: transparent !important; color: oklch(0.96 0.005 140) !important; }</style></body></html>`
	if len(raw) > maxDocumentBytes {
		return start + end
	}
	n, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return start + end
	}
	body := &html.Node{Type: html.ElementNode, Data: "div"}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		sanitize(body, child, 0)
	}
	var out strings.Builder
	out.WriteString(start)
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&out, child); err != nil {
			return start + end
		}
	}
	out.WriteString(end)
	return out.String()
}

func sanitize(parent, n *html.Node, depth int) {
	if depth > 256 {
		return
	}
	if n.Type == html.TextNode {
		soundText(parent, n.Data)
		return
	}
	if n.Type != html.ElementNode && n.Type != html.DocumentNode {
		return
	}
	if n.Namespace != "" {
		return
	}
	switch n.Data {
	case "script", "iframe", "frame", "frameset", "object", "embed", "applet", "form", "input", "button", "textarea", "select", "option", "link", "meta", "base", "svg", "math", "noscript", "template":
		return
	}
	if n.Data == "style" {
		var css strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.TextNode {
				css.WriteString(child.Data)
			}
		}
		if safe := sanitizeCSS(css.String()); safe != "" {
			parent.AppendChild(&html.Node{Type: html.ElementNode, Data: "style"})
			style := parent.LastChild
			style.AppendChild(&html.Node{Type: html.TextNode, Data: safe})
		}
		return
	}
	if allowedTag(n.Data) {
		clean := &html.Node{Type: html.ElementNode, Data: n.Data, DataAtom: n.DataAtom, Attr: sanitizeAttrs(n)}
		parent.AppendChild(clean)
		parent = clean
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		sanitize(parent, child, depth+1)
	}
}

func allowedTag(name string) bool {
	switch name {
	case "a", "div", "p", "span", "br", "hr", "b", "strong", "i", "em", "u", "s", "strike", "small", "big", "sub", "sup", "code", "pre", "kbd", "samp", "var", "mark", "q", "blockquote", "abbr", "time", "ul", "ol", "li", "dl", "dt", "dd", "table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption", "colgroup", "col", "h1", "h2", "h3", "h4", "h5", "h6", "ruby", "rt", "rp", "details", "summary", "figure", "figcaption", "img", "audio", "video", "source", "track", "font", "center":
		return true
	}
	return false
}

func sanitizeAttrs(n *html.Node) []html.Attribute {
	var attrs []html.Attribute
	for _, a := range n.Attr {
		if a.Namespace != "" {
			continue
		}
		switch a.Key {
		case "class", "id", "title", "alt", "lang", "dir", "width", "height", "colspan", "rowspan", "align", "valign", "color", "face", "size", "open", "controls", "loop", "muted":
			attrs = append(attrs, a)
		case "style":
			if css := sanitizeCSS(a.Val); css != "" {
				a.Val = css
				attrs = append(attrs, a)
			}
		case "src", "poster":
			if (a.Key == "poster" && n.Data == "video") || (a.Key == "src" && (n.Data == "img" || n.Data == "audio" || n.Data == "video" || n.Data == "source" || n.Data == "track")) {
				if source := mediaURL(a.Val); source != "" {
					a.Val = source
					attrs = append(attrs, a)
				}
			}
		case "type":
			if n.Data == "source" {
				attrs = append(attrs, a)
			}
		}
	}
	if n.Data == "audio" || n.Data == "video" {
		attrs = append(attrs, html.Attribute{Key: "controls", Val: ""}, html.Attribute{Key: "preload", Val: "none"})
	}
	return attrs
}

func mediaURL(raw string) string {
	raw = strings.TrimPrefix(raw, "/api/media/")
	name, err := url.PathUnescape(raw)
	if err != nil || !MediaName(name) {
		return ""
	}
	return "/api/media/" + url.PathEscape(name)
}

func soundText(parent *html.Node, text string) {
	for {
		before, after, found := strings.Cut(text, "[sound:")
		if !found {
			if text != "" {
				parent.AppendChild(&html.Node{Type: html.TextNode, Data: text})
			}
			return
		}
		if before != "" {
			parent.AppendChild(&html.Node{Type: html.TextNode, Data: before})
		}
		name, rest, closed := strings.Cut(after, "]")
		if !closed {
			parent.AppendChild(&html.Node{Type: html.TextNode, Data: "[sound:" + after})
			return
		}
		if source := mediaURL(name); source != "" {
			parent.AppendChild(&html.Node{Type: html.ElementNode, Data: "audio", Attr: []html.Attribute{{Key: "controls", Val: ""}, {Key: "preload", Val: "none"}, {Key: "src", Val: source}}})
		}
		text = rest
	}
}

// CSS URL syntax is deliberately conservative: escaped tokens and active/import constructs are
// discarded, and every remaining URL is rewritten to the authenticated basename endpoint.
func sanitizeCSS(raw string) string {
	if strings.ContainsAny(raw, "\\<>") {
		return ""
	}
	var uncommented strings.Builder
	for {
		before, after, found := strings.Cut(raw, "/*")
		if !found {
			uncommented.WriteString(raw)
			break
		}
		uncommented.WriteString(before)
		_, rest, closed := strings.Cut(after, "*/")
		if !closed {
			return ""
		}
		raw = rest
	}
	raw = uncommented.String()
	lower := asciiLower(raw)
	for _, bad := range []string{"@import", "@namespace", "expression(", "behavior", "-moz-binding", "image-set(", "image(", "src(", "http:", "https:", "//", "data:", "javascript:"} {
		if strings.Contains(lower, bad) {
			return ""
		}
	}
	var out strings.Builder
	for {
		at := strings.Index(lower, "url(")
		if at < 0 {
			out.WriteString(raw)
			return out.String()
		}
		out.WriteString(raw[:at])
		value, rest, closed := strings.Cut(raw[at+4:], ")")
		consumed := len(raw) - len(rest)
		if !closed {
			return ""
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		source := mediaURL(value)
		if source == "" {
			return ""
		}
		out.WriteString(`url("` + source + `")`)
		raw = rest
		lower = lower[consumed:]
	}
}

// CSS keywords are ASCII. Keeping byte offsets stable also preserves Unicode font/media names.
func asciiLower(raw string) string {
	lower := []byte(raw)
	for i, b := range lower {
		if b >= 'A' && b <= 'Z' {
			lower[i] = b + ('a' - 'A')
		}
	}
	return string(lower)
}
