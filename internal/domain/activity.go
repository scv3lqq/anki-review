package domain

import (
	"sort"
	"time"
)

// ActivityEvent is a creation or rated review of an existing card.
type ActivityEvent struct {
	At     time.Time
	Deck   string
	Review bool
}

// ActivityDeck holds a day's counts for one current deck.
type ActivityDeck struct {
	Name    string `json:"name"`
	Reviews int    `json:"reviews"`
	New     int    `json:"new"`
}

// ActivityDay holds counts for a local calendar day.
type ActivityDay struct {
	Date    string         `json:"date"`
	Reviews int            `json:"reviews"`
	New     int            `json:"new"`
	Decks   []ActivityDeck `json:"decks"`
}

func activityFirstDate(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -364)
}

// ActivityFirstDate returns the first civil-date label in the activity calendar.
func ActivityFirstDate(now time.Time) string {
	return activityFirstDate(now).Format("2006-01-02")
}

// ProjectActivity groups events into local calendar days, excluding future events.
func ProjectActivity(now time.Time, events []ActivityEvent) []ActivityDay {
	loc := now.Location()
	first := activityFirstDate(now)
	days := make([]ActivityDay, 365)
	index := make(map[string]int, 365)
	decks := make([]map[string]*ActivityDeck, 365)
	for i := range days {
		date := first.AddDate(0, 0, i).Format("2006-01-02")
		days[i] = ActivityDay{Date: date, Decks: []ActivityDeck{}}
		index[date] = i
	}
	for _, event := range events {
		if event.At.After(now) {
			continue
		}
		i, ok := index[event.At.In(loc).Format("2006-01-02")]
		if !ok {
			continue
		}
		if decks[i] == nil {
			decks[i] = make(map[string]*ActivityDeck)
		}
		d := decks[i][event.Deck]
		if d == nil {
			d = &ActivityDeck{Name: event.Deck}
			decks[i][event.Deck] = d
		}
		if event.Review {
			days[i].Reviews++
			d.Reviews++
		} else {
			days[i].New++
			d.New++
		}
	}
	for i, byName := range decks {
		for _, d := range byName {
			days[i].Decks = append(days[i].Decks, *d)
		}
		sort.Slice(days[i].Decks, func(a, b int) bool { return days[i].Decks[a].Name < days[i].Decks[b].Name })
	}
	return days
}
