package timeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
)

// recordingFetcher は渡された検索範囲の始点を覚え、ctx が終わるまで待つかどうかを選べる。
type recordingFetcher struct {
	gotSince  time.Time
	isHanging bool
}

func (f *recordingFetcher) Search(ctx context.Context, since time.Time) (github.SearchResult, error) {
	f.gotSince = since
	if f.isHanging {
		<-ctx.Done()
		return github.SearchResult{}, ctx.Err()
	}
	return github.SearchResult{}, nil
}

func TestStartPoll_UsesTrackerSince(t *testing.T) {
	tr := NewTracker(at(-30))
	tr.Apply(tr.StartPoll(at(0)), nil)

	p := tr.StartPoll(at(5))
	if !p.StartedAt.Equal(at(5)) || !p.Since.Equal(tr.Since()) {
		t.Errorf("StartPoll = %+v, want StartedAt %v and Since %v", p, at(5), tr.Since())
	}
}

func TestPollFetch_SearchesFromSince(t *testing.T) {
	f := &recordingFetcher{}
	p := Poll{StartedAt: at(0), Since: at(-30)}
	if _, err := p.Fetch(f, time.Minute); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !f.gotSince.Equal(at(-30)) {
		t.Errorf("searched since %v, want %v", f.gotSince, at(-30))
	}
}

func TestPollFetch_GivesUpAfterTimeout(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := Poll{}.Fetch(&recordingFetcher{isHanging: true}, 10*time.Millisecond)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Fetch() = %v, want a deadline error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Fetch did not return after its timeout")
	}
}

func TestTrackerApply_UsesSinceOfPoll(t *testing.T) {
	tr := NewTracker(at(-30))
	p := tr.StartPoll(at(0))
	// p を始めてから Apply までに tracker の検索範囲が進んでも、p の結果は p の検索範囲で突き合わせる
	tr.Apply(tr.StartPoll(at(30)), nil)

	batch := tr.Apply(p, []github.Item{pr(1, -20, comment("c1", "alice", -20))})
	if len(batch.Entries) != 1 || len(batch.Entries[0].Events) != 1 {
		t.Errorf("entries = %+v, want the comment at -20m within p's range", batch.Entries)
	}
}

func TestMissingResultsWarning(t *testing.T) {
	if got := MissingResultsWarning(github.SearchResult{}); got != "" {
		t.Errorf("without partial errors = %q, want empty", got)
	}
	got := MissingResultsWarning(github.SearchResult{PartialErrors: []string{"a", "b"}})
	if want := "some results are missing: a; b"; got != want {
		t.Errorf("MissingResultsWarning() = %q, want %q", got, want)
	}
}

func TestTruncatedWarning(t *testing.T) {
	if got := TruncatedWarning(github.SearchResult{}); got != "" {
		t.Errorf("without truncation = %q, want empty", got)
	}
	got := TruncatedWarning(github.SearchResult{Truncated: true})
	if want := fmt.Sprintf("some results were truncated: over %d hits", github.MaxSearchHits); got != want {
		t.Errorf("TruncatedWarning() = %q, want %q", got, want)
	}
}
