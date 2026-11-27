package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
)

func TestCheckFlags_AcceptsDefaults(t *testing.T) {
	if err := checkFlags(5*time.Minute, 30*time.Minute); err != nil {
		t.Fatalf("checkFlags() = %v, want nil", err)
	}
}

func TestCheckFlags_AcceptsZeroSince(t *testing.T) {
	if err := checkFlags(minInterval, 0); err != nil {
		t.Fatalf("checkFlags() = %v, want nil", err)
	}
}

func TestCheckFlags_RejectsIntervalBelowMinimum(t *testing.T) {
	err := checkFlags(minInterval-time.Second, 30*time.Minute)
	if err == nil || !strings.Contains(err.Error(), "-interval") {
		t.Fatalf("checkFlags() = %v, want an -interval error", err)
	}
}

func TestCheckFlags_RejectsNegativeSince(t *testing.T) {
	err := checkFlags(5*time.Minute, -time.Minute)
	if err == nil || !strings.Contains(err.Error(), "-since") {
		t.Fatalf("checkFlags() = %v, want a -since error", err)
	}
}

// hangingFetcher は gh が戻らない状況を模し、ctx が終わるまで返らない。
type hangingFetcher struct{}

func (hangingFetcher) Search(ctx context.Context, _ time.Time, _ map[string]bool) (github.SearchResult, error) {
	<-ctx.Done()
	return github.SearchResult{}, errors.New("signal: killed")
}

func TestRunDump_GivesUpAfterTimeout(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		done <- runDump(hangingFetcher{}, timeline.NewTracker(30*time.Minute), false, 10*time.Millisecond)
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "gave up after 10ms") {
			t.Fatalf("runDump() = %v, want a timeout error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDump did not return after its timeout")
	}
}
