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
