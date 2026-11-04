package apkg

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

type entry struct {
	name   string
	data   []byte
	mode   fs.FileMode
	flags  uint16
	method uint16
}

func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: e.method, Flags: e.flags}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func validEntries() []entry {
	return []entry{{name: "collection.anki2", data: []byte("SQLite format 3\x00fixture")}, {name: "media", data: []byte(`{"0":"photo.png","1":"voice.mp3"}`)}, {name: "0", data: []byte("image")}, {name: "1", data: []byte("audio")}}
}

func TestValidateLegacyPackage(t *testing.T) {
	for _, name := range []string{"collection.anki2", "collection.anki21"} {
		entries := validEntries()
		entries[0].name = name
		if err := Validate(archive(t, entries...)); err != nil {
			t.Fatalf("valid %s: %v", name, err)
		}
	}
}

func TestValidateRejectsUnsafeArchives(t *testing.T) {
	for _, name := range []string{"../secret", "/root", `a\b`, "a/../b", "./x", "x\x00y", "x\ny", "C:/x"} {
		t.Run(name, func(t *testing.T) {
			entries := append(validEntries(), entry{name: name, data: []byte("x")})
			if err := Validate(archive(t, entries...)); err == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		extra entry
	}{
		{"duplicate", entry{name: "media", data: []byte(`{}`)}},
		{"symlink", entry{name: "link", data: []byte("target"), mode: fs.ModeSymlink | 0o777}},
		{"device", entry{name: "device", mode: fs.ModeDevice | 0o600}},
		{"encrypted", entry{name: "encrypted", data: []byte("x"), flags: 1}},
		{"nested zip", entry{name: "nested", data: []byte("PK\x03\x04nested")}},
		{"gzip", entry{name: "nested", data: []byte("\x1f\x8bcompressed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(archive(t, append(validEntries(), tc.extra)...)); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	entries := validEntries()
	entries[0].data = []byte("not SQLite")
	if err := Validate(archive(t, entries...)); err == nil {
		t.Fatal("invalid collection accepted")
	}
	if err := Validate(bytes.Repeat([]byte("x"), 8<<20+1)); err == nil {
		t.Fatal("oversized package accepted")
	}
	if err := Validate([]byte("not zip")); err == nil {
		t.Fatal("invalid ZIP accepted")
	}
}

func TestValidateMediaMapping(t *testing.T) {
	for _, mapping := range []string{`{"0":"../x"}`, `{"0":"a/b"}`, `{"0":"a\\b"}`, `{"0":"/x"}`, `{"0":"x\ny"}`, `{"0":"."}`, `{"0":"C:x"}`, `{"999":"x"}`, `{"collection.anki2":"x"}`, `{"0":"x","1":"x"}`, `{"0":"x","0":"y"}`, `[]`, `null`, `{"0":2}`} {
		t.Run(mapping, func(t *testing.T) {
			entries := validEntries()
			entries[1].data = []byte(mapping)
			if err := Validate(archive(t, entries...)); err == nil {
				t.Fatal("unsafe media mapping accepted")
			}
		})
	}
}

func TestValidateModernCompressionNeedsLegacyExport(t *testing.T) {
	for _, change := range []func([]entry){func(e []entry) { e[0].name = "collection.anki21b" }, func(e []entry) { e[2].data = []byte("\x28\xb5\x2f\xfdzstd") }, func(e []entry) { e[1].data = []byte("\x28\xb5\x2f\xfdzstd") }} {
		e := validEntries()
		change(e)
		if err := Validate(archive(t, e...)); err == nil || !strings.Contains(err.Error(), "Export with Support older Anki versions enabled.") {
			t.Fatalf("modern format error = %v", err)
		}
	}
}

func TestValidateCRCAndDishonestSize(t *testing.T) {
	raw := archive(t, validEntries()...)
	needle := []byte("SQLite format 3\x00fixture")
	pos := bytes.Index(raw, needle)
	if pos < 0 {
		t.Fatal("fixture missing")
	}
	raw[pos+16] ^= 1
	if err := Validate(raw); err == nil {
		t.Fatal("bad CRC accepted")
	}
	raw = archive(t, validEntries()...)
	central := bytes.Index(raw, []byte("PK\x01\x02"))
	if central < 0 {
		t.Fatal("missing central directory")
	}
	binary.LittleEndian.PutUint32(raw[central+24:central+28], 1)
	if err := Validate(raw); err == nil {
		t.Fatal("forged size accepted")
	}
}

func TestValidateEntryAndExpandedLimits(t *testing.T) {
	entries := validEntries()
	for n := 0; n < 4096; n++ {
		entries = append(entries, entry{name: fmt.Sprintf("extra%d", n)})
	}
	if err := Validate(archive(t, entries...)); err == nil {
		t.Fatal("entry limit ignored")
	}
	// Compress highly repetitive data so the input stays below 8 MiB while expansion exceeds 256 MiB.
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, e := range validEntries() {
		f, err := w.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	f, err := w.Create("bomb")
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for range 257 {
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Validate(out.Bytes()); err == nil {
		t.Fatal("expansion limit ignored")
	}
}
