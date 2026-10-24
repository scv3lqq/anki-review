package ankiconnect

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/scv3lqq/anki-review/internal/domain"
)

// "Default" is hidden when empty and makes getDeckStats fail for the whole list.
const defaultDeck = "Default"

type deckStats struct {
	Name        string `json:"name"`
	NewCount    int    `json:"new_count"`
	LearnCount  int    `json:"learn_count"`
	ReviewCount int    `json:"review_count"`
}

type field struct {
	Value string `json:"value"`
}

type cardInfo struct {
	CardID      int64            `json:"cardId"`
	Fields      map[string]field `json:"fields"`
	ModelName   string           `json:"modelName"`
	DeckName    string           `json:"deckName"`
	Question    string           `json:"question"`
	Answer      string           `json:"answer"`
	Type        int              `json:"type"`
	Due         int64            `json:"due"`
	Reps        int              `json:"reps"`
	Mod         int64            `json:"mod"`
	NextReviews []string         `json:"nextReviews"`
}

// cardTypeNew is cardInfo.Type of a card never studied.
const cardTypeNew = 0

// DeckNames lists every deck, subdecks as "Parent::Child".
func (c *Client) DeckNames(ctx context.Context) ([]string, error) {
	var names []string
	if err := c.call(ctx, "deckNames", nil, &names); err != nil {
		return nil, err
	}
	return names, nil
}

// Decks lists decks with something due today, in deck-tree order. Kind is left to the caller.
func (c *Client) Decks(ctx context.Context) ([]domain.Deck, error) {
	names, err := c.DeckNames(ctx)
	if err != nil {
		return nil, err
	}
	names = slices.DeleteFunc(names, func(n string) bool { return n == defaultDeck })
	if len(names) == 0 {
		return nil, nil
	}
	var stats map[string]deckStats
	if err := c.call(ctx, "getDeckStats", map[string]any{"decks": names}, &stats); err != nil {
		return nil, err
	}
	var decks []domain.Deck
	for _, s := range stats {
		d := domain.Deck{Name: s.Name, Learn: s.LearnCount, Review: s.ReviewCount, New: s.NewCount}
		d.Due = d.Learn + d.Review + d.New
		if d.Due > 0 {
			decks = append(decks, d)
		}
	}
	slices.SortFunc(decks, func(a, b domain.Deck) int {
		return slices.CompareFunc(strings.Split(a.Name, "::"), strings.Split(b.Name, "::"), strings.Compare)
	})
	return decks, nil
}

// queue returns due card ids of a deck (with subdecks) in Anki's default order: learning first
// (they lapse fastest), then reviews, new cards last, as many new as Anki's daily limit allows.
func (c *Client) queue(ctx context.Context, deck string, limit int) ([]int64, error) {
	base := fmt.Sprintf(`-is:suspended -is:buried "deck:%s"`, escapeDeck(deck))
	var learn, review []int64
	if err := c.call(ctx, "findCards", map[string]any{"query": "is:learn is:due " + base}, &learn); err != nil {
		return nil, err
	}
	if err := c.call(ctx, "findCards", map[string]any{"query": "is:review is:due -is:learn " + base}, &review); err != nil {
		return nil, err
	}
	var stats map[string]deckStats
	if err := c.call(ctx, "getDeckStats", map[string]any{"decks": []string{deck}}, &stats); err != nil {
		return nil, err
	}
	newLimit := 0
	for _, s := range stats {
		newLimit = s.NewCount
	}
	var fresh []int64
	if newLimit > 0 {
		var ids []int64
		if err := c.call(ctx, "findCards", map[string]any{"query": "is:new " + base}, &ids); err != nil {
			return nil, err
		}
		infos, err := c.cardsInfo(ctx, ids, nil)
		if err != nil {
			return nil, err
		}
		slices.SortStableFunc(infos, func(a, b cardInfo) int { return cmp.Compare(a.Due, b.Due) })
		for _, info := range infos[:min(newLimit, len(infos))] {
			fresh = append(fresh, info.CardID)
		}
	}

	seen := make(map[int64]bool)
	var out []int64
	for _, id := range slices.Concat(learn, review, fresh) {
		if !seen[id] && len(out) < limit {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func (c *Client) cardsInfo(ctx context.Context, ids []int64, fields []string) ([]cardInfo, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	params := map[string]any{"cards": ids}
	if fields != nil {
		params["fields"] = fields
	}
	var infos []cardInfo
	if err := c.call(ctx, "cardsInfo", params, &infos); err != nil {
		return nil, err
	}
	return infos, nil
}

// DueCards takes up to limit due cards of a deck as pass items, numbered from 1.
func (c *Client) DueCards(ctx context.Context, deck string, limit int) ([]domain.Item, error) {
	ids, err := c.queue(ctx, deck, limit)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	infos, err := c.cardsInfo(ctx, ids, nil)
	if err != nil {
		return nil, err
	}
	// nextReviews only comes back when asked for by name
	next, err := c.cardsInfo(ctx, ids, []string{"cardId", "nextReviews"})
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]cardInfo, len(infos))
	for _, info := range infos {
		byID[info.CardID] = info
	}
	labels := make(map[int64][]string, len(next))
	for _, info := range next {
		labels[info.CardID] = info.NextReviews
	}

	items := make([]domain.Item, 0, len(ids))
	for _, id := range ids {
		info, ok := byID[id]
		if !ok {
			continue
		}
		question, back := render(info)
		topic := info.DeckName
		if src, ok := info.Fields["Source"]; ok && htmlToText(src.Value) != "" {
			topic = htmlToText(src.Value)
		}
		items = append(items, domain.Item{
			N:         len(items) + 1,
			CardID:    id,
			Mod:       info.Mod,
			Reps:      info.Reps,
			Deck:      info.DeckName,
			Topic:     topic,
			IsNew:     info.Type == cardTypeNew,
			Question:  question,
			Back:      back,
			Intervals: intervals(labels[id]),
		})
	}
	return items, nil
}

func intervals(labels []string) map[domain.Grade]string {
	if len(labels) != len(domain.Grades) {
		return nil
	}
	m := make(map[domain.Grade]string, len(labels))
	for i, g := range domain.Grades {
		m[g] = buttonLabel(labels[i])
	}
	return m
}

// DueCount is how many cards of the deck are still due today.
func (c *Client) DueCount(ctx context.Context, deck string) (int, error) {
	const all = 100_000
	ids, err := c.queue(ctx, deck, all)
	return len(ids), err
}

// CardStates reports reps and mod of the cards that still exist.
func (c *Client) CardStates(ctx context.Context, ids []int64) (map[int64]domain.CardState, error) {
	infos, err := c.cardsInfo(ctx, ids, nil)
	if err != nil {
		return nil, err
	}
	states := make(map[int64]domain.CardState, len(infos))
	for _, info := range infos {
		// cardsInfo returns an empty object for a deleted card
		if info.CardID != 0 {
			states[info.CardID] = domain.CardState{Reps: info.Reps, Mod: info.Mod}
		}
	}
	return states, nil
}

type answerParam struct {
	CardID int64 `json:"cardId"`
	Ease   int   `json:"ease"`
}

// Answer presses the buttons; ok[i] reports whether Anki took answers[i]. A refused queue
// (domain.ErrQueueBusy) aborts the batch before any card is written.
func (c *Client) Answer(ctx context.Context, answers []domain.CardAnswer) ([]bool, error) {
	if len(answers) == 0 {
		return nil, nil
	}
	params := make([]answerParam, len(answers))
	for i, a := range answers {
		params[i] = answerParam{CardID: a.CardID, Ease: a.Ease}
	}
	var ok []bool
	if err := c.call(ctx, "answerCards", map[string]any{"answers": params}, &ok); err != nil {
		return nil, err
	}
	return ok, nil
}

// Sync syncs the collection with AnkiWeb.
func (c *Client) Sync(ctx context.Context) error {
	return c.call(ctx, "sync", nil, nil)
}
