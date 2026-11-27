package ui

import (
	"fmt"
	"slices"
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
	actionStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // 橙。承認・マージなどは eventKinds で別の色にする
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
	lineKindCount
)

// batchLine は欄を付けず、切り詰める前の 1 行。
type batchLine struct {
	kind  lineKind
	text  string
	width int // text の表示幅
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
	// ShowGutter は各行の左端に、新着の印・丸・罫線の欄を付けるか。TUI だけで使う。
	ShowGutter bool
}

// RenderBatches は全 Batch を新しいものを上にして描画する。各 Batch は "--- HH:MM ---" の区切り行に続けて、
// PR / issue ごとに "owner/repo PR#123 タイトル" の見出し行と、字下げした "時刻 アクション ユーザー" のイベント行で描画する。
// PR / issue は最新のイベントが新しい順、イベントも新しい順に並べる。
// 区切り行には、1 つ前（時刻が古い側）の Batch と日付が変わったときだけ日付を出す。
// batches は古い順で渡し、ShowGutter のときは先頭から readCount 件を既読、それより新しいものを未読として欄に出す。
func RenderBatches(batches []timeline.Batch, readCount int, opts RenderOptions) string {
	var cache batchRenderCache
	return cache.render(batches, readCount, opts)
}

// batchRenderCache は Batch ごとに、欄を付けず切り詰める前の行を持ち、描画済みの Batch を描き直さずに済ませる。
// 欄と切り詰めは結合するときに付けるため、既読にしたときや端末幅を変えたときも描き直さない。
// Batch は追記されるだけで中身が変わらない前提で、batches[i] の行を blocks[i] に持つ。
//
// 保持する Batch の数に上限を設けるほうが単純だが、さかのぼって読める履歴が消えるため採らない。
type batchRenderCache struct {
	// bodyOpts は blocks を描画したときの RenderOptions。行の中身を変えない Width と ShowGutter は 0 にしておく。
	bodyOpts RenderOptions
	blocks   [][]batchLine
}

// render は RenderBatches と同じ結果を返す。
func (c *batchRenderCache) render(batches []timeline.Batch, readCount int, opts RenderOptions) string {
	bodyOpts := opts
	bodyOpts.Width, bodyOpts.ShowGutter = 0, false
	if bodyOpts != c.bodyOpts || len(c.blocks) > len(batches) {
		c.bodyOpts = bodyOpts
		c.blocks = nil
	}
	for i := len(c.blocks); i < len(batches); i++ {
		var prev time.Time
		if i > 0 {
			prev = batches[i-1].CheckedAt
		}
		c.blocks = append(c.blocks, batchLines(batches[i], prev, bodyOpts))
	}

	var gutters gutterSet
	if opts.ShowGutter {
		gutters = newGutterSet()
	}
	var sb strings.Builder
	for i := len(c.blocks) - 1; i >= 0; i-- {
		if i < len(c.blocks)-1 {
			sb.WriteString("\n\n")
		}
		isUnread := i >= readCount
		for j, l := range c.blocks[i] {
			if j > 0 {
				sb.WriteByte('\n')
			}
			g := gutters.cell(l.kind, isUnread)
			sb.WriteString(g.text)
			sb.WriteString(fitLine(l, opts.Width, g.width))
		}
	}
	return sb.String()
}

// fitLine は l を、端末幅 width から欄の幅 gutterWidth を除いた幅に切り詰める。
// 幅に収まる行は ansi.Truncate で読み直さずに返し、描き直すたびに全履歴を走査しないようにする。
func fitLine(l batchLine, width, gutterWidth int) string {
	if width <= 0 {
		return l.text
	}
	// 欄より狭い端末でも本文は 1 桁残す。0 にすると truncate が「切り詰めない」と扱うため。
	bodyWidth := max(width-gutterWidth, 1)
	if l.width <= bodyWidth {
		return l.text
	}
	return truncate(l.text, bodyWidth)
}

// gutterCell は描画済みの左端の欄と、その表示幅。
type gutterCell struct {
	text  string
	width int
}

// gutterSet は左端の欄を、既読かどうかと行の種類の組み合わせごとに描画しておいたもの。
// 欄の形はこの組み合わせの数しかないため、行ごとに描画して幅を測らずに済む。
// ゼロ値は欄を付けないときに使い、どの行にも空の欄を返す。
type gutterSet struct {
	read, unread [lineKindCount]gutterCell
}

// newGutterSet は欄を描画する。パッケージ変数として一度だけ作らないのは、
// lipgloss のカラープロファイルを後から切り替えても（テストなど）追従させるため。描画するのは 8 通りだけで、行数によらない。
func newGutterSet() gutterSet {
	var s gutterSet
	for kind := range lineKindCount {
		s.read[kind] = newGutterCell(gutter(kind, false))
		s.unread[kind] = newGutterCell(gutter(kind, true))
	}
	return s
}

func newGutterCell(text string) gutterCell {
	return gutterCell{text: text, width: ansi.StringWidth(text)}
}

func (s *gutterSet) cell(kind lineKind, isUnread bool) gutterCell {
	if isUnread {
		return s.unread[kind]
	}
	return s.read[kind]
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

func batchLines(b timeline.Batch, prevCheckedAt time.Time, opts RenderOptions) []batchLine {
	lines := []batchLine{newBatchLine(lineSeparator, separator(b.CheckedAt, prevCheckedAt))}
	for _, blk := range batchBlocks(b) {
		var linkURL string
		if opts.UseHyperlinks {
			linkURL = blk.Item.URL
		}
		lines = append(lines, newBatchLine(lineItemHeader, renderItemHeader(blk, opts.Viewer, linkURL)))
		if url := blk.Item.URL; opts.ShowURL && url != "" {
			urlText := faintStyle.Underline(linkURL != "").Render(url)
			lines = append(lines, newBatchLine(lineURL, eventIndent+hyperlink(urlText, linkURL)))
		}
		for _, ev := range blk.Events {
			lines = append(lines, newBatchLine(lineEvent, renderEventLine(ev, blk.Item.Repo, b.CheckedAt, opts.Viewer)))
		}
	}
	return lines
}

func newBatchLine(kind lineKind, text string) batchLine {
	return batchLine{kind: kind, text: text, width: ansi.StringWidth(text)}
}

// hyperlink は text を url への OSC 8 のリンクで囲む。url が空なら text をそのまま返す。
// 切り詰めで本文が切れても ansi.Truncate はエスケープシーケンスを残すので、リンクは閉じたままになる。
func hyperlink(text, url string) string {
	if url == "" {
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
	label := checkedAt.Format(clockLayout)
	if !prevCheckedAt.IsZero() {
		label = formatTimeOnDayOf(checkedAt, prevCheckedAt)
	}
	return separatorStyle.Render("--- " + label + " ---")
}

// itemBlock は 1 件の PR / issue と、表示するイベント（新しい順、1 件以上）。
type itemBlock struct {
	Item   github.Item
	Events []displayEvent
}

// batchBlocks は Batch 内のイベントを PR / issue ごとにまとめる。並びは Entries の順のまま。
func batchBlocks(b timeline.Batch) []itemBlock {
	blocks := make([]itemBlock, 0, len(b.Entries))
	for _, e := range b.Entries {
		blocks = append(blocks, itemBlock{Item: e.Item, Events: entryEvents(e)})
	}
	return blocks
}

func entryEvents(e timeline.Entry) []displayEvent {
	events := collapseEvents(e.Events)
	// 並べ替えずに反転だけで新しい順になるのは、e.Events が古い順で、collapseEvents がまとめた行に
	// 最後のイベントの時刻を使うため。同じ分のイベント（PR 作成時のレビュー依頼とアサインなど）も新しい順になる。
	slices.Reverse(events)
	return events
}

// eventIndent はイベント行の字下げ。見出し行と見分けるため。
const eventIndent = "  "

// renderItemHeader は "owner/repo PR#123 Fix typo" の見出し行を描画する。
// PR#123 / IS#45 の部分は PR / issue の状態（open / draft / merged / closed）の色で塗る。
// linkURL が空でなければ "owner/repo PR#123" の部分を linkURL へのリンクにして下線を引く。
// タイトルまで含めると、すべての見出しに長い下線が付いて画面がうるさくなるため、タイトルは含めない。
func renderItemHeader(blk itemBlock, viewer, linkURL string) string {
	item := blk.Item
	repoSt, numberSt, gapSt, titleSt := repoStyle, itemStateStyle(item).Bold(true), lipgloss.NewStyle(), textStyle
	isOnlyViewer := !slices.ContainsFunc(blk.Events, func(ev displayEvent) bool { return ev.Actor != viewer })
	if isOnlyViewer {
		repoSt, numberSt, gapSt, titleSt = faintStyle, faintStyle, faintStyle, faintStyle
	}
	isLinked := linkURL != ""
	ref := repoSt.Underline(isLinked).Render(item.Repo) +
		gapSt.Underline(isLinked).Render(" ") +
		numberSt.Underline(isLinked).Render(numberLabel(item.Kind, item.Number))
	return hyperlink(ref, linkURL) + gapSt.Render(" ") + titleSt.Render(item.Title)
}

// numberLabel は "PR#123" / "IS#45" を返す。
func numberLabel(kind github.Kind, number int) string {
	prefix := "IS"
	if kind == github.KindPullRequest {
		prefix = "PR"
	}
	return fmt.Sprintf("%s#%d", prefix, number)
}

// refLabel は ref を、itemRepo と同じリポジトリなら "PR#123"、違えば "owner/repo PR#123" と書く。
func refLabel(ref github.Ref, itemRepo string) string {
	label := numberLabel(ref.Kind, ref.Number)
	if ref.Repo == itemRepo {
		return label
	}
	return ref.Repo + " " + label
}

// renderEventLine は "  14:01 review requested dave → you, erin" や "  14:01 referenced in PR#12 dave" のイベント行を描画する。
// itemRepo はイベントが起きた PR / issue のリポジトリで、参照元と同じリポジトリならリポジトリ名を省く。
func renderEventLine(ev displayEvent, itemRepo string, checkedAt time.Time, viewer string) string {
	// 自分の操作は、他の人の動きより目立たないよう、要素ごとの色を付けずに行全体を薄くする。
	// 隠さないのは、自分の push が CI の再実行やレビューの再依頼につながる場面もあり、
	// 時系列の文脈として残しておきたいため。
	isViewer := ev.Actor == viewer
	render := func(st lipgloss.Style, s string) string {
		if isViewer {
			return s
		}
		return st.Render(s)
	}

	kind := eventKindOf(ev.Type)
	parts := []string{
		render(faintStyle, formatTimeOnDayOf(ev.At, checkedAt)),
		render(kind.style, kind.label(ev.Count)),
	}
	if ev.Source != nil {
		parts = append(parts, render(kind.style, "in")+" "+render(repoStyle, refLabel(*ev.Source, itemRepo)))
	}
	parts = append(parts, render(userStyle, ev.Actor))
	if len(ev.Targets) > 0 {
		parts = append(parts, render(faintStyle, "→")+" "+render(userStyle, strings.Join(ev.Targets, ", ")))
	}
	line := eventIndent + strings.Join(parts, " ")
	if isViewer {
		return faintStyle.Render(line)
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
	Targets []string // 同じ人の連続したレビュー依頼・アサインをまとめた依頼先。空の依頼先は含めない
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
		if ev.Actor != last.Actor || ev.Type != last.Type {
			out = append(out, newDisplayEvent(ev))
			continue
		}
		switch eventKindOf(ev.Type).collapse {
		case collapseByCount:
			last.Count++
			last.At = ev.At
		case collapseByTargets:
			last.Targets = appendTarget(last.Targets, ev.Target)
			last.At = ev.At
		default:
			out = append(out, newDisplayEvent(ev))
		}
	}
	return out
}

// unknownActor は、誰の操作か分からないイベント（EventUpdated）の行に出す行った人。
// 空のまま出すと、行った人の欄が抜けた行に見えるため。
const unknownActor = "-"

func newDisplayEvent(ev github.Event) displayEvent {
	if ev.Actor == "" {
		ev.Actor = unknownActor
	}
	return displayEvent{Event: ev, Count: 1, Targets: appendTarget(nil, ev.Target)}
}

// appendTarget は依頼先を足す。依頼先のアカウントが削除済みなどで取れなかった分（空文字列）と、
// 依頼の取り消しと再依頼で重なった分は足さない。
func appendTarget(targets []string, target string) []string {
	if target == "" || slices.Contains(targets, target) {
		return targets
	}
	return append(targets, target)
}

// collapseMode は同じ人の連続した同種のイベントを 1 行にまとめるときのまとめ方。
type collapseMode int

const (
	collapseNone      collapseMode = iota
	collapseByCount                // 件数を数える
	collapseByTargets              // 依頼先を並べる
)

// eventKind はイベントの種類ごとの表示のしかた。
type eventKind struct {
	label    func(count int) string // 行のアクション部分。count はまとめた件数
	style    lipgloss.Style
	collapse collapseMode
}

var eventKinds = map[github.EventType]eventKind{
	github.EventCommented:        {label: countedLabel("commented"), style: actionStyle, collapse: collapseByCount},
	github.EventApproved:         {label: fixedLabel("approved"), style: openStyle},
	github.EventChangesRequested: {label: fixedLabel("changes requested"), style: closedStyle},
	github.EventReviewed:         {label: countedLabel("reviewed"), style: actionStyle, collapse: collapseByCount},
	github.EventReplied:          {label: countedLabel("replied"), style: actionStyle, collapse: collapseByCount},
	github.EventCommitted:        {label: pushedLabel, style: actionStyle, collapse: collapseByCount},
	github.EventMerged:           {label: fixedLabel("merged"), style: mergedStyle},
	github.EventClosed:           {label: fixedLabel("closed"), style: closedStyle},
	github.EventReopened:         {label: fixedLabel("reopened"), style: actionStyle},
	github.EventReviewRequested:  {label: fixedLabel("review requested"), style: actionStyle, collapse: collapseByTargets},
	github.EventReadyForReview:   {label: fixedLabel("ready for review"), style: actionStyle},
	github.EventConvertedToDraft: {label: fixedLabel("converted to draft"), style: actionStyle},
	github.EventForcePushed:      {label: fixedLabel("force-pushed"), style: actionStyle},
	github.EventAssigned:         {label: fixedLabel("assigned"), style: actionStyle, collapse: collapseByTargets},
	github.EventReferenced:       {label: fixedLabel("referenced"), style: actionStyle},
	github.EventUpdated:          {label: fixedLabel("updated"), style: actionStyle},
}

// eventKindOf は種類の表示のしかたを返す。表に無い種類は updated として出す。
// 表への書き忘れは TestEventKinds_CoverAllEventTypes で見つけ、実行時に TUI を落とさないよう panic しない。
func eventKindOf(t github.EventType) eventKind {
	if k, ok := eventKinds[t]; ok {
		return k
	}
	return eventKinds[github.EventUpdated]
}

func fixedLabel(label string) func(int) string {
	return func(int) string { return label }
}

func countedLabel(verb string) func(int) string {
	return func(count int) string {
		if count == 1 {
			return verb
		}
		return fmt.Sprintf("%s (%d)", verb, count)
	}
}

func pushedLabel(count int) string {
	if count == 1 {
		return "pushed 1 commit"
	}
	return fmt.Sprintf("pushed %d commits", count)
}

const (
	clockLayout     = "15:04"
	dateClockLayout = "01/02 15:04"
)

// formatTimeOnDayOf は t を ref のタイムゾーンで書く。ref と同じ日なら時刻だけ、
// 違う日（-since でさかのぼった分や、日付をまたいだ区切り行など）は日付も付ける。
func formatTimeOnDayOf(t, ref time.Time) string {
	t = t.In(ref.Location())
	ty, tm, td := t.Date()
	ry, rm, rd := ref.Date()
	if ty == ry && tm == rm && td == rd {
		return t.Format(clockLayout)
	}
	return t.Format(dateClockLayout)
}

// NoUpdatesText は、検索範囲の始点 since から更新が無かったときの案内。
func NoUpdatesText(since time.Time) string {
	return fmt.Sprintf("No updates since %s.", since.Format(dateClockLayout))
}
