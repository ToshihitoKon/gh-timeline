package main

import (
	"bytes"
	"context"
	"fmt"
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
	if want := "-interval must be at least 1m0s"; err == nil || err.Error() != want {
		t.Fatalf("checkFlags() = %v, want %q", err, want)
	}
}

func TestCheckFlags_RejectsNegativeSince(t *testing.T) {
	err := checkFlags(5*time.Minute, -time.Minute)
	if err == nil || !strings.Contains(err.Error(), "-since") {
		t.Fatalf("checkFlags() = %v, want a -since error", err)
	}
}

var dumpStartedAt = time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)

// fakeFetcher は決まった検索結果を返す。
type fakeFetcher struct{ result github.SearchResult }

func (f fakeFetcher) Search(context.Context, time.Time) (github.SearchResult, error) {
	return f.result, nil
}

// hangingFetcher は GitHub API の応答が戻らない状況を模し、ctx が終わるまで返らない。
// 返すエラーは、打ち切られたときの github.Client と同じ形にする。
type hangingFetcher struct{}

func (hangingFetcher) Search(ctx context.Context, _ time.Time) (github.SearchResult, error) {
	<-ctx.Done()
	return github.SearchResult{}, fmt.Errorf("GitHub API: timed out: %w", ctx.Err())
}

// dump は -since 30m の初回ポーリングとして runDump を実行し、stdout と stderr に出したものを返す。
func dump(t *testing.T, fetcher timeline.Fetcher, timeout time.Duration) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	tracker := timeline.NewTracker(dumpStartedAt.Add(-30 * time.Minute))
	err = runDump(&outBuf, &errBuf, fetcher, tracker, dumpStartedAt, false, timeout)
	return outBuf.String(), errBuf.String(), err
}

func commentedPR(number int, title string, actor string, at time.Time) github.Item {
	return github.Item{
		Kind:      github.KindPullRequest,
		Repo:      "owner/repo",
		Number:    number,
		Title:     title,
		State:     "OPEN",
		UpdatedAt: at,
		Events:    []github.Event{{ID: fmt.Sprintf("c%d", number), Type: github.EventCommented, Actor: actor, At: at}},
	}
}

func TestRunDump_PrintsUpdatesAndWarnings(t *testing.T) {
	stdout, stderr, err := dump(t, fakeFetcher{result: github.SearchResult{
		Viewer: "me",
		Items: []github.Item{
			commentedPR(1, "Fix typo", "alice", dumpStartedAt.Add(-10*time.Minute)),
			commentedPR(2, "Add tests", "me", dumpStartedAt.Add(-20*time.Minute)),
		},
		Truncated:     true,
		PartialErrors: []string{"Resource protected by organization SAML enforcement.", "push times unavailable for 1 pull request"},
	}}, time.Minute)
	if err != nil {
		t.Fatalf("runDump() error = %v", err)
	}

	wantStdout := "--- 14:00 ---\n" +
		"owner/repo PR#1 Fix typo\n" +
		"  13:50 commented alice\n" +
		"owner/repo PR#2 Add tests\n" +
		"  13:40 commented me\n" +
		fmt.Sprintf("(some results were truncated: over %d hits)\n", github.MaxSearchHits)
	if stdout != wantStdout {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, wantStdout)
	}
	wantStderr := "gh-timeline: some results are missing: Resource protected by organization SAML enforcement.; push times unavailable for 1 pull request\n"
	if stderr != wantStderr {
		t.Errorf("stderr = %q, want %q", stderr, wantStderr)
	}
}

func TestRunDump_ReportsTruncationWithNoUpdates(t *testing.T) {
	stdout, stderr, err := dump(t, fakeFetcher{result: github.SearchResult{Viewer: "me", Truncated: true}}, time.Minute)
	if err != nil {
		t.Fatalf("runDump() error = %v", err)
	}

	wantStdout := "No updates since 11/20 13:30.\n" +
		fmt.Sprintf("(some results were truncated: over %d hits)\n", github.MaxSearchHits)
	if stdout != wantStdout {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, wantStdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestRunDump_NoUpdatesWithoutWarnings(t *testing.T) {
	stdout, stderr, err := dump(t, fakeFetcher{result: github.SearchResult{Viewer: "me"}}, time.Minute)
	if err != nil {
		t.Fatalf("runDump() error = %v", err)
	}
	if want := "No updates since 11/20 13:30.\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestRunDump_GivesUpAfterTimeout(t *testing.T) {
	type dumpResult struct {
		stdout, stderr string
		err            error
	}
	done := make(chan dumpResult, 1)
	go func() {
		stdout, stderr, err := dump(t, hangingFetcher{}, 10*time.Millisecond)
		done <- dumpResult{stdout, stderr, err}
	}()

	select {
	case got := <-done:
		// github.Client が時間切れと書いたエラーを、包み直さずにそのまま返す
		if want := "GitHub API: timed out: context deadline exceeded"; got.err == nil || got.err.Error() != want {
			t.Fatalf("runDump() = %v, want %q", got.err, want)
		}
		if got.stdout != "" || got.stderr != "" {
			t.Errorf("stdout = %q, stderr = %q, want nothing printed", got.stdout, got.stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runDump did not return after its timeout")
	}
}
