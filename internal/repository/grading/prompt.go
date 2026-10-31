// Package grading defines the shared rubric and structured grading contract.
package grading

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/scv3lqq/anki-review/internal/domain"
)

var (
	//go:embed prompts/*.tmpl
	promptFS  embed.FS
	templates = template.Must(template.ParseFS(promptFS, "prompts/*.tmpl"))

	//go:embed prompts/schema.json
	schemaRaw []byte
	// Schema is the compact JSON output contract shared by all providers.
	Schema = mustCompact(schemaRaw)

	reLineBreak = regexp.MustCompile(`\s*\n\s*`)
)

func mustCompact(raw []byte) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		panic(fmt.Sprintf("grading: prompts/schema.json: %v", err))
	}
	return b.String()
}

type promptCard struct {
	N        int
	IsNew    bool
	Topic    string
	Deck     string
	Question string
	Back     string
	Answer   string
}

// Prompt renders the grading prompt for answered items; answers maps card number to answer.
func Prompt(kind domain.Kind, items []domain.Item, answers map[int]string) (string, error) {
	name := "language.tmpl"
	if kind == domain.KindKnowledge {
		name = "knowledge.tmpl"
	}
	cards := make([]promptCard, len(items))
	for i, it := range items {
		cards[i] = promptCard{
			N:        it.N,
			IsNew:    it.IsNew,
			Topic:    it.Topic,
			Deck:     it.Deck,
			Question: it.Question,
			Back:     it.Back,
			// one line per answer keeps the card layout readable; " / " marks the breaks
			Answer: reLineBreak.ReplaceAllString(answers[it.N], " / "),
		}
	}
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name, struct{ Cards []promptCard }{cards}); err != nil {
		return "", fmt.Errorf("prompt %s: %w", name, err)
	}
	return b.String(), nil
}
