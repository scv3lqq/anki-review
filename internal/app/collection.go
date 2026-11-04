package app

import (
	"context"
	"fmt"

	"github.com/scv3lqq/anki-review/internal/repository/ankiconnect"
	"github.com/scv3lqq/anki-review/internal/repository/apkg"
	"github.com/scv3lqq/anki-review/internal/repository/filestore"
	"github.com/scv3lqq/anki-review/internal/usecase/owner"
	"github.com/scv3lqq/anki-review/internal/usecase/review"
)

type collectionAdapter struct{ account *App }

func (c *collectionAdapter) client() *ankiconnect.Client {
	return ankiconnect.New(c.account.cfg.AnkiURL, func() string { return c.account.owner.Secrets().AnkiKey })
}
func (c *collectionAdapter) service() (*review.Service, error) {
	c.account.mu.Lock()
	defer c.account.mu.Unlock()
	if c.account.service == nil {
		return nil, fmt.Errorf("collection is locked")
	}
	return c.account.service, nil
}
func (c *collectionAdapter) Import(ctx context.Context, raw []byte) error {
	svc, err := c.service()
	if err != nil {
		return err
	}
	return svc.WithCollection(ctx, func() error {
		if err := apkg.Validate(raw); err != nil {
			return &owner.FieldError{Field: "package", Message: err.Error()}
		}
		return c.client().ImportPackage(ctx, raw)
	})
}
func (c *collectionAdapter) Media(ctx context.Context, name string) ([]byte, error) {
	return c.client().Media(ctx, name)
}
func (c *collectionAdapter) Preferences() ([]string, error) {
	return filestore.LoadPreferences(c.account.cfg.DataDir, c.account.cfg.KnowledgeDecks)
}
func (c *collectionAdapter) SavePreferences(ctx context.Context, decks []string) error {
	decks, err := filestore.NormalizeKnowledgeDecks(decks)
	if err != nil {
		return &owner.FieldError{Field: "knowledge_decks", Message: err.Error()}
	}
	svc, err := c.service()
	if err != nil {
		return err
	}
	return svc.UpdateKnowledgeDecks(ctx, decks, func() error { return filestore.SavePreferences(c.account.cfg.DataDir, decks) })
}
