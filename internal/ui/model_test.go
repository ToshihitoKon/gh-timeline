package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
)

var jst = time.FixedZone("JST", 9*60*60)

func entry(events ...github.Event) timeline.Entry {
	return timeline.Entry{
		Item: github.Item{
			Kind: github.KindPullRequest, Repo: "o/r", Number: 7, Title: "Add feature",
			State: "OPEN", Roles: github.RoleAuthor, UpdatedAt: time.Date(2026, 11, 20, 14, 0, 0, 0, jst),
		},
		Events: events,
	}
}

func ev(t github.EventType, actor string, hh, mm int) github.Event {
	return github.Event{Type: t, Actor: actor, At: time.Date(2026, 11, 20, hh, mm, 0, 0, jst)}
}

// wantRow は "時刻 アクション ユーザー 詳細" の 1 行を、固定の列幅で組み立てる。
func wantRow(at, action, user, detail string) string {
	return fmt.Sprintf("%-*s %-*s %-*s %s", len(at), at, actionColWidth, action, userColWidth, user, detail)
}

func TestRenderBatch_OneRowPerEventNewestFirst(t *testing.T) {
	other := entry(ev(github.EventCommented, "bob", 13, 55))
	other.Item.Kind = github.KindIssue
	other.Item.Number = 8
	other.Item.Title = "Bug"
	b := timeline.Batch{
		CheckedAt: time.Date(2026, 11, 20, 14, 5, 0, 0, jst),
		Entries: []timeline.Entry{
			entry(
				ev(github.EventCommitted, "me", 13, 50),
				ev(github.EventCommitted, "me", 13, 51),
				ev(github.EventApproved, "alice", 14, 0),
			),
			other,
		},
	}
	got := ansi.Strip(RenderBatch(b, time.Time{}, "me", 0))
	want := strings.Join([]string{
		"--- 14:05 ---",
		wantRow("14:00", "approved", "alice", "PR o/r#7 Add feature"),
		wantRow("13:55", "commented", "bob", "IS o/r#8 Bug"), // 別の PR / issue のイベントも時刻順に混ざる
		wantRow("13:51", "pushed 2 commits", "me", "PR o/r#7 Add feature"),
	}, "\n")
	if got != want {
		t.Errorf("RenderBatch =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatch_CollapsesNoisyEvents(t *testing.T) {
	request := func(target string) github.Event {
		e := ev(github.EventReviewRequested, "alice", 13, 0)
		e.Target = target
		return e
	}
	b := timeline.Batch{
		CheckedAt: time.Date(2026, 11, 20, 14, 5, 0, 0, jst),
		Entries: []timeline.Entry{entry(
			request("bob"),
			request("carol"),
			ev(github.EventCommented, "bot", 13, 10),
			ev(github.EventCommented, "bot", 13, 11),
			ev(github.EventMerged, "bot", 13, 12),
			ev(github.EventClosed, "bot", 13, 12),
		)},
	}
	got := ansi.Strip(RenderBatch(b, time.Time{}, "me", 0))
	want := strings.Join([]string{
		"--- 14:05 ---",
		wantRow("13:12", "merged", "bot", "PR o/r#7 Add feature"),
		wantRow("13:11", "commented (2)", "bot", "PR o/r#7 Add feature"),
		wantRow("13:00", "review requested", "alice", "PR o/r#7 → bob, carol Add feature"),
	}, "\n")
	if got != want {
		t.Errorf("RenderBatch =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatch_DateShownWhenDayChanges(t *testing.T) {
	prev := time.Date(2026, 11, 19, 23, 58, 0, 0, jst)
	yesterday := github.Event{Type: github.EventCommented, Actor: "alice", At: time.Date(2026, 11, 19, 23, 59, 0, 0, jst)}
	b := timeline.Batch{
		CheckedAt: time.Date(2026, 11, 20, 0, 3, 0, 0, jst),
		Entries:   []timeline.Entry{entry(), entry(yesterday)},
	}
	got := ansi.Strip(RenderBatch(b, prev, "me", 0))
	want := strings.Join([]string{
		"--- 11/20 00:03 ---",
		// イベントの無い更新は updatedAt を "updated" として、誰の操作か分からないので "-" で出す
		wantRow("14:00      ", "updated", "-", "PR o/r#7 Add feature"),
		wantRow("11/19 23:59", "commented", "alice", "PR o/r#7 Add feature"),
	}, "\n")
	if got != want {
		t.Errorf("RenderBatch =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatch_SameMinuteEventsNewestFirstAndEmptyTargetOmitted(t *testing.T) {
	requested := ev(github.EventReviewRequested, "alice", 13, 0) // 依頼先が取れなかった（Target が空）
	assigned := ev(github.EventAssigned, "alice", 13, 0)
	assigned.Target = "alice"
	b := timeline.Batch{
		CheckedAt: time.Date(2026, 11, 20, 14, 5, 0, 0, jst),
		Entries:   []timeline.Entry{entry(requested, assigned)},
	}
	got := ansi.Strip(RenderBatch(b, time.Time{}, "me", 0))
	want := strings.Join([]string{
		"--- 14:05 ---",
		wantRow("13:00", "assigned", "alice", "PR o/r#7 → alice Add feature"),
		wantRow("13:00", "review requested", "alice", "PR o/r#7 Add feature"),
	}, "\n")
	if got != want {
		t.Errorf("RenderBatch =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatch_TruncatesLongUserName(t *testing.T) {
	long := strings.Repeat("x", userColWidth+5)
	b := timeline.Batch{CheckedAt: time.Date(2026, 11, 20, 14, 5, 0, 0, jst), Entries: []timeline.Entry{entry(ev(github.EventCommented, long, 14, 0))}}
	got := ansi.Strip(RenderBatch(b, time.Time{}, "me", 0))
	if want := wantRow("14:00", "commented", strings.Repeat("x", userColWidth-2)+"…", "PR o/r#7 Add feature"); !strings.HasSuffix(got, want) {
		t.Errorf("RenderBatch =\n%s\nwant suffix\n%s", got, want)
	}
}

func TestRenderBatch_TruncatesToWidth(t *testing.T) {
	b := timeline.Batch{CheckedAt: time.Date(2026, 11, 20, 14, 5, 0, 0, jst), Entries: []timeline.Entry{entry()}}
	for _, line := range strings.Split(RenderBatch(b, time.Time{}, "me", 12), "\n") {
		if w := ansi.StringWidth(line); w > 12 {
			t.Errorf("line %q width = %d, want <= 12", ansi.Strip(line), w)
		}
	}
}

type fakeFetcher struct {
	result github.SearchResult
	err    error
}

func (f fakeFetcher) Search(context.Context, time.Time) (github.SearchResult, error) {
	return f.result, f.err
}

// runCmd は tea.Cmd を実行し、返ってきた Msg を返す。
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("cmd is nil")
	}
	return cmd()
}

func newReadyModel(f Fetcher) Model {
	m := NewModel(f, timeline.NewTracker(time.Hour), 5*time.Minute)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	return next.(Model)
}

func TestModel_FetchAppendsBatchOnlyWhenUpdated(t *testing.T) {
	item := entry(ev(github.EventCommented, "alice", 14, 0)).Item
	item.Events = []github.Event{{Type: github.EventCommented, Actor: "alice", At: time.Now().Add(-time.Minute)}}
	item.UpdatedAt = time.Now().Add(-time.Minute)
	m := newReadyModel(fakeFetcher{result: github.SearchResult{Viewer: "me", Items: []github.Item{item}}})

	next, cmd := m.Update(pollTickMsg{seq: 0})
	m = next.(Model)
	if !m.isFetching {
		t.Fatal("isFetching = false after poll tick")
	}
	next, _ = m.Update(runCmd(t, cmd))
	m = next.(Model)
	if len(m.batches) != 1 {
		t.Fatalf("batches = %d, want 1", len(m.batches))
	}

	// 同じ結果がもう一度返っても、区切り行ごと追加しない
	m.isFetching = true
	next, _ = m.Update(fetchedMsg{startedAt: time.Now(), since: time.Now().Add(-time.Hour), result: github.SearchResult{Viewer: "me", Items: []github.Item{item}}})
	m = next.(Model)
	if len(m.batches) != 1 {
		t.Errorf("batches = %d, want still 1", len(m.batches))
	}
}

func batchAt(hh, mm int) timeline.Batch {
	return timeline.Batch{
		CheckedAt: time.Date(2026, 11, 20, hh, mm, 0, 0, jst),
		Entries:   []timeline.Entry{entry(ev(github.EventCommented, "alice", hh, mm-1))},
	}
}

func TestRenderBatches_NewestBatchOnTop(t *testing.T) {
	got := ansi.Strip(RenderBatches([]timeline.Batch{batchAt(11, 45), batchAt(11, 50)}, "me", 0))
	if first, second := strings.Index(got, "--- 11:50 ---"), strings.Index(got, "--- 11:45 ---"); first != 0 || second < first {
		t.Errorf("want 11:50 block first, got\n%s", got)
	}
}

func TestModel_ScrollFollowsTopOrKeepsReadingPosition(t *testing.T) {
	m := NewModel(fakeFetcher{}, timeline.NewTracker(time.Hour), 5*time.Minute)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: chromeLines + 3})
	m = next.(Model)
	m.batches = []timeline.Batch{batchAt(11, 40), batchAt(11, 45)}
	m.refreshContent()

	// 先頭を見ている間は、追記されても先頭に留まる
	m.batches = append(m.batches, batchAt(11, 50))
	m.refreshContent()
	if m.viewport.YOffset != 0 {
		t.Errorf("YOffset = %d, want 0 while following the top", m.viewport.YOffset)
	}

	// 過去を読んでいる間は、同じ行が見え続けるようオフセットをずらす
	m.viewport.SetYOffset(5)
	before := m.viewport.View()
	m.batches = append(m.batches, batchAt(11, 55))
	m.refreshContent()
	if after := m.viewport.View(); after != before {
		t.Errorf("visible lines changed while reading older updates:\nbefore\n%s\nafter\n%s", before, after)
	}
}

func TestModel_StalePollTickIgnoredAfterManualRefresh(t *testing.T) {
	m := newReadyModel(fakeFetcher{})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = next.(Model)
	if !m.isFetching {
		t.Fatal("r did not start fetching")
	}
	m.isFetching = false // 取得完了前に古い tick が届いた状況を作る
	if _, cmd := m.Update(pollTickMsg{seq: 0}); cmd != nil {
		t.Error("stale poll tick started another fetch")
	}
}

func TestModel_FetchErrorShownInFooter(t *testing.T) {
	m := newReadyModel(fakeFetcher{})
	m.isFetching = true
	next, cmd := m.Update(fetchedMsg{startedAt: time.Now(), err: errors.New("gh api: exit status 1:\nHTTP 401")})
	m = next.(Model)
	if cmd == nil {
		t.Error("next poll not scheduled after error")
	}
	if got := ansi.Strip(m.footer()); !strings.Contains(got, "error: gh api: exit status 1: HTTP 401") {
		t.Errorf("footer = %q, want single-line error", got)
	}
}
