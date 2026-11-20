package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
)

// 列幅。区切りをまたいでも列がそろうよう、回ごとの最大幅ではなく固定幅にする。
const (
	actionColWidth = 18 // 最長のラベル "converted to draft" に合わせる
	userColWidth   = 16 // GitHub の login は最大 39 文字だが、詳細の列を残すため長いものは切り詰める
)

// 現状はダーク背景のターミナル専用に固定色を使う（cc-dashboard と同じ方針）。
var (
	separatorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	faintStyle     = lipgloss.NewStyle().Faint(true)
	repoStyle      = lipgloss.NewStyle().Bold(true)
	textStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	openStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	draftStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	mergedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	closedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

// RenderBatches は全 Batch を区切り行付きで、新しいものを上にして描画する。
// batches は古い順で渡す。width が 0 以下なら行を切り詰めない。
func RenderBatches(batches []timeline.Batch, viewer string, width int) string {
	blocks := make([]string, 0, len(batches))
	for i := len(batches) - 1; i >= 0; i-- {
		var prev time.Time
		if i > 0 {
			prev = batches[i-1].CheckedAt
		}
		blocks = append(blocks, RenderBatch(batches[i], prev, viewer, width))
	}
	return strings.Join(blocks, "\n\n")
}

// RenderBatch は 1 回のポーリング分を "--- HH:MM ---" の区切り行に続けて、
// 1 イベント 1 行の "時刻 アクション ユーザー 詳細" の形で新しい順に描画する。
// prevCheckedAt は 1 つ前（時刻が古い側）の Batch の時刻で、日付が変わったときだけ区切り行に日付を出す。
func RenderBatch(b timeline.Batch, prevCheckedAt time.Time, viewer string, width int) string {
	rows := batchRows(b)

	// 時刻の列は、前日以前のイベント（-since を長く指定した初回など）を含む回だけ日付付きの幅にする。
	timeWidth := 0
	for _, r := range rows {
		timeWidth = max(timeWidth, len(formatEventTime(r.At, b.CheckedAt)))
	}

	lines := []string{separator(b.CheckedAt, prevCheckedAt)}
	for _, r := range rows {
		lines = append(lines, renderRow(r, b.CheckedAt, timeWidth, viewer))
	}
	if width > 0 {
		// viewport は長い行を折り返さずにはみ出させるため、表示幅で切り詰める。
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, width, "…")
		}
	}
	return strings.Join(lines, "\n")
}

func separator(checkedAt, prevCheckedAt time.Time) string {
	label := checkedAt.Format("15:04")
	if !prevCheckedAt.IsZero() && !sameDay(checkedAt, prevCheckedAt) {
		label = checkedAt.Format("01/02 15:04")
	}
	return separatorStyle.Render("--- " + label + " ---")
}

// row は表示する 1 行。どの PR / issue のイベントかを持つ。
type row struct {
	displayEvent
	Item github.Item
}

// batchRows は Batch 内の全 PR / issue のイベントを 1 列に並べ、新しい順にする。
func batchRows(b timeline.Batch) []row {
	var rows []row
	for _, e := range b.Entries {
		if len(e.Events) == 0 {
			// タイトル・本文の編集など、取得対象外の操作だけで更新された場合。誰の操作かは分からない。
			rows = append(rows, row{displayEvent: displayEvent{Event: github.Event{Type: eventUpdated, Actor: "-", At: e.Item.UpdatedAt}, Count: 1}, Item: e.Item})
			continue
		}
		for _, ev := range collapseEvents(e.Events) {
			rows = append(rows, row{displayEvent: ev, Item: e.Item})
		}
	}
	// 同じ分に記録されたイベント（PR 作成時のレビュー依頼とアサインなど）も新しい順にそろうよう、
	// 古い順に集めた rows を反転してから安定ソートする。
	slices.Reverse(rows)
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].At.After(rows[j].At)
	})
	return rows
}

// eventUpdated は個別のイベントを取得できなかった更新を表す、表示専用の種類。
const eventUpdated github.EventType = -1

func renderRow(r row, checkedAt time.Time, timeWidth int, viewer string) string {
	timeCell := lipgloss.NewStyle().Width(timeWidth).Render(formatEventTime(r.At, checkedAt))
	actionCell := lipgloss.NewStyle().Width(actionColWidth).Render(actionLabel(r.displayEvent))
	userCell := lipgloss.NewStyle().Width(userColWidth).Render(ansi.Truncate(r.Actor, userColWidth-1, "…"))
	detail := renderDetail(r)

	if r.Actor == viewer {
		// 自分の操作は、他の人の動きより目立たないようにする。
		// 隠さないのは、自分の push が CI の再実行やレビューの再依頼につながる場面もあり、
		// 時系列の文脈として残しておきたいため。
		return faintStyle.Render(strings.Join([]string{timeCell, actionCell, userCell, ansi.Strip(detail)}, " "))
	}
	return strings.Join([]string{
		faintStyle.Render(timeCell),
		eventStyle(r.Type).Render(actionCell),
		textStyle.Render(userCell),
		detail,
	}, " ")
}

// renderDetail は "PR owner/repo#123 → bob, carol Title" の形で、どの PR / issue かを描画する。
// 種別ラベルは PR / issue の状態（open / draft / merged / closed）の色で塗る。
func renderDetail(r row) string {
	item := r.Item
	kind := "IS"
	if item.Kind == github.KindPullRequest {
		kind = "PR"
	}
	parts := []string{
		itemStateStyle(item).Render(kind),
		repoStyle.Render(fmt.Sprintf("%s#%d", item.Repo, item.Number)),
	}
	// 依頼先・アサイン先はタイトルより前に置く。長いタイトルが切り詰められても残るようにするため。
	if r.Type == github.EventReviewRequested || r.Type == github.EventAssigned {
		// 依頼先のアカウントが削除済みなどで取れなかった分は出さない
		targets := slices.DeleteFunc(slices.Clone(r.Targets), func(t string) bool { return t == "" })
		if len(targets) > 0 {
			parts = append(parts, textStyle.Render("→ "+strings.Join(targets, ", ")))
		}
	}
	parts = append(parts, textStyle.Render(item.Title))
	return strings.Join(parts, " ")
}

func itemStateStyle(item github.Item) lipgloss.Style {
	switch item.State {
	case "MERGED":
		return mergedStyle
	case "CLOSED":
		if item.Kind == github.KindIssue && item.StateReason == "COMPLETED" {
			// GitHub の表示に合わせ、完了で閉じた issue はマージ済み PR と同じ紫にする。
			return mergedStyle
		}
		return closedStyle
	}
	if item.IsDraft {
		return draftStyle
	}
	return openStyle
}

// displayEvent は表示用にまとめたイベント。
type displayEvent struct {
	github.Event
	Count   int      // 同じ人の連続したコメント・コミット・レビューをまとめた件数
	Targets []string // 同じ人の連続したレビュー依頼をまとめた依頼先
}

// collapseEvents は同じ人による連続した同種のイベントを 1 行にまとめる。
// PR 作成直後のレビュー依頼の連続や、bot の連続コメントで 1 件の PR が
// 画面を占有しないようにするため。まとめた分の時刻は最後のイベントのものにする。
func collapseEvents(events []github.Event) []displayEvent {
	var out []displayEvent
	for _, ev := range events {
		n := len(out)
		if n == 0 {
			out = append(out, newDisplayEvent(ev))
			continue
		}
		last := &out[n-1]
		// マージ時は GitHub が MergedEvent と ClosedEvent を同時に記録する。
		// "closed" まで出すとマージとは別に閉じられたように読めるため捨てる。
		if ev.Type == github.EventClosed && last.Type == github.EventMerged {
			continue
		}
		if ev.Actor != last.Actor || ev.Type != last.Type {
			out = append(out, newDisplayEvent(ev))
			continue
		}
		switch ev.Type {
		case github.EventCommented, github.EventCommitted, github.EventReviewed:
			last.Count++
			last.At = ev.At
		case github.EventReviewRequested:
			last.Targets = append(last.Targets, ev.Target)
			last.At = ev.At
		default:
			out = append(out, newDisplayEvent(ev))
		}
	}
	return out
}

func newDisplayEvent(ev github.Event) displayEvent {
	return displayEvent{Event: ev, Count: 1, Targets: []string{ev.Target}}
}

// actionLabel はアクションの列に出す文字列。actionColWidth に収まる長さにする。
func actionLabel(ev displayEvent) string {
	switch ev.Type {
	case github.EventCommented:
		return withCount("commented", ev.Count)
	case github.EventApproved:
		return "approved"
	case github.EventChangesRequested:
		return "changes requested"
	case github.EventReviewed:
		return withCount("reviewed", ev.Count)
	case github.EventCommitted:
		if ev.Count == 1 {
			return "pushed 1 commit"
		}
		return fmt.Sprintf("pushed %d commits", ev.Count)
	case github.EventMerged:
		return "merged"
	case github.EventClosed:
		return "closed"
	case github.EventReopened:
		return "reopened"
	case github.EventReviewRequested:
		return "review requested"
	case github.EventReadyForReview:
		return "ready for review"
	case github.EventConvertedToDraft:
		return "converted to draft"
	case github.EventForcePushed:
		return "force-pushed"
	case github.EventAssigned:
		return "assigned"
	}
	return "updated"
}

func withCount(verb string, count int) string {
	if count == 1 {
		return verb
	}
	return fmt.Sprintf("%s (%d)", verb, count)
}

func eventStyle(t github.EventType) lipgloss.Style {
	switch t {
	case github.EventApproved:
		return openStyle
	case github.EventChangesRequested, github.EventClosed:
		return closedStyle
	case github.EventMerged:
		return mergedStyle
	}
	return textStyle
}

// formatEventTime はイベント時刻を出す。区切り行と同じ日なら時刻だけ、
// 違う日（-since でさかのぼった分など）は日付も付ける。
func formatEventTime(at, checkedAt time.Time) string {
	at = at.In(checkedAt.Location())
	if sameDay(at, checkedAt) {
		return at.Format("15:04")
	}
	return at.Format("01/02 15:04")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.In(a.Location()).Date()
	return ay == by && am == bm && ad == bd
}
