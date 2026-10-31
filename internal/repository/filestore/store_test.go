package filestore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		pass *domain.Pass
	}{
		{"no pass", nil},
		{"graded pass", &domain.Pass{
			Session:   "a1",
			Deck:      "Tech::Go",
			Kind:      domain.KindKnowledge,
			Limit:     25,
			CreatedAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
			Items:     []domain.Item{{N: 1, CardID: 10, Back: "key", Intervals: map[domain.Grade]string{domain.Good: "1d"}}},
			Answers:   []domain.Answer{{N: 1, Text: "a"}},
			Results:   []domain.Result{},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := New(filepath.Join(t.TempDir(), "data"))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Save(tt.pass); err != nil {
				t.Fatal(err)
			}
			got, err := s.Load()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.pass) {
				t.Errorf("Load() = %+v, want %+v", got, tt.pass)
			}
		})
	}
}

func TestLoadMissing(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p, err := s.Load(); p != nil || err != nil {
		t.Errorf("Load() = %v, %v; want nil, nil", p, err)
	}
}

// A pass saved by an older version still carries the grader's "ref" field: it must load.
func TestLoadIgnoresDroppedResultFields(t *testing.T) {
	dir := t.TempDir()
	raw := `{"version":1,"pass":{"session":"a1","items":[{"n":1,"back":"запрещать"}],"answers":[{"n":1,"answer":"forbid"}],` +
		`"results":[{"n":1,"grade":"Good","mistake":"","ref":"forbid; prohibit","note":"n","topic":"t"}],"confirmed":null,"status":null}}`
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := New(dir)
	p, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []domain.Result{{N: 1, Grade: domain.Good, Note: "n", Topic: "t"}}
	if !reflect.DeepEqual(p.Results, want) || p.Items[0].Back != "запрещать" {
		t.Errorf("Load() = %+v", p)
	}
}

func TestLoadRejectsOtherVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"version":99,"pass":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := New(dir)
	if _, err := s.Load(); err == nil {
		t.Error("Load() accepted an unknown version")
	}
}

func TestOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&domain.Pass{Session: "a1"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: dirMode, filepath.Join(dir, fileName): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(leftovers) > 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}
