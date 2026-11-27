package main

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
)

func buildInfoWithVersion(v string) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Path: "github.com/ToshihitoKon/gh-timeline", Version: v}}
}

func TestPickVersion_ReleaseAndGoInstallPrintTheSame(t *testing.T) {
	release := pickVersion("v0.4.1", nil)
	goInstall := pickVersion("dev", buildInfoWithVersion("v0.4.1"))
	if release != "v0.4.1" || goInstall != "v0.4.1" {
		t.Fatalf("release = %q, go install = %q, want both v0.4.1", release, goInstall)
	}
}

func TestPickVersion_PrefersEmbeddedVersion(t *testing.T) {
	if got := pickVersion("v0.4.1", buildInfoWithVersion("v0.3.0")); got != "v0.4.1" {
		t.Fatalf("pickVersion() = %q, want v0.4.1", got)
	}
}

func TestPickVersion_FallsBackToDev(t *testing.T) {
	for name, info := range map[string]*debug.BuildInfo{
		"no build info":  nil,
		"empty version":  buildInfoWithVersion(""),
		"local checkout": buildInfoWithVersion("(devel)"),
	} {
		if got := pickVersion("dev", info); got != "dev" {
			t.Errorf("%s: pickVersion() = %q, want dev", name, got)
		}
	}
}

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

func TestCheckFlags_RejectsIntervalUnderOneMinute(t *testing.T) {
	err := checkFlags(30*time.Second, 30*time.Minute)
	if err == nil || !strings.Contains(err.Error(), "at least 1m0s") {
		t.Fatalf("checkFlags(30s) = %v, want an error saying at least 1m0s", err)
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

func (hangingFetcher) Search(ctx context.Context, _ time.Time) (github.SearchResult, error) {
	<-ctx.Done()
	return github.SearchResult{}, errors.New("signal: killed")
}

func TestRunDump_GivesUpAfterTimeout(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		done <- runDump(hangingFetcher{}, timeline.NewTracker(time.Now().Add(-30*time.Minute)), false, 10*time.Millisecond)
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
