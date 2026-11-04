package ankiconnect

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func packageFixture(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for name, data := range map[string]string{"collection.anki2": "SQLite format 3\x00fixture", "media": "{}"} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestImportPackageNativeStagingAndCleanup(t *testing.T) {
	for _, failed := range []string{"", "storeMediaFile", "getMediaDirPath", "importPackage"} {
		t.Run(failed, func(t *testing.T) {
			raw := packageFixture(t)
			var staged string
			var actions []string
			c, _ := newFake(t, func(action string, params json.RawMessage) (any, string) {
				actions = append(actions, action)
				var p map[string]string
				if params != nil {
					if err := json.Unmarshal(params, &p); err != nil {
						t.Fatal(err)
					}
				}
				if action == "storeMediaFile" {
					staged = p["filename"]
					data, err := base64.StdEncoding.DecodeString(p["data"])
					if err != nil || !bytes.Equal(data, raw) {
						t.Error("staged package changed")
					}
					if !strings.HasSuffix(staged, ".apkg") || strings.ContainsAny(staged, `/\`) {
						t.Error("unsafe staging name")
					}
				}
				if action == "importPackage" && p["path"] != "/collection/media/"+staged {
					t.Errorf("native import path = %q", p["path"])
				}
				if action == "deleteMediaFile" && p["filename"] != staged {
					t.Error("cleanup deletes unrelated file")
				}
				if action == failed {
					return nil, "injected failure"
				}
				switch action {
				case "storeMediaFile":
					return staged, ""
				case "getMediaDirPath":
					return "/collection/media", ""
				case "importPackage":
					return true, ""
				default:
					return nil, ""
				}
			})
			err := c.ImportPackage(context.Background(), raw)
			if (err != nil) != (failed != "") {
				t.Fatalf("import error = %v", err)
			}
			if len(actions) < 2 || actions[len(actions)-1] != "deleteMediaFile" {
				t.Fatalf("staging not cleaned: %v", actions)
			}
		})
	}
}

func TestImportPackageWithLowercaseNormalizedStagingName(t *testing.T) {
	var staged string
	var actions []string
	c, _ := newFake(t, func(action string, params json.RawMessage) (any, string) {
		actions = append(actions, action)
		var p map[string]string
		if params != nil {
			if err := json.Unmarshal(params, &p); err != nil {
				t.Fatal(err)
			}
		}
		switch action {
		case "storeMediaFile":
			staged = p["filename"]
			return strings.ToLower(staged), ""
		case "getMediaDirPath":
			return "/collection/media", ""
		case "importPackage":
			if p["path"] != "/collection/media/"+staged {
				t.Errorf("native import path = %q, want requested basename %q", p["path"], staged)
			}
			return true, ""
		case "deleteMediaFile":
			if p["filename"] != staged {
				t.Errorf("cleanup filename = %q, want requested basename %q", p["filename"], staged)
			}
			return nil, ""
		default:
			t.Errorf("unexpected action %q", action)
			return nil, "unexpected action"
		}
	})
	if err := c.ImportPackage(context.Background(), packageFixture(t)); err != nil {
		t.Fatalf("import with normalized staging name: %v", err)
	}
	if got := strings.Join(actions, ","); got != "storeMediaFile,getMediaDirPath,importPackage,deleteMediaFile" {
		t.Fatalf("import actions = %q", got)
	}
}

func TestImportPackageRejectsUnexpectedStoredFilename(t *testing.T) {
	var staged string
	var actions []string
	c, _ := newFake(t, func(action string, params json.RawMessage) (any, string) {
		actions = append(actions, action)
		var p map[string]string
		if err := json.Unmarshal(params, &p); err != nil {
			t.Fatal(err)
		}
		switch action {
		case "storeMediaFile":
			staged = p["filename"]
			return "unexpected.apkg", ""
		case "deleteMediaFile":
			if p["filename"] != staged {
				t.Errorf("cleanup filename = %q, want requested basename %q", p["filename"], staged)
			}
			return nil, ""
		default:
			t.Errorf("unexpected action %q", action)
			return nil, "unexpected action"
		}
	})
	if err := c.ImportPackage(context.Background(), packageFixture(t)); err == nil || !strings.Contains(err.Error(), "unexpected media filename") {
		t.Fatalf("unexpected staging name error = %v", err)
	}
	if got := strings.Join(actions, ","); got != "storeMediaFile,deleteMediaFile" {
		t.Fatalf("rejected import actions = %q", got)
	}
}

func TestImportCleanupSurvivesCanceledRequestAndRejectsInvalidPackage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleaned := false
	c, f := newFake(t, func(action string, params json.RawMessage) (any, string) {
		switch action {
		case "storeMediaFile":
			var p map[string]string
			_ = json.Unmarshal(params, &p)
			return p["filename"], ""
		case "getMediaDirPath":
			return "/media", ""
		case "importPackage":
			cancel()
			return nil, "failed"
		case "deleteMediaFile":
			cleaned = true
			return nil, ""
		default:
			return nil, "unexpected"
		}
	})
	if err := c.ImportPackage(ctx, packageFixture(t)); err == nil {
		t.Fatal("canceled import succeeded")
	}
	if !cleaned {
		t.Fatal("canceled request skipped cleanup")
	}
	before := len(f.requests)
	if err := c.ImportPackage(context.Background(), []byte("invalid")); err == nil {
		t.Fatal("invalid package accepted")
	}
	if len(f.requests) != before {
		t.Fatal("invalid package reached Anki")
	}
}

func TestMediaBoundedAndBasenameOnly(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		valid        bool
	}{{"image.png", base64.StdEncoding.EncodeToString([]byte("image")), true}, {"../vault.json", "", false}, {"/etc/passwd", "", false}, {"bad\\name", "", false}, {"bad\nname", "", false}, {"missing.png", "false", false}, {"broken.png", "not base64!", false}, {"huge.png", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 16<<20+1)), false}} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newFake(t, func(action string, _ json.RawMessage) (any, string) {
				if action != "retrieveMediaFile" {
					t.Error("unexpected action")
				}
				return tc.result, ""
			})
			out, err := c.Media(context.Background(), tc.name)
			if (err == nil) != tc.valid {
				t.Fatalf("Media error=%v, bytes=%d", err, len(out))
			}
			if strings.ContainsAny(tc.name, "/\\\n") && len(f.requests) != 0 {
				t.Fatal("unsafe media name reached Anki")
			}
		})
	}
}

func TestImportFalseResultAndCleanupFailureAreErrors(t *testing.T) {
	for _, failCleanup := range []bool{false, true} {
		c, _ := newFake(t, func(action string, params json.RawMessage) (any, string) {
			switch action {
			case "storeMediaFile":
				var p map[string]string
				_ = json.Unmarshal(params, &p)
				return p["filename"], ""
			case "getMediaDirPath":
				return "/media", ""
			case "importPackage":
				return failCleanup, ""
			case "deleteMediaFile":
				if failCleanup {
					return nil, "failed cleanup"
				}
				return nil, ""
			default:
				return nil, "unexpected"
			}
		})
		if err := c.ImportPackage(context.Background(), packageFixture(t)); err == nil {
			t.Fatal("failed import or cleanup reported success")
		}
	}
}

func TestCardsInfoCSSAndRenderedSidesRetainMedia(t *testing.T) {
	const question = `<b>Question</b><img src="photo.png">[sound:voice.mp3]`
	const answer = `<hr id=answer><i>Answer</i>`
	const css = `.card { color: red }`
	c, _ := newFake(t, func(action string, _ json.RawMessage) (any, string) {
		switch action {
		case "findCards":
			return []int64{1}, ""
		case "getDeckStats":
			return map[string]any{}, ""
		case "cardsInfo":
			return []map[string]any{{"cardId": 1, "deckName": "English", "question": question, "answer": answer, "css": css}}, ""
		default:
			return nil, "unexpected action"
		}
	})
	items, err := c.DueCards(context.Background(), "English", 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("DueCards = %v, %v", items, err)
	}
	it := items[0]
	if !strings.Contains(it.QuestionHTML, css) || !strings.Contains(it.QuestionHTML, "photo.png") || !strings.Contains(it.QuestionHTML, "/api/media/voice.mp3") || !strings.Contains(it.BackHTML, "<i>Answer</i>") {
		t.Fatal("rendered sides lost template CSS, formatting or media")
	}
	if it.Question != "Question[image]" || it.Back != "Answer" {
		t.Fatalf("plain text changed: %q, %q", it.Question, it.Back)
	}
}

func TestRenderedQuestionDoesNotExposeHiddenAnswerAttributes(t *testing.T) {
	raw := renderedHTML(`.card { color: red }`, `<span class="cloze" data-cloze="secret answer" onclick="alert(1)">[...]</span>[sound:voice.mp3]<script>secret script</script>`)
	if strings.Contains(raw, "secret answer") || strings.Contains(raw, "onclick") || strings.Contains(raw, "secret script") || !strings.Contains(raw, "/api/media/voice.mp3") {
		t.Fatal("saved public question HTML contains hidden answers or active content")
	}
}
