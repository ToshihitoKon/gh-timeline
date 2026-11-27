package github

import (
	"context"
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
         {"__typename": "PullRequestReview", "submittedAt": "2026-11-20T02:30:00Z", "createdAt": "2026-11-20T02:00:00Z", "state": "APPROVED", "body": "", "author": {"login": "alice"},
          "comments": {"totalCount": 0, "nodes": []}},
         {"__typename": "PullRequestReview", "submittedAt": "2026-11-20T02:45:00Z", "createdAt": "2026-11-20T02:45:00Z", "state": "COMMENTED", "body": "", "author": {"login": "dave"},
          "comments": {"totalCount": 1, "nodes": [{"replyTo": {"id": "c1"}}]}},
         {"__typename": "PullRequestReview", "submittedAt": null, "createdAt": "2026-11-20T02:50:00Z", "state": "PENDING", "body": "", "author": {"login": "me"},
          "comments": {"totalCount": 1, "nodes": [{"replyTo": {"id": "c1"}}]}},
         {"__typename": "PullRequestReview", "submittedAt": "2026-11-20T02:51:00Z", "createdAt": "2026-11-20T02:51:00Z", "state": "COMMENTED", "body": "", "author": {"login": "erin"},
          "comments": {"totalCount": 2, "nodes": [{"replyTo": {"id": "c1"}}, {"replyTo": null}]}},
         {"__typename": "PullRequestReview", "submittedAt": "2026-11-20T02:52:00Z", "createdAt": "2026-11-20T02:52:00Z", "state": "COMMENTED", "body": "see inline", "author": {"login": "frank"},
          "comments": {"totalCount": 1, "nodes": [{"replyTo": {"id": "c1"}}]}}
       ]}}
    ]},
    "assigned": {"issueCount": 1, "nodes": [
      {"__typename": "Issue", "number": 2, "title": "Bug", "url": "https://github.com/o/r/issues/2",
       "state": "CLOSED", "stateReason": "COMPLETED", "updatedAt": "2026-11-20T04:00:00Z",
       "author": {"login": "carol"}, "repository": {"nameWithOwner": "o/r"},
       "timelineItems": {"nodes": [
         {"__typename": "AssignedEvent", "createdAt": "2026-11-20T03:30:00Z", "actor": {"login": "carol"}, "assignee": {"__typename": "User", "login": "me"}},
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

func TestClientSearch_PassesQueryVariablesToGH(t *testing.T) {
	var gotArgs []string
	fake := func(_ context.Context, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(sampleResponse), nil
	}
	got, err := NewClient(fake, false).Search(context.Background(), time.Now())
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
