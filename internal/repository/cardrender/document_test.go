package cardrender

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestDocumentDarkTheme(t *testing.T) {
	const trustedTheme = `:root { color-scheme: dark !important; background: transparent !important; } body.card { background: transparent !important; color: oklch(0.96 0.005 140) !important; }`
	tests := []struct {
		name     string
		raw      string
		preserve []string
		oversize bool
	}{
		{
			name:     "plain HTML",
			raw:      `<p>safe content</p>`,
			preserve: []string{`<p>safe content</p>`},
		},
		{
			name:     "standard light card CSS",
			raw:      `<style>.card { color: black; background-color: white; }</style><div class="card">safe content</div>`,
			preserve: []string{`.card { color: black; background-color: white; }`, `<div class="card">safe content</div>`},
		},
		{
			name:     "important light card CSS",
			raw:      `<style>.card { color: black !important; background: white !important; }</style><p>safe content</p>`,
			preserve: []string{`.card { color: black !important; background: white !important; }`, `<p>safe content</p>`},
		},
		{
			name:     "legacy full document",
			raw:      `<!doctype html><html><head><style>body.card { background: white !important; color: black !important; }</style></head><body class="card"><p>saved content</p></body></html>`,
			preserve: []string{`body.card { background: white !important; color: black !important; }`, `<p>saved content</p>`},
		},
		{
			name:     "oversize fallback",
			raw:      strings.Repeat("x", maxDocumentBytes+1),
			oversize: true,
		},
		{
			name: "nested formatting media and night CSS",
			raw:  `<style>@font-face {font-family:test;src:url('_font.woff2')} .nightMode .card { color: cyan; } .night_mode .card { background: navy; }</style><div class="card" style="font-family:test;text-align:center"><span style="color: #ff8800">colored content</span><code>x</code><img src="photo.png" alt="example"><audio controls src="audio.ogg"></audio></div>`,
			preserve: []string{
				`font-family:test`, `/api/media/_font.woff2`,
				`.nightMode .card { color: cyan; }`, `.night_mode .card { background: navy; }`,
				`text-align:center`, `<span style="color: #ff8800">colored content</span>`, `<code>x</code>`,
				`src="/api/media/photo.png"`, `src="/api/media/audio.ogg"`, `<audio`, `controls`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := tt.raw
			passes := 2
			if tt.oversize {
				passes = 1
			}
			for pass := 1; pass <= passes; pass++ {
				got := Document(raw)
				t.Run(fmt.Sprintf("pass_%d", pass), func(t *testing.T) {
					doc, err := html.Parse(strings.NewReader(got))
					if err != nil {
						t.Fatalf("parse rendered document: %v", err)
					}
					root := documentTestElement(t, doc, "html")
					body := documentTestElement(t, doc, "body")
					assertDocumentClassTokens(t, root, "nightMode", "night_mode")
					assertDocumentClassTokens(t, body, "card", "nightMode", "night_mode")
					style := body.LastChild
					if style == nil || style.Type != html.ElementNode || style.Data != "style" {
						t.Error("final body child must be the trusted theme style")
					} else if style.FirstChild == nil || style.FirstChild.Type != html.TextNode || style.FirstChild.Data != trustedTheme || style.FirstChild.NextSibling != nil {
						t.Error("final body style must contain the exact trusted theme declarations")
					}
					for _, want := range tt.preserve {
						if !strings.Contains(got, want) {
							t.Errorf("safe content missing: %q", want)
						}
					}
				})
				raw = got
			}
		})
	}
}

func documentTestElement(t *testing.T, root *html.Node, name string) *html.Node {
	t.Helper()
	var find func(*html.Node) *html.Node
	find = func(node *html.Node) *html.Node {
		if node.Type == html.ElementNode && node.Data == name {
			return node
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if found := find(child); found != nil {
				return found
			}
		}
		return nil
	}
	if found := find(root); found != nil {
		return found
	}
	t.Fatalf("rendered document missing %s element", name)
	return nil
}

func assertDocumentClassTokens(t *testing.T, node *html.Node, want ...string) {
	t.Helper()
	classes := make(map[string]bool)
	for _, attr := range node.Attr {
		if attr.Key == "class" {
			for _, token := range strings.Fields(attr.Val) {
				classes[token] = true
			}
		}
	}
	for _, token := range want {
		if !classes[token] {
			t.Errorf("%s missing class token %q", node.Data, token)
		}
	}
}

func TestDocumentPreservesFormattingCSSAndLocalMedia(t *testing.T) {
	raw := `<style>.card { color: red; font-family: test } @font-face {font-family:test;src:url('_font.woff2')} p {background-image:url("photo one.png")}</style><div class="card"><b>bold</b><code>x</code><img src="photo%20one.png" alt="example">[sound:voice.mp3]<audio controls src="audio.ogg"></audio></div>`
	got := Document(raw)
	for _, want := range []string{"<html", "<body", "<b>bold</b>", "<code>x</code>", "color: red", "/api/media/photo%20one.png", "/api/media/voice.mp3", "/api/media/audio.ogg", "/api/media/_font.woff2", "<audio", "controls"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "[sound:") {
		t.Fatal("sound marker not converted")
	}
}

func TestDocumentStripsActiveMarkupAndExternalRequests(t *testing.T) {
	raw := `<base href="https://evil.invalid/"><meta http-equiv="refresh" content="0;url=https://evil.invalid"><link rel="stylesheet" href="https://evil.invalid/a"><script>alert('x')</script><div onclick="alert(1)" style="background:url(https://evil.invalid/a)">safe</div><iframe srcdoc="<script>bad</script>"></iframe><object data="/api/session"></object><form action="/api/reset"><input name="x"></form><svg onload="bad"><image href="https://evil.invalid/a"/></svg><a href="https://evil.invalid" target="_top">link</a><img src="//evil.invalid/x" srcset="https://evil.invalid/x 2x" onerror="bad"><img src="../vault.json"><audio src="data:secret"></audio><style>@import 'https://evil.invalid/css'; div{background:image-set('https://evil.invalid/x' 2x)}</style><p style="background:u\72l('https://evil.invalid');behavior:url(x)">text</p>`
	got := Document(raw)
	for _, bad := range []string{"evil.invalid", "<script", "<iframe", "<object", "<form", "<input", "<svg", "onclick", "onerror", "onload", "srcset", "../vault", "data:secret", "<base", `http-equiv="refresh"`, "target=", "behavior", "@import"} {
		if strings.Contains(got, bad) {
			t.Errorf("unsafe %q retained: %s", bad, got)
		}
	}
	if !strings.Contains(got, "safe") || !strings.Contains(got, "link") {
		t.Fatal("safe text removed")
	}
}

func TestDocumentMalformedHTMLAndIdempotentMedia(t *testing.T) {
	for _, raw := range []string{`<IMG SRC="javascript:alert(1)"><p><b>safe`, `<math><mtext><table><mglyph><style><!--</style><img title="--><img src=x onerror=alert(1)>">`, `<img src="/api/media/picture.png"><audio src="/api/media/a.mp3"></audio>`} {
		got := Document(raw)
		if strings.Contains(got, "onerror") || strings.Contains(got, "javascript:") || strings.Contains(got, "<math") {
			t.Fatalf("unsafe malformed HTML: %s", got)
		}
		if strings.Contains(raw, "/api/media/picture.png") && !strings.Contains(got, "/api/media/picture.png") {
			t.Fatal("rewritten local media stripped")
		}
	}
}

func TestMediaName(t *testing.T) {
	for _, name := range []string{"x.png", "voice one.mp3", "_font.woff2", "картинка.png"} {
		if !MediaName(name) {
			t.Errorf("valid name rejected: %q", name)
		}
	}
	for _, name := range []string{"", ".", "..", "../x", "/x", `a\b`, "x\x00y", "x\ny", "C:x", strings.Repeat("x", 256)} {
		if MediaName(name) {
			t.Errorf("unsafe name accepted: %q", name)
		}
	}
}

func TestDocumentAddsFixedRestrictiveCSP(t *testing.T) {
	got := Document(`<meta http-equiv="Content-Security-Policy" content="default-src *"><b>safe</b>`)
	for _, want := range []string{`http-equiv="Content-Security-Policy"`, "default-src 'none'", "script-src 'none'", "connect-src 'none'", "style-src 'unsafe-inline'", "img-src 'self'", "media-src 'self'", "font-src 'self'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing document CSP %q", want)
		}
	}
	if strings.Contains(got, "default-src *") || strings.Count(got, `http-equiv=`) != 1 {
		t.Fatal("untrusted CSP retained")
	}
}

func TestDocumentCSSUnicodeAndCommentsKeepLocalURLs(t *testing.T) {
	got := Document(`<style>.card {font-family:İ;/* harmless */background:url('photo.png');} p {background:url('second.png')}</style><p>safe</p>`)
	for _, want := range []string{"font-family:İ", "/api/media/photo.png", "/api/media/second.png"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing Unicode CSS or media: %q", want)
		}
	}
}
