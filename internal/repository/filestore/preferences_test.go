package filestore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPreferencesArePrivateDurableAndAccountOwned(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	defaults := []string{"Tech"}
	decks, err := LoadPreferences(a, defaults)
	if err != nil {
		t.Fatal(err)
	}
	decks[0] = "changed"
	if defaults[0] != "Tech" {
		t.Fatal("defaults aliased")
	}
	if err := SavePreferences(a, []string{"Career", "Career", "English"}); err != nil {
		t.Fatal(err)
	}
	decks, err = LoadPreferences(a, nil)
	if err != nil || !reflect.DeepEqual(decks, []string{"Career", "English"}) {
		t.Fatalf("saved preferences %v %v", decks, err)
	}
	other, err := LoadPreferences(b, defaults)
	if err != nil || !reflect.DeepEqual(other, defaults) {
		t.Fatal("preferences crossed accounts")
	}
	info, err := os.Stat(filepath.Join(a, "preferences.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v %v", info, err)
	}
	if err := SavePreferences(a, []string{}); err != nil {
		t.Fatal(err)
	}
	decks, err = LoadPreferences(a, defaults)
	if err != nil || decks == nil || len(decks) != 0 {
		t.Fatalf("explicit empty lost: %v %v", decks, err)
	}
	for _, invalid := range [][]string{{""}, {"line\nbreak"}, {strings.Repeat("x", 257)}, make([]string, 65)} {
		if err := SavePreferences(a, invalid); err == nil {
			t.Fatalf("accepted %v", invalid)
		}
	}
	if err := os.WriteFile(filepath.Join(a, "preferences.json"), []byte(`{"knowledge_decks":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPreferences(a, defaults); err == nil {
		t.Fatal("corrupt preferences accepted")
	}
}
