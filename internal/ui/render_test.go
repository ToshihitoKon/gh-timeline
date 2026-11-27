package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
)

var jst = time.FixedZone("JST", 9*60*60)

// timeAt はテストの基準日（2026-11-20 JST）の hh:mm を返す。
func timeAt(hh, mm int) time.Time {
	return time.Date(2026, 11, 20, hh, mm, 0, 0, jst)
}

func entry(events ...github.Event) timeline.Entry {
	return timeline.Entry{
		Item: github.Item{
			Kind: github.KindPullRequest, Repo: "o/r", Number: 7, Title: "Add feature",
			State: "OPEN", UpdatedAt: timeAt(14, 0),
		},
		Events: events,
	}
}

func ev(t github.EventType, actor string, hh, mm int) github.Event {
	return github.Event{Type: t, Actor: actor, At: timeAt(hh, mm)}
}

// renderBatch は 1 件の Batch を未読として描画する。opts.IsTUI が false なら -dump と同じく左端の欄を付けない。
func renderBatch(b timeline.Batch, opts RenderOptions) string {
	return RenderBatches([]timeline.Batch{b}, 0, opts)
}

func TestEventKinds_CoverAllEventTypes(t *testing.T) {
	for typ := range github.EventTypeCount {
		if k, ok := eventKinds[typ]; !ok || k.label == nil {
			t.Errorf("eventKinds has no entry for EventType %d", typ)
		}
	}
}

func TestEventKindOf_LabelsByKindAndCount(t *testing.T) {
	tests := []struct {
		typ   github.EventType
		count int
		want  string
	}{
		{github.EventCommented, 1, "commented"},
		{github.EventCommented, 3, "commented (3)"},
		{github.EventApproved, 1, "approved"},
		{github.EventChangesRequested, 1, "changes requested"},
		{github.EventReviewed, 2, "reviewed (2)"},
		{github.EventReplied, 2, "replied (2)"},
		{github.EventCommitted, 1, "pushed 1 commit"},
		{github.EventCommitted, 2, "pushed 2 commits"},
		{github.EventMerged, 1, "merged"},
		{github.EventClosed, 1, "closed"},
		{github.EventReopened, 1, "reopened"},
		{github.EventReviewRequested, 1, "review requested"},
		{github.EventReadyForReview, 1, "ready for review"},
		{github.EventConvertedToDraft, 1, "converted to draft"},
		{github.EventForcePushed, 1, "force-pushed"},
		{github.EventAssigned, 1, "assigned"},
		{github.EventReferenced, 1, "referenced"},
		{github.EventUpdated, 1, "updated"},
		{github.EventType(-1), 1, "updated"}, // 表に無い種類
	}
	for _, tt := range tests {
		if got := eventKindOf(tt.typ).label(tt.count); got != tt.want {
			t.Errorf("label of EventType %d with count %d = %q, want %q", tt.typ, tt.count, got, tt.want)
		}
	}
}

func TestRenderBatches_CollapsesOnlyCountedAndTargetedKinds(t *testing.T) {
	b := timeline.Batch{
		CheckedAt: timeAt(14, 5),
		Entries: []timeline.Entry{entry(
			ev(github.EventReviewed, "alice", 13, 0),
			ev(github.EventReviewed, "alice", 13, 1),
			ev(github.EventForcePushed, "bob", 13, 10),
			ev(github.EventForcePushed, "bob", 13, 11),
		)},
	}
	got := ansi.Strip(renderBatch(b, RenderOptions{Viewer: "me"}))
	want := strings.Join([]string{
		"--- 14:05 ---",
		"o/r PR#7 Add feature",
		"  13:11 force-pushed bob",
		"  13:10 force-pushed bob",
		"  13:01 reviewed (2) alice",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_CollapsedTargetsShownOnce(t *testing.T) {
	// 依頼、取り消し、再依頼を 1 回のポーリングの間に行うと、同じ依頼先へのイベントが続けて取れる
	request := ev(github.EventReviewRequested, "alice", 13, 0)
	request.Target = "bob"
	again := ev(github.EventReviewRequested, "alice", 13, 2)
	again.Target = "bob"
	b := timeline.Batch{
		CheckedAt: timeAt(14, 5),
		Entries:   []timeline.Entry{entry(request, again)},
	}
	got := ansi.Strip(renderBatch(b, RenderOptions{Viewer: "me"}))
	want := strings.Join([]string{
		"--- 14:05 ---",
		"o/r PR#7 Add feature",
		"  13:02 review requested alice → bob",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_ShowsReferenceSourceWithRepoOnlyFromOtherRepo(t *testing.T) {
	samePR := ev(github.EventReferenced, "alice", 13, 0)
	samePR.Source = &github.Ref{Kind: github.KindPullRequest, Repo: "o/r", Number: 9}
	otherIssue := ev(github.EventReferenced, "alice", 13, 1)
	otherIssue.Source = &github.Ref{Kind: github.KindIssue, Repo: "x/y", Number: 3}
	unknown := ev(github.EventReferenced, "bob", 13, 2) // 参照元を読めなかった
	b := timeline.Batch{
		CheckedAt: timeAt(14, 5),
		Entries:   []timeline.Entry{entry(samePR, otherIssue, unknown)},
	}
	got := ansi.Strip(renderBatch(b, RenderOptions{Viewer: "me"}))
	want := strings.Join([]string{
		"--- 14:05 ---",
		"o/r PR#7 Add feature",
		"  13:02 referenced bob",
		"  13:01 referenced in x/y IS#3 alice",
		"  13:00 referenced in PR#9 alice",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_ShowsTargetOfAnyKind(t *testing.T) {
	commented := ev(github.EventCommented, "alice", 13, 0)
	commented.Target = "bob"
	b := timeline.Batch{
		CheckedAt: timeAt(14, 5),
		Entries:   []timeline.Entry{entry(commented)},
	}
	got := ansi.Strip(renderBatch(b, RenderOptions{Viewer: "me"}))
	want := strings.Join([]string{
		"--- 14:05 ---",
		"o/r PR#7 Add feature",
		"  13:00 commented alice → bob",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_GroupsEventsByItemNewestFirst(t *testing.T) {
	other := entry(ev(github.EventCommented, "bob", 13, 55))
	other.Item.Kind = github.KindIssue
	other.Item.Number = 8
	other.Item.Title = "Bug"
	b := timeline.Batch{
		CheckedAt: timeAt(14, 5),
		Entries: []timeline.Entry{
			entry(
				ev(github.EventCommitted, "me", 13, 50),
				ev(github.EventCommitted, "me", 13, 51),
				ev(github.EventApproved, "alice", 14, 0),
			),
			other,
		},
	}
	got := ansi.Strip(renderBatch(b, RenderOptions{Viewer: "me"}))
	want := strings.Join([]string{
		"--- 14:05 ---",
		"o/r PR#7 Add feature", // 最新のイベントが新しい PR / issue が上
		"  14:00 approved alice",
		"  13:51 pushed 2 commits me",
		"o/r IS#8 Bug",
		"  13:55 commented bob",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_CollapsesNoisyEvents(t *testing.T) {
	withTarget := func(e github.Event, target string) github.Event {
		e.Target = target
		return e
	}
	request := ev(github.EventReviewRequested, "alice", 13, 0)
	assign := ev(github.EventAssigned, "alice", 13, 1)
	b := timeline.Batch{
		CheckedAt: timeAt(14, 5),
		Entries: []timeline.Entry{entry(
			withTarget(request, "bob"),
			withTarget(request, "carol"),
			withTarget(assign, "bob"),
			withTarget(assign, "carol"),
			ev(github.EventCommented, "bot", 13, 10),
			ev(github.EventCommented, "bot", 13, 11),
			ev(github.EventReplied, "dave", 13, 20),
			ev(github.EventReplied, "dave", 13, 21),
		)},
	}
	got := ansi.Strip(renderBatch(b, RenderOptions{Viewer: "me"}))
	want := strings.Join([]string{
		"--- 14:05 ---",
		"o/r PR#7 Add feature",
		"  13:21 replied (2) dave",
		"  13:11 commented (2) bot",
		"  13:01 assigned alice → bob, carol",
		"  13:00 review requested alice → bob, carol",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_DateShownWhenDayChanges(t *testing.T) {
	prev := time.Date(2026, 11, 19, 23, 58, 0, 0, jst)
	yesterday := github.Event{Type: github.EventCommented, Actor: "alice", At: time.Date(2026, 11, 19, 23, 59, 0, 0, jst)}
	updated := github.Event{Type: github.EventUpdated, At: timeAt(14, 0)}
	b := timeline.Batch{
		CheckedAt: timeAt(0, 3),
		Entries:   []timeline.Entry{entry(updated), entry(yesterday)},
	}
	got := ansi.Strip(RenderBatches([]timeline.Batch{{CheckedAt: prev}, b}, 0, RenderOptions{Viewer: "me"}))
	want := strings.Join([]string{
		"--- 11/20 00:03 ---",
		// 誰の操作か分からない EventUpdated は、行った人を "-" で出す
		"o/r PR#7 Add feature",
		"  14:00 updated -",
		"o/r PR#7 Add feature",
		"  11/19 23:59 commented alice",
		"",
		"--- 23:58 ---", // 最も古い Batch には比べる日付が無いので、日付を出さない
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_SameMinuteEventsNewestFirstAndEmptyTargetOmitted(t *testing.T) {
	requested := ev(github.EventReviewRequested, "alice", 13, 0) // 依頼先が取れなかった（Target が空）
	assigned := ev(github.EventAssigned, "alice", 13, 0)
	assigned.Target = "alice"
	b := timeline.Batch{
		CheckedAt: timeAt(14, 5),
		Entries:   []timeline.Entry{entry(requested, assigned)},
	}
	got := ansi.Strip(renderBatch(b, RenderOptions{Viewer: "me"}))
	want := strings.Join([]string{
		"--- 14:05 ---",
		"o/r PR#7 Add feature",
		"  13:00 assigned alice → alice",
		"  13:00 review requested alice",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_TruncatesToWidth(t *testing.T) {
	b := timeline.Batch{CheckedAt: timeAt(14, 5), Entries: []timeline.Entry{entry()}}
	for _, line := range strings.Split(renderBatch(b, RenderOptions{Viewer: "me", Width: 12}), "\n") {
		if w := ansi.StringWidth(line); w > 12 {
			t.Errorf("line %q width = %d, want <= 12", ansi.Strip(line), w)
		}
	}
}

const itemURL = "https://github.com/o/r/pull/7"

func entryWithURL(events ...github.Event) timeline.Entry {
	e := entry(events...)
	e.Item.URL = itemURL
	return e
}

func TestRenderBatches_ShowURLAddsURLLineUnderHeader(t *testing.T) {
	noURL := entry(ev(github.EventCommented, "bob", 13, 55))
	noURL.Item.Number = 8
	b := timeline.Batch{
		CheckedAt: timeAt(14, 5),
		Entries:   []timeline.Entry{entryWithURL(ev(github.EventApproved, "alice", 14, 0)), noURL},
	}
	got := ansi.Strip(renderBatch(b, RenderOptions{Viewer: "me", ShowURL: true}))
	want := strings.Join([]string{
		"--- 14:05 ---",
		"o/r PR#7 Add feature",
		"  " + itemURL,
		"  14:00 approved alice",
		"o/r PR#8 Add feature", // URL が取れなかった分は URL の行を出さない
		"  13:55 commented bob",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_WithoutShowURLHasNoURLLine(t *testing.T) {
	b := timeline.Batch{CheckedAt: timeAt(14, 5), Entries: []timeline.Entry{entryWithURL()}}
	if got := renderBatch(b, RenderOptions{Viewer: "me"}); strings.Contains(got, itemURL) {
		t.Errorf("RenderBatches contains the URL without ShowURL:\n%q", got)
	}
}

// splitHyperlink は line を、url へのリンクより前・リンクの中・リンクより後ろに分ける。
// リンクが無い、または閉じていなければ ok が false になる。
func splitHyperlink(line, url string) (before, inside, after string, ok bool) {
	before, rest, found := strings.Cut(line, ansi.SetHyperlink(url))
	if !found {
		return "", "", "", false
	}
	inside, after, found = strings.Cut(rest, ansi.ResetHyperlink())
	return before, inside, after, found
}

func TestRenderBatches_HyperlinksRepoAndNumber(t *testing.T) {
	for _, tc := range []struct {
		name  string
		actor string
	}{
		{"others acted", "alice"},
		// 自分の操作だけの見出しは色を落として薄くするので、それでもリンクが残ることを確かめる
		{"only viewer acted", "me"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := timeline.Batch{
				CheckedAt: timeAt(14, 5),
				Entries:   []timeline.Entry{entryWithURL(ev(github.EventCommented, tc.actor, 14, 0))},
			}
			lines := strings.Split(renderBatch(b, RenderOptions{Viewer: "me", ShowURL: true, IsTUI: true}), "\n")

			before, inside, after, ok := splitHyperlink(lines[1], itemURL)
			if !ok || ansi.Strip(before) != "▐ ● " || ansi.Strip(inside) != "o/r PR#7" || ansi.Strip(after) != " Add feature" {
				t.Errorf("header %q: want only %q linked to %s", lines[1], "o/r PR#7", itemURL)
			}
			// URL の行は欄と字下げを除いた URL の部分だけをリンクにする
			before, inside, after, ok = splitHyperlink(lines[2], itemURL)
			if !ok || ansi.Strip(before) != "▐ │ "+eventIndent || ansi.Strip(inside) != itemURL || after != "" {
				t.Errorf("URL line %q: want only the URL linked", lines[2])
			}
			if event := lines[3]; strings.Contains(event, "\x1b]8;") {
				t.Errorf("event line %q has a hyperlink", event)
			}
		})
	}
}

// underlineSGR は下線を含む SGR。lipgloss は下線を色や太字と 1 つの SGR にまとめて出す（\x1b[1;4;38;5;42m など）。
var underlineSGR = regexp.MustCompile(`\x1b\[(?:[0-9:]*;)*4(?:;[0-9:]*)*m`)

func TestRenderBatches_LinkedTextIsUnderlined(t *testing.T) {
	// テストの実行環境は色を出さない設定になり、下線も出なくなるため、色を出す設定に切り替える
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })

	b := timeline.Batch{
		CheckedAt: timeAt(14, 5),
		Entries:   []timeline.Entry{entryWithURL(ev(github.EventApproved, "alice", 14, 0))},
	}
	linked := strings.Split(renderBatch(b, RenderOptions{Viewer: "me", ShowURL: true, IsTUI: true}), "\n")
	for _, line := range linked[1:3] { // 見出し行と URL の行
		_, inside, after, ok := splitHyperlink(line, itemURL)
		if !ok || !underlineSGR.MatchString(inside) {
			t.Errorf("linked part of %q is not underlined", line)
		}
		// 見出しのタイトルなど、リンクの外には下線を引かない
		if underlineSGR.MatchString(after) {
			t.Errorf("text after the link in %q is underlined", line)
		}
	}
	if event := linked[3]; underlineSGR.MatchString(event) {
		t.Errorf("event line %q is underlined", event)
	}

	// -dump のようにリンクにしないときは、下線も引かない
	for _, line := range strings.Split(renderBatch(b, RenderOptions{Viewer: "me", ShowURL: true}), "\n") {
		if underlineSGR.MatchString(line) {
			t.Errorf("line %q is underlined without IsTUI", line)
		}
	}
}

func TestRenderBatches_NoHyperlinksUnlessRequested(t *testing.T) {
	b := timeline.Batch{CheckedAt: timeAt(14, 5), Entries: []timeline.Entry{entryWithURL()}}
	// -dump の出力にエスケープシーケンスを混ぜない
	if got := renderBatch(b, RenderOptions{Viewer: "me", ShowURL: true}); strings.Contains(got, "\x1b]8;") {
		t.Errorf("RenderBatches has a hyperlink without IsTUI:\n%q", got)
	}
}

func TestRenderBatches_TruncatedHeaderKeepsHyperlinkClosed(t *testing.T) {
	b := timeline.Batch{CheckedAt: timeAt(14, 5), Entries: []timeline.Entry{entryWithURL()}}
	got := RenderBatches([]timeline.Batch{b}, 1, RenderOptions{Viewer: "me", Width: 12, IsTUI: true})
	// 欄の 4 桁を除いた 8 桁に切り詰めると、リンクの途中（"o/r PR#…"）で切れる
	header := strings.Split(got, "\n")[1]
	if _, _, _, ok := splitHyperlink(header, itemURL); !ok {
		t.Errorf("truncated header %q does not keep the hyperlink closed", header)
	}
}

func batchAt(hh, mm int) timeline.Batch {
	return timeline.Batch{
		CheckedAt: timeAt(hh, mm),
		Entries:   []timeline.Entry{entry(ev(github.EventCommented, "alice", hh, mm-1))},
	}
}

func TestRenderBatches_NewestBatchOnTop(t *testing.T) {
	got := ansi.Strip(RenderBatches([]timeline.Batch{batchAt(11, 45), batchAt(11, 50)}, 0, RenderOptions{Viewer: "me", IsTUI: true}))
	second := strings.Index(got, "--- 11:45 ---")
	if !strings.HasPrefix(got, "▐ --- 11:50 ---") || second < 0 {
		t.Errorf("want 11:50 block first, got\n%s", got)
	}
}

func TestRenderBatches_GraphAndUnreadMarkerAtLeftEdge(t *testing.T) {
	newer := batchAt(11, 50)
	issue := entry(ev(github.EventCommented, "bob", 11, 47))
	issue.Item.Kind = github.KindIssue
	issue.Item.Number = 8
	issue.Item.Title = "Bug"
	newer.Entries = append(newer.Entries, issue)

	got := ansi.Strip(RenderBatches([]timeline.Batch{batchAt(11, 45), newer}, 1, RenderOptions{Viewer: "me", IsTUI: true}))
	want := strings.Join([]string{
		// 区切り行は丸と罫線の列から書き始め、新着の印の列には入らない
		"▐ --- 11:50 ---",
		"▐ ● o/r PR#7 Add feature",
		"▐ │   11:49 commented alice",
		"▐ ● o/r IS#8 Bug",
		"▐ │   11:47 commented bob",
		"",
		"  --- 11:45 ---",
		"  ● o/r PR#7 Add feature",
		"  │   11:44 commented alice",
	}, "\n")
	if got != want {
		t.Errorf("RenderBatches =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderBatches_TruncatesIncludingMarker(t *testing.T) {
	for _, width := range []int{1, 12} {
		for _, line := range strings.Split(RenderBatches([]timeline.Batch{batchAt(11, 45)}, 0, RenderOptions{Viewer: "me", Width: width, IsTUI: true}), "\n") {
			// 極端に狭い端末では、欄の 4 桁に加えて本文を 1 桁（省略記号）だけ残すため、その分まで許す
			if w := ansi.StringWidth(line); w > max(width, 5) {
				t.Errorf("width %d: line %q width = %d", width, ansi.Strip(line), w)
			}
		}
	}
}

func TestBatchRenderCache_RendersOnlyNewBatches(t *testing.T) {
	opts := RenderOptions{Viewer: "me", IsTUI: true}
	var c batchRenderCache
	c.render([]timeline.Batch{batchAt(11, 45)}, 0, opts)

	// 描画済みの Batch は中身を見直さないことを、中身を差し替えた Batch で確かめる
	changed := batchAt(11, 45)
	changed.Entries[0].Item.Title = "Changed"
	added := batchAt(11, 50)
	added.Entries[0].Item.Title = "Added"
	got := ansi.Strip(c.render([]timeline.Batch{changed, added}, 0, opts))

	if !strings.Contains(got, "Added") {
		t.Errorf("new batch not rendered:\n%s", got)
	}
	if strings.Contains(got, "Changed") || !strings.Contains(got, "Add feature") {
		t.Errorf("rendered batch was rendered again:\n%s", got)
	}
}

func TestBatchRenderCache_KeepsRenderedBatchesOnResizeAndMarkRead(t *testing.T) {
	opts := RenderOptions{Viewer: "me", Width: 80, IsTUI: true}
	var c batchRenderCache
	c.render([]timeline.Batch{batchAt(11, 45)}, 0, opts)

	// 描画済みの Batch は中身を見直さないことを、中身を差し替えた Batch で確かめる
	changed := batchAt(11, 45)
	changed.Entries[0].Item.Title = "Changed"
	batches := []timeline.Batch{changed}

	narrow := opts
	narrow.Width = 12
	resized := c.render(batches, 0, narrow)
	if got := ansi.Strip(resized); strings.Contains(got, "Changed") {
		t.Errorf("rendered batch was rendered again on resize:\n%s", got)
	}
	for _, line := range strings.Split(resized, "\n") {
		if w := ansi.StringWidth(line); w > narrow.Width {
			t.Errorf("line %q width = %d, want <= %d after resize", ansi.Strip(line), w, narrow.Width)
		}
	}

	read := ansi.Strip(c.render(batches, 1, opts))
	if strings.Contains(read, "Changed") {
		t.Errorf("rendered batch was rendered again on mark read:\n%s", read)
	}
	if strings.Contains(read, unreadMarker) {
		t.Errorf("unread marker remains after mark read:\n%s", read)
	}
}

func TestRenderBatches_ViewerEventLineIsOnlyFaint(t *testing.T) {
	// テストの実行環境は色を出さない設定になるため、色を出す設定に切り替える
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })

	request := ev(github.EventReviewRequested, "me", 14, 0)
	request.Target = "alice"
	b := timeline.Batch{CheckedAt: timeAt(14, 5), Entries: []timeline.Entry{entry(request)}}
	event := strings.Split(renderBatch(b, RenderOptions{Viewer: "me"}), "\n")[2]
	// 時刻・アクション・ユーザーごとの色を付けず、行全体を 1 つの faint で囲む
	if want := faintStyle.Render("  14:00 review requested me → alice"); event != want {
		t.Errorf("viewer's event line = %q, want %q", event, want)
	}
}

func TestBatchRenderCache_MatchesUncachedRenderWhenReadStateOrOptionsChange(t *testing.T) {
	batches := []timeline.Batch{batchAt(11, 40), batchAt(11, 45), batchAt(11, 50)}
	steps := []struct {
		name      string
		readCount int
		opts      RenderOptions
	}{
		{"all unread", 0, RenderOptions{Viewer: "me", Width: 80, IsTUI: true}},
		{"marked read", 3, RenderOptions{Viewer: "me", Width: 80, IsTUI: true}},
		{"resized", 3, RenderOptions{Viewer: "me", Width: 20, IsTUI: true}},
		{"URL shown", 3, RenderOptions{Viewer: "me", Width: 20, ShowURL: true, IsTUI: true}},
	}
	var c batchRenderCache
	for _, s := range steps {
		got := c.render(batches, s.readCount, s.opts)
		if want := RenderBatches(batches, s.readCount, s.opts); got != want {
			t.Errorf("%s: cached render =\n%s\nwant\n%s", s.name, got, want)
		}
	}
}
