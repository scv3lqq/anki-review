package domain

import (
	"testing"
	"time"
)

func TestProjectActivityCalendarAndAttribution(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, loc)
	first := time.Date(2025, 3, 10, 0, 0, 0, 0, loc)
	got := ProjectActivity(now, []ActivityEvent{
		{At: first, Deck: "A", Review: true},
		{At: first.Add(-time.Millisecond), Deck: "A", Review: true},
		{At: time.Date(2026, 3, 8, 23, 30, 0, 0, loc), Deck: "B", Review: true},
		{At: now.Add(time.Second), Deck: "B", Review: true},
		{At: now, Deck: "B"},
	})
	if len(got) != 365 || got[0].Date != "2025-03-10" || got[364].Date != "2026-03-09" {
		t.Fatalf("calendar boundaries: %#v", got)
	}
	if got[0].Reviews != 1 || got[363].Reviews != 1 || got[364].New != 1 || got[364].Reviews != 0 {
		t.Fatalf("counts: first=%+v yesterday=%+v today=%+v", got[0], got[363], got[364])
	}
	if len(got[364].Decks) != 1 || got[364].Decks[0].Name != "B" || got[364].Decks[0].New != 1 {
		t.Fatalf("deck: %+v", got[364])
	}
}

func TestProjectActivitySantiagoMidnightTransition(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2026-09-06", "2027-01-01", "2027-09-05"} {
		t.Run(date, func(t *testing.T) {
			today, err := time.Parse("2006-01-02", date)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, loc)
			first := today.AddDate(0, 0, -364)
			// 01:00 is the first instant on Santiago's spring-forward date.
			transition := time.Date(2026, 9, 6, 1, 0, 0, 0, loc)
			events := []ActivityEvent{{At: transition, Deck: "A", Review: true}, {At: now.Add(time.Second), Deck: "A", Review: true}}
			days := ProjectActivity(now, events)
			if len(days) != 365 {
				t.Fatalf("days: %d", len(days))
			}
			for i, day := range days {
				want := first.AddDate(0, 0, i).Format("2006-01-02")
				if day.Date != want {
					t.Fatalf("day %d: got %s want %s", i, day.Date, want)
				}
				wantReviews := 0
				if want == "2026-09-06" {
					wantReviews = 1
				}
				if day.Reviews != wantReviews {
					t.Fatalf("day %s: reviews %d want %d", want, day.Reviews, wantReviews)
				}
			}
		})
	}
}
