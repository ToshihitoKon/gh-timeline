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
         {"__typename": "PullRequestReview", "submittedAt": "2026-11-20T02:30:00Z", "createdAt": "2026-11-20T02:00:00Z", "state": "APPROVED", "author": {"login": "alice"}},
         {"__typename": "IssueComment", "createdAt": "2026-11-20T02:10:00Z", "author": null},
         {"__typename": "PullRequestReview", "submittedAt": null, "createdAt": "2026-11-20T02:50:00Z", "state": "PENDING", "author": {"login": "me"}},
         {"__typename": "PullRequestCommit", "commit": {"committedDate": "2026-11-20T02:40:00Z", "author": {"name": "Bob B", "user": null}}}
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
		// PENDING のレビューは含まれない
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
