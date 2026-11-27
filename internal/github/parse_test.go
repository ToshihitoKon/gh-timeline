package github

import (
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

func TestConvertEvent_SkipsUnknownType(t *testing.T) {
	if got, ok := convertEvent(rawEvent{Typename: "LabeledEvent"}); ok {
		t.Errorf("convertEvent(LabeledEvent) = %+v, true; want false", got)
	}
}
