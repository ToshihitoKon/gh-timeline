package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
)

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
