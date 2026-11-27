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

func comment(id, actor string, min int) github.Event {
	return github.Event{ID: id, Type: github.EventCommented, Actor: actor, At: at(min)}
}

func commit(oid string, committedMin int) github.Event {
	return github.Event{ID: oid, Type: github.EventCommitted, Actor: "me", At: at(committedMin)}
}

func eventIDs(events []github.Event) []string {
	var ids []string
	for _, ev := range events {
		ids = append(ids, ev.ID)
	}
	return ids
}

func TestTrackerSince_FirstPollUsesFirstSinceThenOverlap(t *testing.T) {
	tr := NewTracker(base.Add(-24 * time.Hour))
	if got, want := tr.Since(), base.Add(-24*time.Hour); !got.Equal(want) {
		t.Errorf("first Since = %v, want %v", got, want)
	}
	tr.Apply(base, nil)
	if got, want := tr.Since(), base.Add(-searchLagOverlap); !got.Equal(want) {
		t.Errorf("second Since = %v, want last start minus overlap %v", got, want)
	}
}

func TestTrackerSince_StaysAtFirstSinceUntilPollSucceeds(t *testing.T) {
	tr := NewTracker(at(-30))
	// 初回のポーリングが失敗すると Apply は呼ばれない。何度取り直しても始点は動かない
	for range 3 {
		if got := tr.Since(); !got.Equal(at(-30)) {
			t.Fatalf("Since before the first success = %v, want %v", got, at(-30))
		}
	}
}

func TestTrackerSince_NeverBeforeFirstSince(t *testing.T) {
	// -since 5m のように searchLagOverlap より短い幅で始めた
	tr := NewTracker(at(-5))
	tr.Apply(at(0), nil)
	if got := tr.Since(); !got.Equal(at(-5)) {
		t.Errorf("second Since = %v, want the first since %v (not %v)", got, at(-5), base.Add(-searchLagOverlap))
	}
}

func TestTrackerSince_TruncatedToSeconds(t *testing.T) {
	tr := NewTracker(base.Add(1500 * time.Millisecond))
	if got, want := tr.Since(), base.Add(time.Second); !got.Equal(want) {
		t.Errorf("first Since = %v, want %v", got, want)
	}
	tr.Apply(at(20).Add(300*time.Millisecond), nil)
	if got, want := tr.Since(), at(10); !got.Equal(want) {
		t.Errorf("second Since = %v, want %v", got, want)
	}
}

func TestTrackerApply_NewItemShowsEventInSameSecondAsSince(t *testing.T) {
	// 検索には秒に切り詰めた始点 12:00:00 が渡り、12:00:00 にコメントされた PR が返ってくる
	tr := NewTracker(base.Add(500 * time.Millisecond))
	batch := tr.Apply(at(5), []github.Item{pr(1, 0, comment("c1", "alice", 0))})
	if len(batch.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(batch.Entries))
	}
	if got := eventIDs(batch.Entries[0].Events); len(got) != 1 || got[0] != "c1" {
		t.Errorf("event IDs = %v, want [c1] (not an update without events)", got)
	}
}

func TestTrackerApply_SkipsAlreadyShownUpdates(t *testing.T) {
	tr := NewTracker(at(-60))
	first := tr.Apply(at(0), []github.Item{pr(1, -10, comment("c1", "alice", -10))})
	if len(first.Entries) != 1 {
		t.Fatalf("first batch entries = %d, want 1", len(first.Entries))
	}

	// 検索範囲が重なり、同じ PR がもう一度返ってきた
	second := tr.Apply(at(5), []github.Item{pr(1, -10, comment("c1", "alice", -10))})
	if len(second.Entries) != 0 {
		t.Errorf("second batch entries = %d, want 0 (already shown)", len(second.Entries))
	}
}

func TestTrackerApply_ShowsOnlyUnshownEvents(t *testing.T) {
	tr := NewTracker(at(-60))
	tr.Apply(at(0), []github.Item{pr(1, -10, comment("c1", "alice", -10))})

	batch := tr.Apply(at(5), []github.Item{pr(1, 3, comment("c1", "alice", -10), comment("c2", "bob", 3))})
	if len(batch.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(batch.Entries))
	}
	if got := eventIDs(batch.Entries[0].Events); len(got) != 1 || got[0] != "c2" {
		t.Errorf("event IDs = %v, want only bob's new comment c2", got)
	}
}

func TestTrackerApply_ShowsLatePushedCommitOfSeenItem(t *testing.T) {
	tr := NewTracker(at(-60))
	tr.Apply(at(0), []github.Item{pr(1, -10, comment("c1", "alice", -10))})

	// 12:20 に、手元で 10:00 にコミットしたものを push し、直後に CI のボットがコメントした。
	// コミットの時刻は前回表示した updatedAt よりも検索範囲の始点よりも古い。
	batch := tr.Apply(at(25), []github.Item{pr(1, 21, comment("c1", "alice", -10), commit("abc", -120), comment("c2", "ci-bot", 21))})
	if len(batch.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(batch.Entries))
	}
	if got := eventIDs(batch.Entries[0].Events); len(got) != 2 || got[0] != "abc" || got[1] != "c2" {
		t.Errorf("event IDs = %v, want [abc c2]", got)
	}
}

func TestTrackerApply_ShowsUpdateWithoutNewEventsAsEmpty(t *testing.T) {
	tr := NewTracker(at(-60))
	tr.Apply(at(0), []github.Item{pr(1, -10, comment("c1", "alice", -10))})

	// タイトルの編集など、取得対象外の操作で updatedAt だけが進んだ
	batch := tr.Apply(at(5), []github.Item{pr(1, 3, comment("c1", "alice", -10))})
	if len(batch.Entries) != 1 || len(batch.Entries[0].Events) != 0 {
		t.Errorf("entries = %+v, want one entry without events", batch.Entries)
	}
}

func TestTrackerApply_NewItemShowsEventsWithinSearchWindow(t *testing.T) {
	tr := NewTracker(at(-60))
	batch := tr.Apply(at(0), []github.Item{pr(1, -5, comment("old", "alice", -90), comment("new", "bob", -5))})
	if got := eventIDs(batch.Entries[0].Events); len(got) != 1 || got[0] != "new" {
		t.Errorf("event IDs = %v, want only the event after since", got)
	}
}

func TestTrackerApply_RemembersEventsNotShownOnFirstSight(t *testing.T) {
	tr := NewTracker(at(-60))
	// 2 時間前に push されたコミットは、初めて見たときの検索範囲の外
	tr.Apply(at(0), []github.Item{pr(1, -5, commit("abc", -120), comment("c1", "alice", -5))})

	batch := tr.Apply(at(5), []github.Item{pr(1, 3, commit("abc", -120), comment("c1", "alice", -5), comment("c2", "bob", 3))})
	if got := eventIDs(batch.Entries[0].Events); len(got) != 1 || got[0] != "c2" {
		t.Errorf("event IDs = %v, want only c2 (abc was fetched before, outside the range)", got)
	}
}

func TestTrackerApply_IgnoresOldEventShiftedIntoFetchedRange(t *testing.T) {
	tr := NewTracker(at(-60))
	tr.Apply(at(0), []github.Item{pr(1, -5, comment("c1", "alice", -5))})

	// 新しいイベントが消え、取得する直近の件数に、初めて見たときの始点より前のコメントが入ってきた
	batch := tr.Apply(at(5), []github.Item{pr(1, 3, comment("older", "carol", -90), comment("c1", "alice", -5))})
	if len(batch.Entries) != 1 || len(batch.Entries[0].Events) != 0 {
		t.Errorf("entries = %+v, want one entry without events (the old comment is not new)", batch.Entries)
	}
}

func TestTrackerApply_SortsEntriesByNewestShownEvent(t *testing.T) {
	tr := NewTracker(at(-60))
	tr.Apply(at(0), []github.Item{pr(1, -30, comment("c1", "alice", -30))})

	// #1 は updatedAt が新しいが、表示するイベントは #2 より古い
	batch := tr.Apply(at(5), []github.Item{
		pr(1, 4, comment("c1", "alice", -30), commit("abc", -20)),
		pr(2, 2, comment("c2", "bob", 2)),
	})
	if len(batch.Entries) != 2 || batch.Entries[0].Item.Number != 2 || batch.Entries[1].Item.Number != 1 {
		t.Errorf("entries = %+v, want #2 then #1", batch.Entries)
	}
}

func TestTrackerApply_ForgetsItemsFarBeforeSearchRange(t *testing.T) {
	tr := NewTracker(at(-60))
	tr.Apply(at(0), []github.Item{pr(1, -10, comment("c1", "alice", -10))})

	tr.Apply(at(60), nil)
	// 始点は 12:50 で、updatedAt の 11:50 は始点より前だが、seenRetention の内なので残す
	tr.Apply(at(100), nil)
	if !tr.SeenKeys()["o/r#1"] {
		t.Fatal("o/r#1 was forgotten within seenRetention")
	}

	// 始点は 13:30
	tr.Apply(at(200), nil)
	if tr.SeenKeys()["o/r#1"] {
		t.Error("o/r#1 is still remembered after falling seenRetention behind the search range")
	}
}
