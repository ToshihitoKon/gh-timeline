package github

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// pushedPRResponse は、コミット 3 件と、それより前のコメント 1 件を持つ PR の検索結果を返す。
// コミットは 01:00 台に作られ、updatedAt（push の時刻）は 03:00。
func pushedPRResponse(updatedAt string) string {
	return fmt.Sprintf(`{"data": {
	  "viewer": {"login": "me"},
	  "authored": {"issueCount": 1, "nodes": [
	    {"__typename": "PullRequest", "number": 1, "title": "Add feature", "url": "https://github.com/o/r/pull/1",
	     "state": "OPEN", "updatedAt": %q, "repository": {"nameWithOwner": "o/r"},
	     "headRefName": "feature/x", "headRepository": {"nameWithOwner": "fork/r"},
	     "timelineItems": {"nodes": [
	       {"__typename": "IssueComment", "createdAt": "2026-11-20T02:00:00Z", "author": {"login": "alice"}},
	       {"__typename": "PullRequestCommit", "commit": {"oid": "c1", "committedDate": "2026-11-20T01:00:00Z", "author": {"user": {"login": "me"}}}},
	       {"__typename": "PullRequestCommit", "commit": {"oid": "c2", "committedDate": "2026-11-20T01:10:00Z", "author": {"user": {"login": "me"}}}},
	       {"__typename": "PullRequestCommit", "commit": {"oid": "c3", "committedDate": "2026-11-20T01:20:00Z", "author": {"user": {"login": "me"}}}}
	     ]},
	     "reviews": {"nodes": []}}
	  ]},
	  "assigned": {"issueCount": 0, "nodes": []},
	  "reviewRequested": {"issueCount": 0, "nodes": []},
	  "reviewedBy": {"issueCount": 0, "nodes": []}
	}}`, updatedAt)
}

// activity API は新しい順に返す。c1 と c2 は 02:30 の push、c3 は 02:59 の push に含まれる。
const pushActivityResponse = `[
  {"after": "c3", "timestamp": "2026-11-20T02:59:00Z", "activity_type": "push"},
  {"after": "c2", "timestamp": "2026-11-20T02:30:00Z", "activity_type": "push"},
  {"after": "c0", "timestamp": "2026-11-20T00:00:00Z", "activity_type": "branch_creation"}
]`

// fakeGH は検索に searchResponse を、activity API に activityResponse を返し、
// activity API の呼び出しを記録する。
type fakeGH struct {
	searchResponse   string
	activityResponse string
	activityErr      error
	activityCalls    []string
}

func (f *fakeGH) run(_ context.Context, args ...string) ([]byte, error) {
	if args[1] == "graphql" {
		return []byte(f.searchResponse), nil
	}
	f.activityCalls = append(f.activityCalls, args[1])
	if f.activityErr != nil {
		return nil, f.activityErr
	}
	return []byte(f.activityResponse), nil
}

func commitTimes(t *testing.T, item Item) []string {
	t.Helper()
	var times []string
	for _, ev := range item.Events {
		if ev.Type == EventCommitted {
			times = append(times, ev.At.UTC().Format("15:04"))
		}
	}
	return times
}

func TestClientSearch_RetimesCommitsToPushTime(t *testing.T) {
	gh := &fakeGH{searchResponse: pushedPRResponse("2026-11-20T03:00:00Z"), activityResponse: pushActivityResponse}
	got, err := NewClient(gh.run, false).Search(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// fork からの PR では、push は fork 側のリポジトリに記録される。
	want := "repos/fork/r/activity?per_page=30&ref=refs%2Fheads%2Ffeature%2Fx"
	if len(gh.activityCalls) != 1 || gh.activityCalls[0] != want {
		t.Errorf("activity calls = %v, want [%s]", gh.activityCalls, want)
	}
	if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "02:30 02:30 02:59"; got != want {
		t.Errorf("commit times = %s, want %s", got, want)
	}
	if last := got.Items[0].Events[3]; last.Type != EventCommitted || !last.At.Equal(time.Date(2026, 11, 20, 2, 59, 0, 0, time.UTC)) {
		t.Errorf("last event = %+v, want the commit pushed at 02:59 (events re-sorted)", last)
	}
}

func TestClientSearch_TimesCommitsOfBranchCreationByThatPush(t *testing.T) {
	// git push -u で c1 と c2 を push してブランチを作り、あとから c3 だけを push した。
	activity := `[
	  {"after": "c3", "timestamp": "2026-11-20T02:59:00Z", "activity_type": "push"},
	  {"after": "c2", "timestamp": "2026-11-20T02:30:00Z", "activity_type": "branch_creation"}
	]`
	gh := &fakeGH{searchResponse: pushedPRResponse("2026-11-20T03:00:00Z"), activityResponse: activity}
	got, err := NewClient(gh.run, false).Search(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "02:30 02:30 02:59"; got != want {
		t.Errorf("commit times = %s, want %s", got, want)
	}
}

func TestClientSearch_SkipsPushWhoseHeadIsNotInPR(t *testing.T) {
	// 02:45 の push の head は、その後の force push で PR から消えたコミット。
	// この push にはコミットを割り当てず、c2 は c3 と同じ 02:59 の push に含まれたとみなす。
	activity := `[
	  {"after": "c3", "timestamp": "2026-11-20T02:59:00Z", "activity_type": "force_push"},
	  {"after": "gone", "timestamp": "2026-11-20T02:45:00Z", "activity_type": "push"},
	  {"after": "c1", "timestamp": "2026-11-20T02:30:00Z", "activity_type": "branch_creation"}
	]`
	gh := &fakeGH{searchResponse: pushedPRResponse("2026-11-20T03:00:00Z"), activityResponse: activity}
	got, err := NewClient(gh.run, false).Search(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "02:30 02:59 02:59"; got != want {
		t.Errorf("commit times = %s, want %s", got, want)
	}
}

func TestClientSearch_RetimesFirstSeenPREvenWhenACommentFollowsThePush(t *testing.T) {
	// CI のボットが push の直後にコメントし、updatedAt がコメントの時刻になった。
	response := strings.Replace(pushedPRResponse("2026-11-20T03:00:00Z"), `"createdAt": "2026-11-20T02:00:00Z"`, `"createdAt": "2026-11-20T03:00:00Z"`, 1)
	gh := &fakeGH{searchResponse: response, activityResponse: pushActivityResponse}
	got, err := NewClient(gh.run, false).Search(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "02:30 02:30 02:59"; got != want {
		t.Errorf("commit times = %s, want push times %s", got, want)
	}
}

// pushTimeUnknownCommits は、push の時刻に置き換えられなかったコミットの ID を返す。
func pushTimeUnknownCommits(item Item) []string {
	var ids []string
	for _, ev := range item.Events {
		if ev.Type == EventCommitted && ev.IsPushTimeUnknown {
			ids = append(ids, ev.ID)
		}
	}
	return ids
}

func TestClientSearch_ReusesPushesWhileNewestCommitIsUnchanged(t *testing.T) {
	gh := &fakeGH{searchResponse: pushedPRResponse("2026-11-20T03:00:00Z"), activityResponse: pushActivityResponse}
	client := NewClient(gh.run, false)
	for poll := range 2 {
		got, err := client.Search(context.Background(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "02:30 02:30 02:59"; got != want {
			t.Errorf("poll %d: commit times = %s, want %s", poll, got, want)
		}
	}
	if len(gh.activityCalls) != 1 {
		t.Errorf("activity calls = %d, want 1 (cached while the head stays at c3)", len(gh.activityCalls))
	}
}

func TestClientSearch_RetimesCommitPushedLaterToShownPR(t *testing.T) {
	gh := &fakeGH{searchResponse: pushedPRResponse("2026-11-20T03:00:00Z"), activityResponse: pushActivityResponse}
	client := NewClient(gh.run, false)
	if _, err := client.Search(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}

	// PR を表示した後の 03:30 に、01:30 に手元でコミットしておいた c4 を push した。
	c3 := `"oid": "c3", "committedDate": "2026-11-20T01:20:00Z", "author": {"user": {"login": "me"}}}}`
	c4 := `, {"__typename": "PullRequestCommit", "commit": {"oid": "c4", "committedDate": "2026-11-20T01:30:00Z", "author": {"user": {"login": "me"}}}}`
	gh.searchResponse = strings.Replace(pushedPRResponse("2026-11-20T03:30:00Z"), c3, c3+c4, 1)
	gh.activityResponse = `[{"after": "c4", "timestamp": "2026-11-20T03:30:00Z", "activity_type": "push"},` + strings.TrimPrefix(pushActivityResponse, "[")
	got, err := client.Search(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(gh.activityCalls) != 2 {
		t.Errorf("activity calls = %d, want 2 (fetched again after the head moved to c4)", len(gh.activityCalls))
	}
	if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "02:30 02:30 02:59 03:30"; got != want {
		t.Errorf("commit times = %s, want %s", got, want)
	}
}

func TestClientSearch_ReportsAndMarksCommitsWhenActivityFails(t *testing.T) {
	gh := &fakeGH{searchResponse: pushedPRResponse("2026-11-20T03:00:00Z"), activityErr: errors.New("HTTP 404")}
	client := NewClient(gh.run, false)
	got, err := client.Search(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Search err = %v, want nil (activity failure does not fail the search)", err)
	}
	if want := []string{"push times unavailable for 1 pull request"}; !slices.Equal(got.PartialErrors, want) {
		t.Errorf("PartialErrors = %q, want %q", got.PartialErrors, want)
	}
	if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "01:00 01:10 01:20"; got != want {
		t.Errorf("commit times = %s, want committedDate %s", got, want)
	}
	if got, want := pushTimeUnknownCommits(got.Items[0]), []string{"c1", "c2", "c3"}; !slices.Equal(got, want) {
		t.Errorf("commits with unknown push time = %v, want %v", got, want)
	}

	// 失敗した結果はキャッシュせず、次の検索で取り直す。
	gh.activityErr, gh.activityResponse = nil, pushActivityResponse
	got, err = client.Search(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(gh.activityCalls) != 2 {
		t.Errorf("activity calls = %d, want 2", len(gh.activityCalls))
	}
	if len(got.PartialErrors) != 0 {
		t.Errorf("PartialErrors = %q, want none", got.PartialErrors)
	}
	if got := pushTimeUnknownCommits(got.Items[0]); len(got) != 0 {
		t.Errorf("commits with unknown push time = %v, want none", got)
	}
}

func TestClientSearch_RefetchesWhileNewestCommitIsMissingFromActivity(t *testing.T) {
	// c3 の push が、まだ activity API に反映されていない。
	activity := `[{"after": "c2", "timestamp": "2026-11-20T02:30:00Z", "activity_type": "branch_creation"}]`
	gh := &fakeGH{searchResponse: pushedPRResponse("2026-11-20T03:00:00Z"), activityResponse: activity}
	client := NewClient(gh.run, false)
	got, err := client.Search(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PartialErrors) != 0 {
		t.Errorf("PartialErrors = %q, want none (the fetch succeeded)", got.PartialErrors)
	}
	if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "01:20 02:30 02:30"; got != want {
		t.Errorf("commit times = %s, want %s (c3 keeps its committedDate)", got, want)
	}
	if got, want := pushTimeUnknownCommits(got.Items[0]), []string{"c3"}; !slices.Equal(got, want) {
		t.Errorf("commits with unknown push time = %v, want %v", got, want)
	}

	if _, err := client.Search(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(gh.activityCalls) != 2 {
		t.Errorf("activity calls = %d, want 2 (not cached until c3 is retimed)", len(gh.activityCalls))
	}
}

// prsResponse は、pushedPRResponse と同じコミットを持つ PR を n 件返す検索結果を作る。
func prsResponse(n int) string {
	nodes := make([]string, n)
	for i := range nodes {
		nodes[i] = fmt.Sprintf(`
	    {"__typename": "PullRequest", "number": %d, "title": "PR %d", "url": "https://github.com/o/r/pull/%d",
	     "state": "OPEN", "updatedAt": "2026-11-20T03:00:00Z", "repository": {"nameWithOwner": "o/r"},
	     "headRefName": "feature/%d", "headRepository": {"nameWithOwner": "o/r"},
	     "timelineItems": {"nodes": [
	       {"__typename": "PullRequestCommit", "commit": {"oid": "c1", "committedDate": "2026-11-20T01:00:00Z", "author": {"user": {"login": "me"}}}},
	       {"__typename": "PullRequestCommit", "commit": {"oid": "c2", "committedDate": "2026-11-20T01:10:00Z", "author": {"user": {"login": "me"}}}},
	       {"__typename": "PullRequestCommit", "commit": {"oid": "c3", "committedDate": "2026-11-20T01:20:00Z", "author": {"user": {"login": "me"}}}}
	     ]},
	     "reviews": {"nodes": []}}`, i+1, i+1, i+1, i+1)
	}
	return fmt.Sprintf(`{"data": {
	  "viewer": {"login": "me"},
	  "authored": {"issueCount": %d, "nodes": [%s]},
	  "assigned": {"issueCount": 0, "nodes": []},
	  "reviewRequested": {"issueCount": 0, "nodes": []},
	  "reviewedBy": {"issueCount": 0, "nodes": []}
	}}`, n, strings.Join(nodes, ","))
}

// blockingGH は activity API の呼び出しを release が閉じられるまで止め、同時に進行中だった呼び出しの最大数を記録する。
// 進行中の呼び出しが pushFetchConcurrency に達したら reachedLimit を閉じる。
type blockingGH struct {
	searchResponse string
	release        chan struct{}
	reachedLimit   chan struct{}

	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	calls       int
}

func newBlockingGH(searchResponse string) *blockingGH {
	return &blockingGH{searchResponse: searchResponse, release: make(chan struct{}), reachedLimit: make(chan struct{})}
}

func (g *blockingGH) run(_ context.Context, args ...string) ([]byte, error) {
	if args[1] == "graphql" {
		return []byte(g.searchResponse), nil
	}
	g.mu.Lock()
	g.calls++
	g.inFlight++
	g.maxInFlight = max(g.maxInFlight, g.inFlight)
	if g.inFlight == pushFetchConcurrency {
		select {
		case <-g.reachedLimit:
		default:
			close(g.reachedLimit)
		}
	}
	g.mu.Unlock()

	<-g.release

	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
	return []byte(pushActivityResponse), nil
}

func TestClientSearch_FetchesPushesInParallelUpToLimit(t *testing.T) {
	numPRs := pushFetchConcurrency*2 + 1
	gh := newBlockingGH(prsResponse(numPRs))

	var got SearchResult
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, err = NewClient(gh.run, false).Search(context.Background(), time.Now())
	}()

	select {
	case <-gh.reachedLimit:
	case <-time.After(5 * time.Second):
		t.Fatalf("activity calls never reached %d at once, want them run in parallel", pushFetchConcurrency)
	}
	close(gh.release)
	<-done

	if err != nil {
		t.Fatal(err)
	}
	if gh.maxInFlight > pushFetchConcurrency {
		t.Errorf("max concurrent activity calls = %d, want at most %d", gh.maxInFlight, pushFetchConcurrency)
	}
	if gh.calls != numPRs {
		t.Errorf("activity calls = %d, want %d (one per PR)", gh.calls, numPRs)
	}
	for _, item := range got.Items {
		if got, want := strings.Join(commitTimes(t, item), " "), "02:30 02:30 02:59"; got != want {
			t.Errorf("%s commit times = %s, want %s", item.Key(), got, want)
		}
	}
}
