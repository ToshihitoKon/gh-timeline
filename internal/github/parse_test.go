package github

import (
	"slices"
	"testing"
	"time"
)

func TestConvertEvent_ActorOnlyTypes(t *testing.T) {
	at := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		typename string
		want     EventType
	}{
		{"MergedEvent", EventMerged},
		{"ClosedEvent", EventClosed},
		{"ReopenedEvent", EventReopened},
		{"ReadyForReviewEvent", EventReadyForReview},
		{"ConvertToDraftEvent", EventConvertedToDraft},
		{"HeadRefForcePushedEvent", EventForcePushed},
	}
	for _, tt := range tests {
		got, ok := convertEvent(rawEvent{Typename: tt.typename, CreatedAt: at, Actor: &rawActor{Login: "alice"}})
		want := Event{Type: tt.want, Actor: "alice", At: at}
		if !ok || got != want {
			t.Errorf("convertEvent(%s) = %+v, %v; want %+v, true", tt.typename, got, ok, want)
		}
	}
}

func TestConvertEvent_CrossReferenceKeepsSource(t *testing.T) {
	at := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	ref := func(typename, repo string, number int) *rawRef {
		r := &rawRef{Typename: typename, Number: number}
		r.Repository.NameWithOwner = repo
		return r
	}
	tests := []struct {
		name   string
		source *rawRef
		want   *Ref
	}{
		{"pull request", ref("PullRequest", "o/r", 12), &Ref{Kind: KindPullRequest, Repo: "o/r", Number: 12}},
		{"issue", ref("Issue", "x/y", 3), &Ref{Kind: KindIssue, Repo: "x/y", Number: 3}},
		{"control characters", ref("Issue", "x/y\u001b[2J", 3), &Ref{Kind: KindIssue, Repo: "x/y[2J", Number: 3}},
		{"empty node", ref("", "", 0), nil},
		{"null", nil, nil},
	}
	for _, tt := range tests {
		got, ok := convertEvent(rawEvent{Typename: "CrossReferencedEvent", ID: "CRE_1", CreatedAt: at, Actor: &rawActor{Login: "alice"}, Source: tt.source})
		if !ok || got.Type != EventReferenced || got.ID != "CRE_1" || got.Actor != "alice" || !got.At.Equal(at) {
			t.Errorf("%s: convertEvent = %+v, %v; want referenced by alice at %v", tt.name, got, ok, at)
		}
		if (got.Source == nil) != (tt.want == nil) || (got.Source != nil && *got.Source != *tt.want) {
			t.Errorf("%s: Source = %+v, want %+v", tt.name, got.Source, tt.want)
		}
	}
}

func TestConvertEvent_SkipsUnknownType(t *testing.T) {
	if got, ok := convertEvent(rawEvent{Typename: "LabeledEvent"}); ok {
		t.Errorf("convertEvent(LabeledEvent) = %+v, true; want false", got)
	}
}

func TestDropClosedByMerge_DropsClosedRecordedWithMerge(t *testing.T) {
	mergedAt := time.Date(2026, 11, 27, 15, 20, 33, 0, time.UTC)
	tests := []struct {
		name       string
		closedAt   time.Time
		shouldKeep bool
	}{
		{"same time as merge", mergedAt, false},
		{"one second after merge", mergedAt.Add(time.Second), false},
		{"at the end of the window", mergedAt.Add(closedByMergeWindow), false},
		{"after the window", mergedAt.Add(closedByMergeWindow + time.Second), true},
		{"closed before merge and reopened", mergedAt.Add(-time.Hour), true},
	}
	for _, tt := range tests {
		events := []Event{
			{Type: EventMerged, Actor: "alice", At: mergedAt},
			{Type: EventClosed, Actor: "alice", At: tt.closedAt},
		}
		sortByTime(events)
		got := dropClosedByMerge(events)
		hasClosed := slices.ContainsFunc(got, func(ev Event) bool { return ev.Type == EventClosed })
		if hasClosed != tt.shouldKeep {
			t.Errorf("%s: closed kept = %v, want %v (events %+v)", tt.name, hasClosed, tt.shouldKeep, got)
		}
		if !slices.ContainsFunc(got, func(ev Event) bool { return ev.Type == EventMerged }) {
			t.Errorf("%s: merged dropped: %+v", tt.name, got)
		}
	}
}

func TestStripControl_ReplacesWhitespaceControlsWithSpace(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Fix\tlogin", "Fix login"},
		{"Fix\nlogin", "Fix login"},
		{"Fix\r\nlogin", "Fix  login"},
		{"Fix\x1b[31mlogin\x7f", "Fix[31mlogin"}, // 空白類以外の制御文字は除く
		{"\v\f", ""},                             // 空白類でも \t \n \r 以外は除く
	}
	for _, tt := range tests {
		if got := stripControl(tt.in); got != tt.want {
			t.Errorf("stripControl(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
