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

// 現状はダーク背景のターミナル専用に固定色を使う（cc-dashboard と同じ方針）。
// 行は列幅をそろえずに詰めて並べるため、要素の切れ目が色で分かるよう要素ごとに色を変える。
var (
	separatorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	faintStyle     = lipgloss.NewStyle().Faint(true)
	repoStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))  // 青
	actionStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // 橙。承認・マージなどは eventStyle で別の色にする
	// シアン。桃系（213 など）は 16 色の端末でマージ済みの紫と同じマゼンタに丸められ、見分けられなくなる。
	userStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	textStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	openStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	draftStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	mergedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	closedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

	// open の PR 番号や approved と同じ緑にする。28 のような暗い緑は黒い背景に沈んで見落とすため。
	// 印・丸・罫線は左端の欄にしか出ないので、同じ色でも位置で見分けられる。
	unreadStyle = openStyle
	// 既読の丸と罫線は本文より後ろに下げたいので、draft の 245 より暗い灰色にする。
	readGraphStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// 左端の欄は「新着の印・空白・丸か罫線・空白」の 4 桁。
// 新着の印は右半分を塗る ▐ にする。▎ では細くて見落とし、█ では塗りが重く、
// ▌ では端末の左端に張り付いて窮屈に見えるため。
// 未読と既読を印の有無という形の違いで見分けられるようにし、色を出せない端末でも区別できるようにする。
const (
	unreadMarker = "▐"
	itemMarker   = "●"
	eventMarker  = "│"
)

// lineKind は描画した 1 行の種類。左端の欄に何を出すかが種類で変わる。
type lineKind int

const (
	lineSeparator lineKind = iota
	lineItemHeader
	lineURL
	lineEvent
)

type batchLine struct {
	kind lineKind
	text string
}

// RenderOptions は Batch の描画のしかた。
type RenderOptions struct {
	// Viewer は自分のログイン名。自分の操作を薄く出すために使う。
	Viewer string
	// Width は行を切り詰める表示幅。0 以下なら切り詰めない。
	Width int
	// ShowURL は見出し行の次に PR / issue の URL の行を出すか。
	ShowURL bool
	// UseHyperlinks は見出し行と URL の行を OSC 8 のリンクにするか。
	// -dump の出力をパイプで受けた側に、見えないエスケープシーケンスを混ぜないよう、TUI だけで使う。
	UseHyperlinks bool
}

// RenderBatches は全 Batch を区切り行付きで、新しいものを上にして描画する。
// batches は古い順で渡し、先頭から readCount 件を既読、それより新しいものを未読として
// 各行の左端の欄に印・丸・罫線を付ける。
func RenderBatches(batches []timeline.Batch, readCount int, opts RenderOptions) string {
	blocks := make([]string, 0, len(batches))
	for i := len(batches) - 1; i >= 0; i-- {
		var prev time.Time
		if i > 0 {
			prev = batches[i-1].CheckedAt
		}
		isUnread := i >= readCount
		var lines []string
		for _, l := range batchLines(batches[i], prev, opts) {
			prefix := gutter(l.kind, isUnread)
			bodyWidth := opts.Width
			if opts.Width > 0 {
				// 欄より狭い端末でも本文は 1 桁残す。0 にすると truncate が「切り詰めない」と扱うため。
				bodyWidth = max(opts.Width-ansi.StringWidth(prefix), 1)
			}
			lines = append(lines, prefix+truncate(l.text, bodyWidth))
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}

// gutter は左端の欄を返す。区切り行は丸と罫線の列から書き始め、まとまりの始まりが
// PR / issue の見出しより一段外に出て見えるようにする。
func gutter(kind lineKind, isUnread bool) string {
	newMark, graphStyle := " ", readGraphStyle
	if isUnread {
		newMark, graphStyle = unreadStyle.Render(unreadMarker), unreadStyle
	}
	switch kind {
	case lineItemHeader:
		return newMark + " " + graphStyle.Render(itemMarker) + " "
	case lineURL, lineEvent:
		return newMark + " " + graphStyle.Render(eventMarker) + " "
	}
	return newMark + " "
}

// RenderBatch は 1 回のポーリング分を "--- HH:MM ---" の区切り行に続けて、PR / issue ごとに
// "owner/repo PR#123 タイトル" の見出し行と、字下げした "時刻 アクション ユーザー" のイベント行で描画する。
// PR / issue は最新のイベントが新しい順、イベントも新しい順に並べる。
// prevCheckedAt は 1 つ前（時刻が古い側）の Batch の時刻で、日付が変わったときだけ区切り行に日付を出す。
func RenderBatch(b timeline.Batch, prevCheckedAt time.Time, opts RenderOptions) string {
	var lines []string
	for _, l := range batchLines(b, prevCheckedAt, opts) {
		lines = append(lines, truncate(l.text, opts.Width))
	}
	return strings.Join(lines, "\n")
}

func batchLines(b timeline.Batch, prevCheckedAt time.Time, opts RenderOptions) []batchLine {
	lines := []batchLine{{kind: lineSeparator, text: separator(b.CheckedAt, prevCheckedAt)}}
	for _, blk := range batchBlocks(b) {
		url := blk.Item.URL
		isLinked := opts.UseHyperlinks && url != ""
		var linkURL string
		if isLinked {
			linkURL = url
		}
		lines = append(lines, batchLine{kind: lineItemHeader, text: renderItemHeader(blk, opts.Viewer, linkURL)})
		if opts.ShowURL && url != "" {
			urlText := faintStyle.Underline(isLinked).Render(url)
			lines = append(lines, batchLine{kind: lineURL, text: eventIndent + linkIf(isLinked, urlText, url)})
		}
		for _, ev := range blk.Events {
			lines = append(lines, batchLine{kind: lineEvent, text: renderEventLine(ev, b.CheckedAt, opts.Viewer)})
		}
	}
	return lines
}

// linkIf は isLinked のとき text を url への OSC 8 のリンクで囲む。
// 切り詰めで本文が切れても ansi.Truncate はエスケープシーケンスを残すので、リンクは閉じたままになる。
func linkIf(isLinked bool, text, url string) string {
	if !isLinked {
		return text
	}
	return ansi.SetHyperlink(url) + text + ansi.ResetHyperlink()
}

// truncate は viewport が長い行を折り返さずにはみ出させるため、表示幅で切り詰める。
// width が 0 以下（-dump など端末幅が無いとき）は切り詰めない。
func truncate(line string, width int) string {
	if width <= 0 {
		return line
	}
	return ansi.Truncate(line, width, "…")
}

func separator(checkedAt, prevCheckedAt time.Time) string {
	label := checkedAt.Format("15:04")
	if !prevCheckedAt.IsZero() && !sameDay(checkedAt, prevCheckedAt) {
		label = checkedAt.Format("01/02 15:04")
	}
	return separatorStyle.Render("--- " + label + " ---")
}

// itemBlock は 1 件の PR / issue と、表示するイベント（新しい順、1 件以上）。
type itemBlock struct {
	Item   github.Item
	Events []displayEvent
}

// batchBlocks は Batch 内のイベントを PR / issue ごとにまとめ、先頭のイベントが新しい順に並べる。
func batchBlocks(b timeline.Batch) []itemBlock {
	blocks := make([]itemBlock, 0, len(b.Entries))
	for _, e := range b.Entries {
		blocks = append(blocks, itemBlock{Item: e.Item, Events: entryEvents(e)})
	}
	// Entries は updatedAt も含めた時刻で並んでいるため、見えているイベントの時刻で並べ直す。
	sort.SliceStable(blocks, func(i, j int) bool {
		return blocks[i].Events[0].At.After(blocks[j].Events[0].At)
	})
	return blocks
}

func entryEvents(e timeline.Entry) []displayEvent {
	if len(e.Events) == 0 {
		// タイトル・本文の編集など、取得対象外の操作だけで更新された場合。誰の操作かは分からない。
		return []displayEvent{{Event: github.Event{Type: eventUpdated, Actor: "-", At: e.Item.UpdatedAt}, Count: 1}}
	}
	events := collapseEvents(e.Events)
	// 同じ分に記録されたイベント（PR 作成時のレビュー依頼とアサインなど）も新しい順にそろうよう、
	// 古い順のイベントを反転してから安定ソートする。
	slices.Reverse(events)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].At.After(events[j].At)
	})
	return events
}

// eventUpdated は個別のイベントを取得できなかった更新を表す、表示専用の種類。
const eventUpdated github.EventType = -1

// eventIndent はイベント行の字下げ。見出し行と見分けるため。
const eventIndent = "  "

// renderItemHeader は "owner/repo PR#123 Fix typo" の見出し行を描画する。
// PR#123 / IS#45 の部分は PR / issue の状態（open / draft / merged / closed）の色で塗る。
// linkURL が空でなければ "owner/repo PR#123" の部分を linkURL へのリンクにして下線を引く。
// タイトルまで含めると、すべての見出しに長い下線が付いて画面がうるさくなるため、タイトルは含めない。
func renderItemHeader(blk itemBlock, viewer, linkURL string) string {
	item := blk.Item
	kind := "IS"
	if item.Kind == github.KindPullRequest {
		kind = "PR"
	}
	repoSt, numberSt, gapSt, titleSt := repoStyle, itemStateStyle(item).Bold(true), lipgloss.NewStyle(), textStyle
	isOnlyViewer := !slices.ContainsFunc(blk.Events, func(ev displayEvent) bool { return ev.Actor != viewer })
	if isOnlyViewer {
		repoSt, numberSt, gapSt, titleSt = faintStyle, faintStyle, faintStyle, faintStyle
	}
	isLinked := linkURL != ""
	ref := repoSt.Underline(isLinked).Render(item.Repo) +
		gapSt.Underline(isLinked).Render(" ") +
		numberSt.Underline(isLinked).Render(fmt.Sprintf("%s#%d", kind, item.Number))
	return linkIf(isLinked, ref, linkURL) + gapSt.Render(" ") + titleSt.Render(item.Title)
}

// renderEventLine は "  14:01 review requested dave → you, erin" のイベント行を描画する。
func renderEventLine(ev displayEvent, checkedAt time.Time, viewer string) string {
	parts := []string{
		faintStyle.Render(formatEventTime(ev.At, checkedAt)),
		eventStyle(ev.Type).Render(actionLabel(ev)),
		userStyle.Render(ev.Actor),
	}
	if ev.Type == github.EventReviewRequested || ev.Type == github.EventAssigned {
		// 依頼先のアカウントが削除済みなどで取れなかった分は出さない
		targets := slices.DeleteFunc(slices.Clone(ev.Targets), func(t string) bool { return t == "" })
		if len(targets) > 0 {
			parts = append(parts, faintStyle.Render("→")+" "+userStyle.Render(strings.Join(targets, ", ")))
		}
	}
	line := eventIndent + strings.Join(parts, " ")

	if ev.Actor == viewer {
		// 自分の操作は、他の人の動きより目立たないようにする。
		// 隠さないのは、自分の push が CI の再実行やレビューの再依頼につながる場面もあり、
		// 時系列の文脈として残しておきたいため。
		return faintStyle.Render(ansi.Strip(line))
	}
	return line
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

// actionLabel は行のアクション部分に出す文字列。
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
	return actionStyle
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
