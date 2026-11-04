package ankiconnect

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/repository/apkg"
	"github.com/scv3lqq/anki-review/internal/repository/cardrender"
)

const (
	maxMediaBytes  = 16 << 20
	cleanupTimeout = 5 * time.Second
)

// ImportPackage validates an archive, stages a randomly named media file, and delegates import to
// Anki's native importer so note models, media, scheduling and review history retain their meaning.
func (c *Client) ImportPackage(ctx context.Context, raw []byte) (err error) {
	if err := apkg.Validate(raw); err != nil {
		return fmt.Errorf("validate package: %w", err)
	}
	name := "_anki-review-import-" + strings.ToLower(rand.Text()) + ".apkg"
	// Even a canceled or failed upload may have created its file before the response was lost.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if cleanupErr := c.call(cleanup, "deleteMediaFile", map[string]string{"filename": name}, nil); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove staged package: %w", cleanupErr))
		}
	}()
	var stored string
	if err := c.call(ctx, "storeMediaFile", map[string]string{"filename": name, "data": base64.StdEncoding.EncodeToString(raw)}, &stored); err != nil {
		return fmt.Errorf("stage package: %w", err)
	}
	if stored != name {
		return fmt.Errorf("stage package: %w: unexpected media filename", domain.ErrAnki)
	}
	var dir string
	if err := c.call(ctx, "getMediaDirPath", nil, &dir); err != nil {
		return fmt.Errorf("locate staged package: %w", err)
	}
	if !path.IsAbs(dir) || strings.Contains(dir, `\`) || strings.IndexFunc(dir, unicode.IsControl) >= 0 {
		return fmt.Errorf("locate staged package: %w: invalid media directory", domain.ErrAnki)
	}
	var imported bool
	if err := c.call(ctx, "importPackage", map[string]string{"path": path.Join(dir, name)}, &imported); err != nil {
		return fmt.Errorf("import package: %w", err)
	}
	if !imported {
		return fmt.Errorf("import package: %w: native importer refused package", domain.ErrAnki)
	}
	return nil
}

// Media fetches one validated basename from Anki's media directory and bounds the decoded bytes.
func (c *Client) Media(ctx context.Context, name string) ([]byte, error) {
	if !cardrender.MediaName(name) {
		return nil, errors.New("media filename must be a safe basename")
	}
	var encoded string
	if err := c.call(ctx, "retrieveMediaFile", map[string]string{"filename": name}, &encoded); err != nil {
		return nil, fmt.Errorf("retrieve media: %w", err)
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(maxMediaBytes) {
		return nil, errors.New("media exceeds 16 MiB")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode media: %w", err)
	}
	if len(raw) > maxMediaBytes {
		return nil, errors.New("media exceeds 16 MiB")
	}
	return raw, nil
}
