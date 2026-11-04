package ankiconnect

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/scv3lqq/anki-review/internal/domain"
)

type repeatedByte byte

func (b repeatedByte) Read(raw []byte) (int, error) {
	for i := range raw {
		raw[i] = byte(b)
	}
	return len(raw), nil
}

func TestResponseLimitAndTrailingJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func(http.ResponseWriter)
	}{
		{"oversized", func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, `{"result":"`)
			_, _ = io.CopyN(w, repeatedByte('x'), 1024)
			_, _ = io.WriteString(w, `","error":null}`)
		}},
		{"trailing invalid", func(w http.ResponseWriter) { _, _ = io.WriteString(w, `{"result":6,"error":null} garbage`) }},
		{"second object", func(w http.ResponseWriter) { _, _ = io.WriteString(w, `{"result":6,"error":null}{"result":7}`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { tc.body(w) }))
			defer srv.Close()
			c := New(srv.URL, nil)
			c.responseLimit = 128
			if err := c.Version(context.Background()); !errors.Is(err, domain.ErrAnki) {
				t.Fatalf("unsafe response accepted: %v", err)
			}
		})
	}
	if maxResponseBytes != 24<<20 {
		t.Fatal("default response bound does not preserve 16 MiB media")
	}
}

func TestRenderedCardAndCSSSizeLimits(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		question, answer, css int
		valid                 bool
	}{
		{"exact combined limit", 128 << 10, 128 << 10, 64 << 10, true},
		{"question over", 256<<10 + 1, 0, 0, false},
		{"combined over", 128 << 10, 128<<10 + 1, 0, false},
		{"css over", 1, 1, 64<<10 + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newFake(t, func(string, json.RawMessage) (any, string) {
				return []map[string]any{{"cardId": 1, "question": strings.Repeat("q", tc.question), "answer": strings.Repeat("a", tc.answer), "css": strings.Repeat(" ", tc.css)}}, ""
			})
			infos, err := c.cardsInfo(context.Background(), []int64{1}, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("size limit error=%v, cards=%d", err, len(infos))
			}
			if err != nil && (!errors.Is(err, domain.ErrAnki) || !strings.Contains(err.Error(), "safe size limit")) {
				t.Fatalf("unclear card size error: %v", err)
			}
		})
	}
}
