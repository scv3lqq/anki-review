// Package apkg validates bounded, legacy-compatible Anki package archives before native import.
package apkg

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scv3lqq/anki-review/internal/repository/cardrender"
)

const (
	// MaxBytes keeps the base64 AnkiConnect request below its 16 MiB request cap.
	MaxBytes          = 8 << 20
	maxEntries        = 4096
	maxExpanded       = 256 << 20
	legacyInstruction = "Export with Support older Anki versions enabled."
)

// Validate fully reads every entry to verify its CRC and actual expansion before any Anki writes.
func Validate(raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxBytes {
		return errors.New("invalid Anki package must be at most 8 MiB")
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return fmt.Errorf("read Anki package: %w", err)
	}
	if len(z.File) > maxEntries {
		return errors.New("invalid Anki package has too many entries")
	}
	entries := make(map[string]*zip.File, len(z.File))
	var total int64
	var collection bool
	for _, f := range z.File {
		if !safePath(f.Name) || !f.Mode().IsRegular() || f.Flags&0x41 != 0 {
			return errors.New("invalid Anki package has an unsafe entry")
		}
		if entries[f.Name] != nil {
			return errors.New("invalid Anki package has duplicate entries")
		}
		entries[f.Name] = f
		if f.Name == "collection.anki21b" {
			return fmt.Errorf("unsupported package: %s", legacyInstruction)
		}
		if f.UncompressedSize64 > uint64(maxExpanded-total) {
			return errors.New("invalid Anki package expands beyond 256 MiB")
		}
		prefix, size, err := readEntry(f, int64(maxExpanded)-total)
		if err != nil {
			return err
		}
		total += size
		if nestedCompression(prefix) {
			return fmt.Errorf("unsupported package: %s", legacyInstruction)
		}
		if f.Name == "collection.anki2" || f.Name == "collection.anki21" {
			if !bytes.HasPrefix(prefix, []byte("SQLite format 3\x00")) {
				return fmt.Errorf("collection is not a legacy SQLite database: %s", legacyInstruction)
			}
			collection = true
		}
	}
	if !collection || entries["media"] == nil {
		return fmt.Errorf("package needs a legacy collection and media mapping: %s", legacyInstruction)
	}
	return validateMapping(entries["media"], entries)
}

func safePath(name string) bool {
	return name != "" && utf8.ValidString(name) && !strings.ContainsAny(name, `\:`) && !strings.HasPrefix(name, "/") && path.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, "../") && strings.IndexFunc(name, unicode.IsControl) < 0
}

func readEntry(f *zip.File, remaining int64) ([]byte, int64, error) {
	r, err := f.Open()
	if err != nil {
		return nil, 0, fmt.Errorf("open package entry: %w; %s", err, legacyInstruction)
	}
	defer r.Close()
	var prefix [16]byte
	n, err := io.ReadFull(r, prefix[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, 0, fmt.Errorf("read package entry: %w", err)
	}
	if int64(n) > remaining {
		return nil, 0, errors.New("invalid Anki package expands beyond 256 MiB")
	}
	count, err := io.Copy(io.Discard, io.LimitReader(r, remaining-int64(n)+1))
	if err != nil {
		return nil, 0, fmt.Errorf("verify package entry: %w", err)
	}
	if count+int64(n) > remaining {
		return nil, 0, errors.New("invalid Anki package expands beyond 256 MiB")
	}
	return prefix[:n], count + int64(n), nil
}

func nestedCompression(prefix []byte) bool {
	return bytes.HasPrefix(prefix, []byte("\x28\xb5\x2f\xfd")) || bytes.HasPrefix(prefix, []byte("PK\x03\x04")) || bytes.HasPrefix(prefix, []byte("PK\x05\x06")) || bytes.HasPrefix(prefix, []byte("\x1f\x8b")) || (len(prefix) >= 4 && prefix[0] >= 0x50 && prefix[0] <= 0x5f && bytes.Equal(prefix[1:4], []byte("\x2a\x4d\x18")))
}

func validateMapping(f *zip.File, entries map[string]*zip.File) error {
	r, err := f.Open()
	if err != nil {
		return fmt.Errorf("open media mapping: %w", err)
	}
	defer r.Close()
	d := json.NewDecoder(io.LimitReader(r, maxExpanded+1))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("invalid Anki media mapping must be a JSON object")
	}
	keys, names := make(map[string]bool), make(map[string]bool)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return fmt.Errorf("read media mapping: %w", err)
		}
		key, ok := token.(string)
		if !ok || !numeric(key) || keys[key] || entries[key] == nil {
			return errors.New("invalid Anki media mapping has an invalid archive entry")
		}
		var name string
		if err := d.Decode(&name); err != nil {
			return fmt.Errorf("read media filename: %w", err)
		}
		if !cardrender.MediaName(name) || names[name] {
			return errors.New("invalid Anki media mapping has an unsafe or duplicate filename")
		}
		keys[key], names[name] = true, true
		if len(keys) > maxEntries {
			return errors.New("invalid Anki media mapping has too many entries")
		}
	}
	if _, err := d.Token(); err != nil {
		return fmt.Errorf("finish media mapping: %w", err)
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return errors.New("invalid Anki media mapping has trailing data")
	}
	return nil
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
