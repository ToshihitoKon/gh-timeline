package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
)

// maxEventsPerEntry は 1 つの PR / issue について表示するイベントの上限。
// -since を長く指定した初回のポーリングや、bot が短時間に連投した PR で
// 1 件が画面を埋めないよう、直近の分だけに絞る。
const maxEventsPerEntry = 5

// eventIndent はイベント行の字下げ。PR / issue の行の種別ラベル（"PR " / "IS "）と同じ幅にして、
// イベントの時刻がリポジトリ名の先頭にそろうようにする。
const eventIndent = "   "

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

// RenderBatch は 1 回のポーリング分を "--- HH:MM ---" の区切り行に続けて描画する。
// prevCheckedAt は 1 つ前（時刻が古い側）の Batch の時刻で、日付が変わったときだけ区切り行に日付を出す。
func RenderBatch(b timeline.Batch, prevCheckedAt time.Time, viewer string, width int) string {
	lines := []string{separator(b.CheckedAt, prevCheckedAt)}
	for _, e := range b.Entries {
		lines = append(lines, renderEntry(e, b.CheckedAt, viewer)...)
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

func renderEntry(e timeline.Entry, checkedAt time.Time, viewer string) []string {
	item := e.Item
	isSelfOnly := e.IsOnlyBy(viewer)

	kind := "IS"
	if item.Kind == github.KindPullRequest {
		kind = "PR"
	}
	stateLabel, stateStyle := itemState(item)
	head := strings.Join([]string{
		stateStyle.Render(kind),
		repoStyle.Render(fmt.Sprintf("%s#%d", item.Repo, item.Number)),
		stateStyle.Render(stateLabel),
		textStyle.Render(item.Title),
		faintStyle.Render("(" + strings.Join(item.Roles.Labels(), ", ") + ")"),
	}, " ")
	if isSelfOnly {
		// 自分の操作だけによる更新は、他の人の動きより目立たないようにする。
		// 隠さないのは、自分の push が CI の再実行やレビューの再依頼につながる場面もあり、
		// 時系列の文脈として残しておきたいため。
		head = faintStyle.Render(ansi.Strip(head))
	}

	lines := []string{head}
	if len(e.Events) == 0 {
		return append(lines, eventIndent+faintStyle.Render(formatEventTime(item.UpdatedAt, checkedAt)+"  updated"))
	}

	events := collapseEvents(e.Events)
	if hidden := len(events) - maxEventsPerEntry; hidden > 0 {
		lines = append(lines, eventIndent+faintStyle.Render(fmt.Sprintf("… %d earlier", hidden)))
		events = events[hidden:]
	}
	for _, ev := range events {
		lines = append(lines, eventIndent+renderEvent(ev, checkedAt, viewer))
	}
	return lines
}

// itemState は PR / issue の状態ラベルと色を返す。
func itemState(item github.Item) (string, lipgloss.Style) {
	switch item.State {
	case "MERGED":
		return "merged", mergedStyle
	case "CLOSED":
		if item.Kind == github.KindIssue && item.StateReason == "COMPLETED" {
			// GitHub の表示に合わせ、完了で閉じた issue はマージ済み PR と同じ紫にする。
			return "closed", mergedStyle
		}
		return "closed", closedStyle
	}
	if item.IsDraft {
		return "draft", draftStyle
	}
	return "open", openStyle
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

func renderEvent(ev displayEvent, checkedAt time.Time, viewer string) string {
	at := formatEventTime(ev.At, checkedAt)
	text := ev.Actor + " " + describeEvent(ev)
	if ev.Actor == viewer {
		return faintStyle.Render(at + "  " + text)
	}
	return faintStyle.Render(at) + "  " + eventStyle(ev.Type).Render(text)
}

func describeEvent(ev displayEvent) string {
	switch ev.Type {
	case github.EventCommented:
		return withCount("commented", ev.Count)
	case github.EventApproved:
		return "approved"
	case github.EventChangesRequested:
		return "requested changes"
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
		return "requested review from " + strings.Join(ev.Targets, ", ")
	case github.EventReadyForReview:
		return "marked as ready for review"
	case github.EventConvertedToDraft:
		return "converted to draft"
	case github.EventForcePushed:
		return "force-pushed"
	case github.EventAssigned:
		return "assigned " + ev.Target
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
// 違う日（初回の lookback でさかのぼった分など）は日付も付ける。
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
