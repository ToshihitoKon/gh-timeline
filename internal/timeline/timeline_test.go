package timeline

import (
	"testing"
	"time"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
)

var base = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func at(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

func pr(number int, updatedMin int, events ...github.Event) github.Item {
	return github.Item{Kind: github.KindPullRequest, Repo: "o/r", Number: number, UpdatedAt: at(updatedMin), Events: events}
}

func comment(actor string, min int) github.Event {
	return github.Event{Type: github.EventCommented, Actor: actor, At: at(min)}
}

func TestTrackerSince_FirstPollUsesLookbackThenOverlap(t *testing.T) {
	tr := NewTracker(24 * time.Hour)
	if got, want := tr.Since(base), base.Add(-24*time.Hour); !got.Equal(want) {
		t.Errorf("first Since = %v, want %v", got, want)
	}
	tr.Apply(base, base.Add(-24*time.Hour), nil)
	if got, want := tr.Since(at(5)), base.Add(-searchLagOverlap); !got.Equal(want) {
		t.Errorf("second Since = %v, want last start minus overlap %v", got, want)
	}
}

func TestTrackerApply_SkipsAlreadyShownUpdates(t *testing.T) {
	tr := NewTracker(time.Hour)
	first := tr.Apply(at(0), at(-60), []github.Item{pr(1, -10, comment("alice", -10))})
	if len(first.Entries) != 1 {
		t.Fatalf("first batch entries = %d, want 1", len(first.Entries))
	}

	// 検索範囲が重なり、同じ updatedAt の PR がもう一度返ってきた
	second := tr.Apply(at(5), at(-10), []github.Item{pr(1, -10, comment("alice", -10))})
	if len(second.Entries) != 0 {
		t.Errorf("second batch entries = %d, want 0 (already shown)", len(second.Entries))
	}
}

func TestTrackerApply_ShowsOnlyEventsAfterLastShown(t *testing.T) {
	tr := NewTracker(time.Hour)
	tr.Apply(at(0), at(-60), []github.Item{pr(1, -10, comment("alice", -10))})

	batch := tr.Apply(at(5), at(-10), []github.Item{pr(1, 3, comment("alice", -10), comment("bob", 3))})
	if len(batch.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(batch.Entries))
	}
	events := batch.Entries[0].Events
	if len(events) != 1 || events[0].Actor != "bob" {
		t.Errorf("events = %+v, want only bob's new comment", events)
	}
}

func TestTrackerApply_NewItemShowsEventsWithinSearchWindow(t *testing.T) {
	tr := NewTracker(time.Hour)
	batch := tr.Apply(at(0), at(-60), []github.Item{pr(1, -5, comment("old", -90), comment("new", -5))})
	events := batch.Entries[0].Events
	if len(events) != 1 || events[0].Actor != "new" {
		t.Errorf("events = %+v, want only the event after since", events)
	}
}

func TestTrackerApply_SortsEntriesOldestFirst(t *testing.T) {
	tr := NewTracker(time.Hour)
	// 検索結果は新しい順で返る
	batch := tr.Apply(at(0), at(-60), []github.Item{
		pr(2, -1, comment("x", -1)),
		pr(1, -20, comment("y", -20)),
	})
	if batch.Entries[0].Item.Number != 1 || batch.Entries[1].Item.Number != 2 {
		t.Errorf("order = #%d, #%d; want #1 then #2", batch.Entries[0].Item.Number, batch.Entries[1].Item.Number)
	}
}

func TestEntryIsOnlyBy(t *testing.T) {
	tests := []struct {
		name   string
		events []github.Event
		want   bool
	}{
		{"all mine", []github.Event{comment("me", 0), comment("me", 1)}, true},
		{"mixed", []github.Event{comment("me", 0), comment("alice", 1)}, false},
		{"no events", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Entry{Events: tt.events}).IsOnlyBy("me"); got != tt.want {
				t.Errorf("IsOnlyBy = %v, want %v", got, tt.want)
			}
		})
	}
}
