package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const sampleResponse = `{
  "data": {
    "viewer": {"login": "me"},
    "authored": {"issueCount": 1, "nodes": [
      {"__typename": "PullRequest", "number": 1, "title": "Add feature", "url": "https://github.com/o/r/pull/1",
       "state": "OPEN", "isDraft": false, "updatedAt": "2026-11-20T03:00:00Z",
       "author": {"login": "me"}, "repository": {"nameWithOwner": "o/r"},
       "timelineItems": {"nodes": [
         {"__typename": "IssueComment", "createdAt": "2026-11-20T02:10:00Z", "author": null},
         {"__typename": "PullRequestCommit", "commit": {"committedDate": "2026-11-20T02:40:00Z", "author": {"name": "Bob B", "user": null}}}
       ]},
       "reviews": {"nodes": [
         {"__typename": "PullRequestReview", "submittedAt": "2026-11-20T02:30:00Z", "state": "APPROVED", "body": "", "author": {"login": "alice"},
          "comments": {"totalCount": 0, "nodes": []}},
         {"__typename": "PullRequestReview", "submittedAt": "2026-11-20T02:45:00Z", "state": "COMMENTED", "body": "", "author": {"login": "dave"},
          "comments": {"totalCount": 1, "nodes": [{"replyTo": {"id": "c1"}}]}},
         {"__typename": "PullRequestReview", "submittedAt": null, "state": "PENDING", "body": "", "author": {"login": "me"},
          "comments": {"totalCount": 1, "nodes": [{"replyTo": {"id": "c1"}}]}},
         {"__typename": "PullRequestReview", "submittedAt": "2026-11-20T02:51:00Z", "state": "COMMENTED", "body": "", "author": {"login": "erin"},
          "comments": {"totalCount": 2, "nodes": [{"replyTo": {"id": "c1"}}, {"replyTo": null}]}},
         {"__typename": "PullRequestReview", "submittedAt": "2026-11-20T02:52:00Z", "state": "COMMENTED", "body": "see inline", "author": {"login": "frank"},
          "comments": {"totalCount": 1, "nodes": [{"replyTo": {"id": "c1"}}]}}
       ]}}
    ]},
    "assigned": {"issueCount": 1, "nodes": [
      {"__typename": "Issue", "number": 2, "title": "Bug", "url": "https://github.com/o/r/issues/2",
       "state": "CLOSED", "stateReason": "COMPLETED", "updatedAt": "2026-11-20T04:00:00Z",
       "author": {"login": "carol"}, "repository": {"nameWithOwner": "o/r"},
       "timelineItems": {"nodes": [
         {"__typename": "AssignedEvent", "createdAt": "2026-11-20T03:30:00Z", "actor": {"login": "carol"}, "assignee": {"login": "me"}},
         {"__typename": "ClosedEvent", "createdAt": "2026-11-20T03:50:00Z", "actor": {"login": "carol"}}
       ]}}
    ]},
    "reviewRequested": {"issueCount": 0, "nodes": []},
    "reviewedBy": {"issueCount": 150, "nodes": [
      {"__typename": "PullRequest", "number": 1, "title": "Add feature", "url": "https://github.com/o/r/pull/1",
       "state": "OPEN", "isDraft": false, "updatedAt": "2026-11-20T03:00:00Z",
       "author": {"login": "me"}, "repository": {"nameWithOwner": "o/r"},
       "timelineItems": {"nodes": []}},
      {}
    ]}
  }
}`

func TestParseResponse_SetsEventIDsWithCommitOID(t *testing.T) {
	body := `{"data": {
	  "viewer": {"login": "me"},
	  "authored": {"issueCount": 1, "nodes": [
	    {"__typename": "PullRequest", "number": 1, "updatedAt": "2026-11-20T03:00:00Z", "repository": {"nameWithOwner": "o/r"},
	     "timelineItems": {"nodes": [
	       {"__typename": "IssueComment", "id": "IC_1", "createdAt": "2026-11-20T02:00:00Z", "author": {"login": "alice"}},
	       {"__typename": "PullRequestCommit", "id": "PURC_1", "commit": {"oid": "abc123", "committedDate": "2026-11-20T02:10:00Z", "author": {"user": {"login": "me"}}}},
	       {"__typename": "ClosedEvent", "id": "CE_1", "createdAt": "2026-11-20T02:30:00Z", "actor": {"login": "me"}}
	     ]},
	     "reviews": {"nodes": [
	       {"__typename": "PullRequestReview", "id": "PRR_1", "submittedAt": "2026-11-20T02:20:00Z", "state": "APPROVED", "author": {"login": "bob"}}
	     ]}}
	  ]},
	  "assigned": {"nodes": []}, "reviewRequested": {"nodes": []}, "reviewedBy": {"nodes": []}
	}}`
	got, err := parseResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, ev := range got.Items[0].Events {
		ids = append(ids, ev.ID)
	}
	// コミットは PullRequestCommit の node id ではなく oid
	if got, want := strings.Join(ids, " "), "IC_1 abc123 PRR_1 CE_1"; got != want {
		t.Errorf("event IDs = %s, want %s", got, want)
	}
}

func TestParseResponse_MergesRolesAndSortsByUpdatedAt(t *testing.T) {
	got, err := parseResponse([]byte(sampleResponse))
	if err != nil {
		t.Fatal(err)
	}
	if got.Viewer != "me" {
		t.Errorf("Viewer = %q, want me", got.Viewer)
	}
	if !got.Truncated {
		t.Error("Truncated = false, want true (issueCount 150 > nodes)")
	}
	if len(got.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2 (duplicate PR merged, empty node skipped)", len(got.Items))
	}
	if got.Items[0].Key() != "o/r#2" || got.Items[1].Key() != "o/r#1" {
		t.Errorf("order = %s, %s; want o/r#2 (newer) first", got.Items[0].Key(), got.Items[1].Key())
	}
	if pr := got.Items[1]; pr.Roles != RoleAuthor|RoleReviewed {
		t.Errorf("PR roles = %b, want author|reviewed", pr.Roles)
	}
}

func TestParseResponse_ConvertsEventsInTimeOrder(t *testing.T) {
	got, err := parseResponse([]byte(sampleResponse))
	if err != nil {
		t.Fatal(err)
	}
	events := got.Items[1].Events
	want := []struct {
		typ   EventType
		actor string
	}{
		{EventCommented, ghostLogin}, // author が null（削除済みアカウント）
		{EventApproved, "alice"},     // createdAt ではなく submittedAt の 02:30 で並ぶ
		{EventCommitted, "Bob B"},    // GitHub ユーザーに紐づかないコミットは author 名
		{EventReplied, "dave"},       // 本文が無く、既存スレッドへの返信だけのレビュー
		// PENDING のレビューは含まれない
		{EventReviewed, "erin"},  // 返信と新しいスレッドを含むレビュー
		{EventReviewed, "frank"}, // 本文のあるレビュー
	}
	if len(events) != len(want) {
		t.Fatalf("len(Events) = %d, want %d: %+v", len(events), len(want), events)
	}
	for i, w := range want {
		if events[i].Type != w.typ || events[i].Actor != w.actor {
			t.Errorf("Events[%d] = {%v %q}, want {%v %q}", i, events[i].Type, events[i].Actor, w.typ, w.actor)
		}
	}

	issueEvents := got.Items[0].Events
	if issueEvents[0].Type != EventAssigned || issueEvents[0].Target != "me" {
		t.Errorf("issue Events[0] = %+v, want assigned to me", issueEvents[0])
	}
}

func TestParseResponse_DropsClosedRecordedWithMerge(t *testing.T) {
	// マージに伴う closed が merged より先に並ぶ応答。マージ前に閉じて開き直した回の closed は残す。
	const body = `{
  "data": {
    "viewer": {"login": "me"},
    "authored": {"issueCount": 1, "nodes": [
      {"__typename": "PullRequest", "number": 1, "title": "Add feature", "url": "https://github.com/o/r/pull/1",
       "state": "MERGED", "isDraft": false, "updatedAt": "2026-11-20T03:00:00Z",
       "author": {"login": "me"}, "repository": {"nameWithOwner": "o/r"},
       "timelineItems": {"nodes": [
         {"__typename": "ClosedEvent", "createdAt": "2026-11-20T01:00:00Z", "actor": {"login": "alice"}},
         {"__typename": "ReopenedEvent", "createdAt": "2026-11-20T01:10:00Z", "actor": {"login": "alice"}},
         {"__typename": "ClosedEvent", "createdAt": "2026-11-20T02:00:00Z", "actor": {"login": "bob"}},
         {"__typename": "MergedEvent", "createdAt": "2026-11-20T02:00:00Z", "actor": {"login": "bob"}}
       ]}}
    ]},
    "assigned": {"issueCount": 0, "nodes": []},
    "reviewRequested": {"issueCount": 0, "nodes": []},
    "reviewedBy": {"issueCount": 0, "nodes": []}
  }
}`
	got, err := parseResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var types []EventType
	for _, ev := range got.Items[0].Events {
		types = append(types, ev.Type)
	}
	if want := []EventType{EventClosed, EventReopened, EventMerged}; !slices.Equal(types, want) {
		t.Errorf("event types = %v, want %v", types, want)
	}
}

func TestParseResponse_StripsControlCharacters(t *testing.T) {
	const body = `{
  "data": {
    "viewer": {"login": "me"},
    "authored": {"issueCount": 1, "nodes": [
      {"__typename": "PullRequest", "number": 1, "title": "Fix\u001b]52;c;aGk=\u0007 バグ", "url": "https://github.com/o/r/pull/1",
       "state": "OPEN", "isDraft": false, "updatedAt": "2026-11-20T03:00:00Z",
       "author": {"login": "al\u009bice"}, "repository": {"nameWithOwner": "o/r\u007f"},
       "timelineItems": {"nodes": [
         {"__typename": "PullRequestCommit", "commit": {"committedDate": "2026-11-20T02:00:00Z", "author": {"name": "Bob\u001b[2J B\r\n", "user": null}}},
         {"__typename": "ReviewRequestedEvent", "createdAt": "2026-11-20T02:10:00Z", "actor": {"login": "car\u0000ol"}, "requestedReviewer": {"login": "", "name": "team\u001b[31m-a"}},
         {"__typename": "AssignedEvent", "createdAt": "2026-11-20T02:20:00Z", "actor": {"login": "carol"}, "assignee": {"login": "da\bve"}}
       ]}}
    ]},
    "assigned": {"issueCount": 0, "nodes": []},
    "reviewRequested": {"issueCount": 0, "nodes": []},
    "reviewedBy": {"issueCount": 0, "nodes": []}
  }
}`
	got, err := parseResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	item := got.Items[0]
	if item.Title != "Fix]52;c;aGk= バグ" {
		t.Errorf("Title = %q, want control characters removed and other characters kept", item.Title)
	}
	if item.Repo != "o/r" {
		t.Errorf("Repo = %q, want o/r", item.Repo)
	}
	if item.Author != "alice" {
		t.Errorf("Author = %q, want alice", item.Author)
	}

	want := []struct {
		actor  string
		target string
	}{
		{"Bob[2J B", ""},        // git の author 名
		{"carol", "team[31m-a"}, // チームへのレビュー依頼
		{"carol", "dave"},       // アサイン先の login
	}
	if len(item.Events) != len(want) {
		t.Fatalf("len(Events) = %d, want %d: %+v", len(item.Events), len(want), item.Events)
	}
	for i, w := range want {
		if ev := item.Events[i]; ev.Actor != w.actor || ev.Target != w.target {
			t.Errorf("Events[%d] = {Actor: %q, Target: %q}, want {Actor: %q, Target: %q}", i, ev.Actor, ev.Target, w.actor, w.target)
		}
	}
}

func TestParseResponse_ReturnsGraphQLErrors(t *testing.T) {
	_, err := parseResponse([]byte(`{"data": null, "errors": [{"message": "boom"}]}`))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want GraphQL error containing boom", err)
	}
}

// partialResponse は、権限の無い PR が null のノードになり、そのエラーが errors に載った応答。
const partialResponse = `{
  "data": {
    "viewer": {"login": "me"},
    "authored": {"issueCount": 3, "nodes": [
      {"__typename": "Issue", "number": 2, "title": "Bug", "url": "https://github.com/o/r/issues/2",
       "state": "OPEN", "updatedAt": "2026-11-20T04:00:00Z",
       "author": {"login": "me"}, "repository": {"nameWithOwner": "o/r"},
       "timelineItems": {"nodes": []}},
      null,
      null
    ]},
    "assigned": {"issueCount": 0, "nodes": []},
    "reviewRequested": {"issueCount": 0, "nodes": []},
    "reviewedBy": {"issueCount": 0, "nodes": []}
  },
  "errors": [
    {"message": "Resource protected by organization SAML enforcement."},
    {"message": "Resource protected by organization SAML enforcement."}
  ]
}`

func TestParseResponse_KeepsDataWithPartialErrors(t *testing.T) {
	got, err := parseResponse([]byte(partialResponse))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Key() != "o/r#2" {
		t.Errorf("Items = %+v, want only o/r#2", got.Items)
	}
	want := []string{"Resource protected by organization SAML enforcement."}
	if !slices.Equal(got.PartialErrors, want) {
		t.Errorf("PartialErrors = %q, want %q (duplicates removed)", got.PartialErrors, want)
	}
}

func TestClientSearch_UsesDataWhenGHFailsWithPartialErrors(t *testing.T) {
	fake := func(context.Context, ...string) ([]byte, error) {
		return []byte(partialResponse), errors.New("gh api: exit status 1")
	}
	got, err := NewClient(fake, false).Search(context.Background(), time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || len(got.PartialErrors) != 1 {
		t.Errorf("got %d items and partial errors %q, want 1 item and 1 partial error", len(got.Items), got.PartialErrors)
	}
}

func TestClientSearch_ReturnsGHErrorWithoutData(t *testing.T) {
	ghErr := errors.New("gh api: exit status 1: HTTP 401: Bad credentials")
	for name, out := range map[string]string{
		"no output": "",
		"null data": `{"data": null, "errors": [{"message": "boom"}]}`,
	} {
		fake := func(context.Context, ...string) ([]byte, error) { return []byte(out), ghErr }
		if _, err := NewClient(fake, false).Search(context.Background(), time.Now(), nil); err != ghErr {
			t.Errorf("%s: err = %v, want the gh error", name, err)
		}
	}
}

// installFakeGHCommand は、script を本体とする gh コマンドを PATH の先頭に置く。
func installFakeGHCommand(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestExecGH_ReturnsStdoutWhenGHFails(t *testing.T) {
	installFakeGHCommand(t, "echo '{\"data\":{}}'\necho 'gh: boom' >&2\nexit 1\n")
	out, err := ExecGH(context.Background(), "api", "graphql")
	if err == nil || !strings.Contains(err.Error(), "gh: boom") {
		t.Errorf("err = %v, want an error with gh's stderr", err)
	}
	if strings.TrimSpace(string(out)) != `{"data":{}}` {
		t.Errorf("out = %q, want gh's stdout", out)
	}
}

func TestExecGH_TimeoutSaysTimedOut(t *testing.T) {
	// exec で置き換えないと、sh を止めても sleep が標準出力を開いたまま残り、Output が戻らない。
	installFakeGHCommand(t, "exec sleep 10\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := ExecGH(ctx, "api", "graphql")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want a timed out error wrapping context.DeadlineExceeded", err)
	}
}

func TestSearchQueries_ReviewRequestScope(t *testing.T) {
	since := time.Date(2026, 11, 20, 12, 0, 0, 0, time.FixedZone("JST", 9*60*60))

	personal := searchQueries(since, false)
	if !strings.Contains(personal["reviewRequested"], "user-review-requested:@me") {
		t.Errorf("default reviewRequested = %q, want user-review-requested", personal["reviewRequested"])
	}
	if !strings.Contains(personal["authored"], "updated:>=2026-11-20T03:00:00Z") {
		t.Errorf("authored = %q, want since converted to UTC", personal["authored"])
	}

	team := searchQueries(since, true)
	if !strings.HasPrefix(team["reviewRequested"], "is:pr review-requested:@me ") {
		t.Errorf("team reviewRequested = %q, want review-requested", team["reviewRequested"])
	}
}

// comments は親のノード数の分だけ取得回数としてコストに数えられるため、reviews の下でだけ取る。
func TestSearchQuery_FetchesCommentsOnlyInReviewFragment(t *testing.T) {
	foundInReview := false
	for _, part := range strings.Split(searchQuery, "\nfragment ") {
		hasComments := strings.Contains(part, "comments(")
		if strings.HasPrefix(part, "review on PullRequestReview") {
			foundInReview = hasComments
			continue
		}
		if hasComments {
			t.Errorf("comments is fetched outside the review fragment:\n%s", part)
		}
	}
	if !foundInReview {
		t.Error("review fragment does not fetch comments")
	}
}

func TestClientSearch_PassesQueryVariablesToGH(t *testing.T) {
	var gotArgs []string
	fake := func(_ context.Context, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(sampleResponse), nil
	}
	got, err := NewClient(fake, false).Search(context.Background(), time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Errorf("len(Items) = %d, want 2", len(got.Items))
	}
	joined := strings.Join(gotArgs, "\n")
	for _, name := range []string{"authored=", "assigned=", "reviewRequested=", "reviewedBy=", "first=100"} {
		if !strings.Contains(joined, "\n"+name) {
			t.Errorf("gh args missing %q", name)
		}
	}
}
