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

func TestModel_ViewKeepsHyperlinks(t *testing.T) {
	item := newItem(7, 1)
	item.URL = itemURL
	m := applyFetch(newReadyModel(fakeFetcher{}), item)
	// viewport や lipgloss を通しても OSC 8 が消えずに端末へ届く
	if view := m.View(); !strings.Contains(view, ansi.SetHyperlink(itemURL)) {
		t.Errorf("view has no hyperlink to %s:\n%q", itemURL, view)
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

func newReadyModel(f timeline.Fetcher) Model {
	return newReadyModelWithHeight(f, 20)
}

func newReadyModelWithHeight(f timeline.Fetcher, height int) Model {
	m := NewModel(f, timeline.NewTracker(time.Now().Add(-time.Hour)), 5*time.Minute, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: height})
	return next.(Model)
}

func TestModel_FetchAppendsBatchOnlyWhenUpdated(t *testing.T) {
	item := newItem(7, 1)
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
	m = applyFetch(m, item)
	if len(m.batches) != 1 {
		t.Errorf("batches = %d, want still 1", len(m.batches))
	}
}

func TestModel_FirstFetchWithNoUpdatesReplacesLoading(t *testing.T) {
	m := newReadyModel(fakeFetcher{result: github.SearchResult{Viewer: "me"}})
	if !strings.Contains(m.View(), "Loading...") {
		t.Fatalf("view before fetch does not show Loading...:\n%s", m.View())
	}

	next, cmd := m.Update(pollTickMsg{seq: 0})
	next, _ = next.(Model).Update(runCmd(t, cmd))
	view := next.(Model).View()

	if strings.Contains(view, "Loading...") {
		t.Errorf("view still shows Loading...:\n%s", view)
	}
	if !strings.Contains(view, "No updates since") {
		t.Errorf("view does not show No updates since:\n%s", view)
	}
}

func TestModel_FetchWithoutUpdatesKeepsContent(t *testing.T) {
	m := applyFetch(newReadyModel(fakeFetcher{}), newItem(1, 1))
	// 描き直したかどうかを、viewport の内容が置き換わるかで確かめる
	m.viewport.SetContent("not re-rendered")

	m = applyFetch(m)
	if got := m.viewport.View(); !strings.Contains(got, "not re-rendered") {
		t.Errorf("timeline re-rendered after a fetch with no updates:\n%s", got)
	}

	m = applyFetch(m, newItem(2, 1))
	if got := m.viewport.View(); strings.Contains(got, "not re-rendered") {
		t.Errorf("timeline not re-rendered after a fetch with updates:\n%s", got)
	}
}

func TestModel_ScrollFollowsTopOrKeepsReadingPosition(t *testing.T) {
	m := newReadyModelWithHeight(fakeFetcher{}, chromeLines+3)
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
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = next.(Model)
	if !m.isFetching {
		t.Fatal("r did not start fetching")
	}
	// r より前に予約した tick が、取得中と取得後のそれぞれで届く
	if _, tickCmd := m.Update(pollTickMsg{seq: 0}); tickCmd != nil {
		t.Error("stale poll tick started another fetch while fetching")
	}
	next, _ = m.Update(runCmd(t, cmd))
	m = next.(Model)
	if _, tickCmd := m.Update(pollTickMsg{seq: 0}); tickCmd != nil {
		t.Error("stale poll tick started another fetch after the refresh finished")
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

// deliverFetch は startedAt に始めた取得が result と err を返した状況を Model に流し、Update が返した Cmd も返す。
func deliverFetch(m Model, startedAt time.Time, result github.SearchResult, err error) (Model, tea.Cmd) {
	m.isFetching = true
	next, cmd := m.Update(fetchedMsg{poll: m.tracker.StartPoll(startedAt), result: result, err: err})
	return next.(Model), cmd
}

// applyResult は取得が成功し、result が返ってきた状況を Model に流す。
func applyResult(m Model, result github.SearchResult) Model {
	m, _ = deliverFetch(m, time.Now(), result, nil)
	return m
}

// applyFetch は取得が成功し、items が返ってきた状況を Model に流す。
func applyFetch(m Model, items ...github.Item) Model {
	return applyResult(m, github.SearchResult{Viewer: "me", Items: items})
}

// failFetch は取得が失敗した状況を Model に流す。
func failFetch(m Model) Model {
	m, _ = deliverFetch(m, time.Now(), github.SearchResult{}, errors.New("boom"))
	return m
}

var spaceKey = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}

func TestModel_FirstFetchIsRead(t *testing.T) {
	m := applyFetch(newReadyModel(fakeFetcher{}), newItem(1, 1))
	if m.readBatchCount != 1 {
		t.Errorf("readBatchCount = %d, want 1", m.readBatchCount)
	}
}

func TestModel_FirstFetchErrorThenSuccessIsRead(t *testing.T) {
	m := failFetch(newReadyModel(fakeFetcher{}))
	m = applyFetch(m, newItem(1, 1))
	if m.readBatchCount != 1 {
		t.Errorf("readBatchCount = %d, want 1", m.readBatchCount)
	}
}

func TestModel_ResizeAfterFailedFirstFetchStillShowsLoading(t *testing.T) {
	m := failFetch(newReadyModel(fakeFetcher{}))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	view := next.(Model).View()
	if strings.Contains(view, "No updates since") || !strings.Contains(view, "Loading...") {
		t.Errorf("view after a failed first fetch claims nothing was found:\n%s", view)
	}
}

func TestModel_PartialErrorsShowInFooterWithResults(t *testing.T) {
	m := applyResult(newReadyModel(fakeFetcher{}), github.SearchResult{
		Viewer:        "me",
		Items:         []github.Item{newItem(1, 1)},
		PartialErrors: []string{"Resource protected by\norganization SAML enforcement."},
	})
	if len(m.batches) != 1 {
		t.Errorf("batches = %d, want 1 (results kept despite partial errors)", len(m.batches))
	}
	if footer := ansi.Strip(m.footer()); !strings.HasPrefix(footer, "some results are missing: Resource protected by organization SAML enforcement.") {
		t.Errorf("footer = %q, want the partial error on one line", footer)
	}

	// 次の取得でエラーが無くなれば警告を消す
	m = applyFetch(m)
	if footer := ansi.Strip(m.footer()); strings.Contains(footer, "missing") {
		t.Errorf("footer = %q, want no warning after a clean fetch", footer)
	}
}

func TestModel_TruncatedWarningShowsInFooter(t *testing.T) {
	m := applyResult(newReadyModel(fakeFetcher{}), github.SearchResult{Viewer: "me", Truncated: true})
	want := fmt.Sprintf("some results were truncated: over %d hits", github.MaxSearchHits)
	if footer := ansi.Strip(m.footer()); !strings.HasPrefix(footer, want) {
		t.Errorf("footer = %q, want it to start with %q", footer, want)
	}
}

func TestModel_LaterFetchIsUnreadUntilSpace(t *testing.T) {
	m := applyFetch(newReadyModel(fakeFetcher{})) // 初回は更新なし
	m = applyFetch(m, newItem(1, 1))
	if m.readBatchCount != 0 {
		t.Fatalf("readBatchCount = %d, want 0 before space", m.readBatchCount)
	}
	if top := ansi.Strip(m.viewport.View()); !strings.HasPrefix(top, "▐ --- ") {
		t.Errorf("top line not marked unread:\n%s", top)
	}

	next, _ := m.Update(spaceKey)
	m = next.(Model)
	if m.readBatchCount != 1 {
		t.Errorf("readBatchCount = %d, want 1 after space", m.readBatchCount)
	}
	if top := ansi.Strip(m.viewport.View()); !strings.HasPrefix(top, "  --- ") {
		t.Errorf("top line still marked unread after space:\n%s", top)
	}
}

func TestModel_SpaceKeepsScrollPosition(t *testing.T) {
	m := newReadyModelWithHeight(fakeFetcher{}, chromeLines+3)
	m.batches = []timeline.Batch{batchAt(11, 40), batchAt(11, 45), batchAt(11, 50)}
	m.refreshContent()
	m.viewport.SetYOffset(2)

	next, _ := m.Update(spaceKey)
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

func TestModel_FetchErrorKeepsLastSuccessfulCheckTime(t *testing.T) {
	m, _ := deliverFetch(newReadyModel(fakeFetcher{}), time.Date(2026, 11, 20, 11, 45, 0, 0, time.Local), github.SearchResult{Viewer: "me"}, nil)
	m, _ = deliverFetch(m, time.Date(2026, 11, 20, 11, 50, 0, 0, time.Local), github.SearchResult{}, errors.New("boom"))
	if got := ansi.Strip(m.statusLine()); !strings.HasPrefix(got, "checked 11:45 · next ") {
		t.Errorf("status line = %q, want the time of the last successful check", got)
	}
}

func TestModel_FirstFetchErrorShowsOnlyNextPoll(t *testing.T) {
	m := failFetch(newReadyModel(fakeFetcher{}))
	if got := ansi.Strip(m.statusLine()); strings.Contains(got, "checked") || !strings.HasPrefix(got, "next ") {
		t.Errorf("status line = %q, want only the next poll before any successful check", got)
	}
}

func TestModel_ViewTruncatesTitleToWidth(t *testing.T) {
	next, _ := newReadyModel(fakeFetcher{}).Update(tea.WindowSizeMsg{Width: 10, Height: 20})
	title := strings.Split(next.(Model).View(), "\n")[0]
	if w := ansi.StringWidth(title); w > 10 {
		t.Errorf("title %q width = %d, want <= 10", ansi.Strip(title), w)
	}
}

func TestModel_FetchErrorShownInFooter(t *testing.T) {
	m, cmd := deliverFetch(newReadyModel(fakeFetcher{}), time.Now(), github.SearchResult{}, errors.New("gh api: exit status 1:\nHTTP 401"))
	if cmd == nil {
		t.Error("next poll not scheduled after error")
	}
	if got := ansi.Strip(m.footer()); !strings.Contains(got, "error: gh api: exit status 1: HTTP 401") {
		t.Errorf("footer = %q, want single-line error", got)
	}
	// 80 桁の端末で切り詰めても、キー案内に押し出されずにエラーの本文が読める
	if got := ansi.Strip(truncate(m.footer(), m.viewport.Width)); !strings.Contains(got, "error: gh api: exit status 1: HTTP 401") {
		t.Errorf("footer at width %d = %q, want the error text to survive truncation", m.viewport.Width, got)
	}
}

func TestModel_StatusLineWhileFetchingHasOneSpaceAfterSpinner(t *testing.T) {
	m := newReadyModel(fakeFetcher{})
	m.isFetching = true
	if got := ansi.Strip(m.statusLine()); got != "⣾ checking..." {
		t.Errorf("status line = %q, want %q", got, "⣾ checking...")
	}
}
