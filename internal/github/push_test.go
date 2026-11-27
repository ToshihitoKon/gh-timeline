package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	     "state": "OPEN", "updatedAt": %q, "author": {"login": "me"}, "repository": {"nameWithOwner": "o/r"},
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

func TestClientSearch_SkipsPushTimesWhenUpdateIsExplained(t *testing.T) {
	// updatedAt がコメントの時刻と同じなら、更新はコメントで説明がつく。
	gh := &fakeGH{searchResponse: pushedPRResponse("2026-11-20T02:00:00Z"), activityResponse: pushActivityResponse}
	got, err := NewClient(gh.run, false).Search(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(gh.activityCalls) != 0 {
		t.Errorf("activity calls = %v, want none", gh.activityCalls)
	}
	if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "01:00 01:10 01:20"; got != want {
		t.Errorf("commit times = %s, want committedDate %s", got, want)
	}
}

func TestClientSearch_KeepsCommittedDateWhenActivityFails(t *testing.T) {
	gh := &fakeGH{searchResponse: pushedPRResponse("2026-11-20T03:00:00Z"), activityErr: errors.New("HTTP 404")}
	got, err := NewClient(gh.run, false).Search(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Search err = %v, want nil (activity failure is ignored)", err)
	}
	if got, want := strings.Join(commitTimes(t, got.Items[0]), " "), "01:00 01:10 01:20"; got != want {
		t.Errorf("commit times = %s, want committedDate %s", got, want)
	}
}

func TestClientSearch_CachesPushesUntilPRLeavesResultsOrIsUpdated(t *testing.T) {
	gh := &fakeGH{activityResponse: pushActivityResponse}
	client := NewClient(gh.run, false)
	search := func(response string) Item {
		t.Helper()
		gh.searchResponse = response
		got, err := client.Search(context.Background(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Items) == 0 {
			return Item{}
		}
		return got.Items[0]
	}
	empty := `{"data": {"viewer": {"login": "me"}, "authored": {"nodes": []}, "assigned": {"nodes": []}, "reviewRequested": {"nodes": []}, "reviewedBy": {"nodes": []}}}`

	search(pushedPRResponse("2026-11-20T03:00:00Z"))
	item := search(pushedPRResponse("2026-11-20T03:00:00Z"))
	if len(gh.activityCalls) != 1 {
		t.Errorf("activity calls after same update = %d, want 1 (cached)", len(gh.activityCalls))
	}
	if got, want := strings.Join(commitTimes(t, item), " "), "02:30 02:30 02:59"; got != want {
		t.Errorf("commit times from cache = %s, want %s", got, want)
	}

	search(pushedPRResponse("2026-11-20T03:05:00Z"))
	if len(gh.activityCalls) != 2 {
		t.Errorf("activity calls after new update = %d, want 2", len(gh.activityCalls))
	}

	search(empty)
	search(pushedPRResponse("2026-11-20T03:05:00Z"))
	if len(gh.activityCalls) != 3 {
		t.Errorf("activity calls after PR left the results = %d, want 3 (cache dropped)", len(gh.activityCalls))
	}
}
