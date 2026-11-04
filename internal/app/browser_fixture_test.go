package app

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/repository/apkg"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeanki"
)

func browserPackage(t *testing.T) []byte {
	t.Helper()
	photo := image.NewRGBA(image.Rect(0, 0, 4, 4))
	photo.Set(1, 1, color.RGBA{R: 220, A: 255})
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, photo); err != nil {
		t.Fatal(err)
	}
	wav := make([]byte, 124)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 8000)
	binary.LittleEndian.PutUint32(wav[28:], 8000)
	binary.LittleEndian.PutUint16(wav[32:], 1)
	binary.LittleEndian.PutUint16(wav[34:], 8)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 80)
	for i := 44; i < len(wav); i++ {
		wav[i] = 128
	}
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	for name, raw := range map[string][]byte{"collection.anki2": []byte("SQLite format 3\x00browser fixture"), "media": []byte(`{"0":"photo.png","1":"voice.wav"}`), "0": pixels.Bytes(), "1": wav} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

// TestMultiAccountBrowserFixture is an ordinary HTTP test unless its private handoff is requested.
func TestMultiAccountBrowserFixture(t *testing.T) {
	ownerAnki := fakeanki.New(t)
	ownerAnki.RequireKey(testKey)
	guestAnki := fakeanki.New(t)
	guestAnki.RequireKey(strings.Repeat("1", 64))
	socket, _ := fakeBroker(t, guestAnki)
	bin, _ := isolatedClaudeFixture(t)
	cfg := testConfig(ownerAnki, bin, t.TempDir())
	cfg.AccountSocket = socket
	cfg.MaxAccounts = 4
	root, _ := serve(t, cfg)
	setUp(t, root)
	anonymous := separateClient(t, root.base)
	if status, body := anonymous.call(http.MethodGet, "/api/session", nil); status != 200 || strings.Contains(string(body), "account_id") {
		t.Fatal("browser bootstrap leaked account state")
	}
	status, body := root.call(http.MethodPost, "/api/invitations", map[string]any{})
	if status != 200 {
		t.Fatal("browser fixture invitation failed")
	}
	var invitation struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &invitation); err != nil {
		t.Fatal(err)
	}
	packageBytes := browserPackage(t)
	if err := apkg.Validate(packageBytes); err != nil {
		t.Fatal(err)
	}
	directory := os.Getenv("ANKI_REVIEW_BROWSER_DIR")
	if directory == "" {
		anonymous.post("/api/register", map[string]any{"invitation": invitation.Token, "user": "browser-guest", "password": testPassword, "claude_token": "sk-ant-oat01-browser-fixture"})
		if status := uploadPackage(t, anonymous, packageBytes); status != 200 {
			t.Fatal("browser fixture package import failed")
		}
		anonymous.post("/api/start", map[string]any{"deck": "Imported", "count": 1})
		var cards struct {
			Cards []struct {
				HTML string `json:"question_html"`
			} `json:"cards"`
		}
		anonymous.get("/api/cards", &cards)
		if len(cards.Cards) != 1 || !strings.Contains(cards.Cards[0].HTML, "photo.png") || !strings.Contains(cards.Cards[0].HTML, "voice.wav") {
			t.Fatal("browser fixture lacks imported image/audio")
		}
		return
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("browser handoff must be a private directory")
	}
	packagePath := filepath.Join(directory, "fixture.apkg")
	if err := os.WriteFile(packagePath, packageBytes, 0600); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]string{"url": root.base, "invitation_url": root.base + "/#invite=" + invitation.Token, "package_path": packagePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "fixture.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("fake browser fixture: %s", root.base)
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("browser fixture timed out")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(directory, "stop")); err == nil {
				return
			}
		}
	}
}
