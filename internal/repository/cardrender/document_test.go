package cardrender

import (
	"strings"
	"testing"
)

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
