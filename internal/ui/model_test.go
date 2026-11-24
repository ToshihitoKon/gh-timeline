package ui

import (
	"context"
	"errors"
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

func TestRenderBatch_GroupsEventsByItemNewestFirst(t *testing.T) {
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
		"o/r PR#7 Add feature", // 最新のイベントが新しい PR / issue が上
		"  14:00 approved alice",
		"  13:51 pushed 2 commits me",
		"o/r IS#8 Bug",
		"  13:55 commented bob",
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
		"o/r PR#7 Add feature",
		"  13:12 merged bot",
		"  13:11 commented (2) bot",
		"  13:00 review requested alice → bob, carol",
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
		"o/r PR#7 Add feature",
		"  14:00 updated -",
		"o/r PR#7 Add feature",
		"  11/19 23:59 commented alice",
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
		"o/r PR#7 Add feature",
		"  13:00 assigned alice → alice",
		"  13:00 review requested alice",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatch =\n%s\nwant\n%s", got, want)
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
	got := ansi.Strip(RenderBatches([]timeline.Batch{batchAt(11, 45), batchAt(11, 50)}, 0, "me", 0))
	// 左端の欄の 1 桁があるため、先頭の区切り行は 2 桁目から始まる
	if first, second := strings.Index(got, "--- 11:50 ---"), strings.Index(got, "--- 11:45 ---"); first != len(unreadMarker) || second < first {
		t.Errorf("want 11:50 block first, got\n%s", got)
	}
}

func TestRenderBatches_UnreadBlocksMarkedAtLeftEdge(t *testing.T) {
	got := ansi.Strip(RenderBatches([]timeline.Batch{batchAt(11, 45), batchAt(11, 50)}, 1, "me", 0))
	want := strings.Join([]string{
		"▎--- 11:50 ---",
		"▎o/r PR#7 Add feature",
		"▎  11:49 commented alice",
		"",
		" --- 11:45 ---",
		" o/r PR#7 Add feature",
		"   11:44 commented alice",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_TruncatesIncludingMarker(t *testing.T) {
	for _, width := range []int{1, 12} {
		for _, line := range strings.Split(RenderBatches([]timeline.Batch{batchAt(11, 45)}, 0, "me", width), "\n") {
			// 幅 1 では欄の 1 桁に加えて本文を 1 桁（省略記号）だけ残すため、2 桁まで許す
			if w := ansi.StringWidth(line); w > max(width, 2) {
				t.Errorf("width %d: line %q width = %d", width, ansi.Strip(line), w)
			}
		}
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

// newItem は検索結果に入る PR を、指定の分前に alice がコメントした状態で作る。
// 番号を変えると Tracker が別の PR として扱い、必ず新しい更新になる。
func newItem(number int, minutesAgo int) github.Item {
	at := time.Now().Add(-time.Duration(minutesAgo) * time.Minute)
	item := entry().Item
	item.Number = number
	item.UpdatedAt = at
	item.Events = []github.Event{{Type: github.EventCommented, Actor: "alice", At: at}}
	return item
}

// applyFetch は取得が成功し、items が返ってきた状況を Model に流す。
func applyFetch(t *testing.T, m Model, items ...github.Item) Model {
	t.Helper()
	m.isFetching = true
	next, _ := m.Update(fetchedMsg{
		startedAt: time.Now(),
		since:     time.Now().Add(-time.Hour),
		result:    github.SearchResult{Viewer: "me", Items: items},
	})
	return next.(Model)
}

var spaceKey = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}

func TestModel_FirstFetchIsRead(t *testing.T) {
	m := applyFetch(t, newReadyModel(fakeFetcher{}), newItem(1, 1))
	if m.readBatchCount != 1 {
		t.Errorf("readBatchCount = %d, want 1", m.readBatchCount)
	}
}

func TestModel_FirstFetchErrorThenSuccessIsRead(t *testing.T) {
	m := newReadyModel(fakeFetcher{})
	m.isFetching = true
	next, _ := m.Update(fetchedMsg{startedAt: time.Now(), err: errors.New("boom")})
	m = applyFetch(t, next.(Model), newItem(1, 1))
	if m.readBatchCount != 1 {
		t.Errorf("readBatchCount = %d, want 1", m.readBatchCount)
	}
}

func TestModel_LaterFetchIsUnreadUntilSpace(t *testing.T) {
	m := applyFetch(t, newReadyModel(fakeFetcher{})) // 初回は更新なし
	m = applyFetch(t, m, newItem(1, 1))
	if m.readBatchCount != 0 {
		t.Fatalf("readBatchCount = %d, want 0 before space", m.readBatchCount)
	}
	if top := ansi.Strip(m.viewport.View()); !strings.HasPrefix(top, unreadMarker+"--- ") {
		t.Errorf("top line not marked unread:\n%s", top)
	}

	next, _ := m.Update(spaceKey)
	m = next.(Model)
	if m.readBatchCount != 1 {
		t.Errorf("readBatchCount = %d, want 1 after space", m.readBatchCount)
	}
	if top := ansi.Strip(m.viewport.View()); !strings.HasPrefix(top, readGutter+"--- ") {
		t.Errorf("top line still marked unread after space:\n%s", top)
	}
}

func TestModel_SpaceKeepsScrollPosition(t *testing.T) {
	m := NewModel(fakeFetcher{}, timeline.NewTracker(time.Hour), 5*time.Minute)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: chromeLines + 3})
	m = next.(Model)
	m.batches = []timeline.Batch{batchAt(11, 40), batchAt(11, 45), batchAt(11, 50)}
	m.refreshContent()
	m.viewport.SetYOffset(2)

	next, _ = m.Update(spaceKey)
	m = next.(Model)
	if m.viewport.YOffset != 2 {
		t.Errorf("YOffset = %d, want 2 (space must not scroll)", m.viewport.YOffset)
	}
}

func TestModel_SpaceWithNoBatches(t *testing.T) {
	next, cmd := newReadyModel(fakeFetcher{}).Update(spaceKey)
	if m := next.(Model); m.readBatchCount != 0 || cmd != nil {
		t.Errorf("readBatchCount = %d, cmd = %v; want 0, nil", m.readBatchCount, cmd)
	}
}

func TestModel_FooterShowsMarkRead(t *testing.T) {
	if got := ansi.Strip(newReadyModel(fakeFetcher{}).footer()); !strings.Contains(got, "space: mark read") {
		t.Errorf("footer = %q, want it to mention space: mark read", got)
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
