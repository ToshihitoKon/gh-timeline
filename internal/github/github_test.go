package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// graphQLRequest は GraphQL の要求の本文。
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// fakeAPI は GitHub の代わりに GraphQL と REST の要求に答える。
// rest は GraphQL 以外の要求の、パスとクエリ文字列（/repos/o/r/activity?... など）を受け取る。
type fakeAPI struct {
	graphQL func(req graphQLRequest) (status int, body string)
	rest    func(requestURI string) (status int, body string)
}

func (f fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var status int
	var body string
	if r.Method == http.MethodPost && r.URL.Path == "/graphql" {
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status, body = f.graphQL(req)
	} else {
		status, body = f.rest(r.URL.RequestURI())
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// respondGraphQL は、どの GraphQL の要求にも body を返す fakeAPI を作る。
func respondGraphQL(body string) fakeAPI {
	return fakeAPI{graphQL: func(graphQLRequest) (int, string) { return http.StatusOK, body }}
}

// newTestClient は、GitHub の代わりに handler へ要求を送る Client を作る。
func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	// api_host などを、テストを動かす人の gh の設定から読まないようにする。go-gh は設定を
	// プロセスで最初の 1 回だけ読むため、gh の設定に触れうるテストはどれも先に空のディレクトリへ向ける。
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	client, err := newClient(api.ClientOptions{
		Host:         "github.com",
		AuthToken:    "test-token",
		LogIgnoreEnv: true,
		Transport:    toServer{server},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// toServer は、api.github.com への要求の宛先を httptest のサーバーに書き換える。
type toServer struct{ server *httptest.Server }

func (s toServer) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := url.Parse(s.server.URL)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
	return s.server.Client().Transport.RoundTrip(req)
}

// isolateGHAuth は、テストを動かす人の gh の設定とログインを NewClient から見えなくする。
// gh を PATH から外すのは、keyring のトークンを読む gh auth token を実行させないため。
func isolateGHAuth(t *testing.T) {
	t.Helper()
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	for _, name := range []string{"GH_HOST", "GH_PATH", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		t.Setenv(name, "")
	}
}

func TestNewClient_UsesGHToken(t *testing.T) {
	isolateGHAuth(t)
	t.Setenv("GH_TOKEN", "test-token")
	if _, err := NewClient(false); err != nil {
		t.Fatalf("NewClient() = %v, want nil with GH_TOKEN set", err)
	}
}

func TestNewClient_FailsWithoutTokenForGHHost(t *testing.T) {
	isolateGHAuth(t)
	t.Setenv("GH_HOST", "ghe.example.com")
	// GH_TOKEN は github.com 用で、GitHub Enterprise Server には使われない。
	t.Setenv("GH_TOKEN", "test-token")
	_, err := NewClient(false)
	if err == nil || !strings.Contains(err.Error(), "ghe.example.com") || !strings.Contains(err.Error(), "gh auth login") {
		t.Fatalf("NewClient() = %v, want an error naming ghe.example.com and gh auth login", err)
	}
}

func TestNewClient_UsesEnterpriseTokenForGHHost(t *testing.T) {
	isolateGHAuth(t)
	t.Setenv("GH_HOST", "ghe.example.com")
	t.Setenv("GH_ENTERPRISE_TOKEN", "test-token")
	if _, err := NewClient(false); err != nil {
		t.Fatalf("NewClient() = %v, want nil with GH_ENTERPRISE_TOKEN set", err)
	}
}

// searchWithResponse は、検索に body を返す GitHub に対して Search を実行する。
func searchWithResponse(t *testing.T, body string) (SearchResult, error) {
	t.Helper()
	return newTestClient(t, respondGraphQL(body)).Search(context.Background(), time.Now())
}

const sampleResponse = `{
  "data": {
    "viewer": {"login": "me"},
    "authored": {"issueCount": 1, "nodes": [
      {"__typename": "PullRequest", "number": 1, "title": "Add feature", "url": "https://github.com/o/r/pull/1",
       "state": "OPEN", "isDraft": false, "updatedAt": "2026-11-20T03:00:00Z",
       "repository": {"nameWithOwner": "o/r"},
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
       "repository": {"nameWithOwner": "o/r"},
       "timelineItems": {"nodes": [
         {"__typename": "AssignedEvent", "createdAt": "2026-11-20T03:30:00Z", "actor": {"login": "carol"}, "assignee": {"login": "me"}},
         {"__typename": "ClosedEvent", "createdAt": "2026-11-20T03:50:00Z", "actor": {"login": "carol"}}
       ]}}
    ]},
    "reviewRequested": {"issueCount": 0, "nodes": []},
    "reviewedBy": {"issueCount": 150, "nodes": [
      {"__typename": "PullRequest", "number": 1, "title": "Add feature", "url": "https://github.com/o/r/pull/1",
       "state": "OPEN", "isDraft": false, "updatedAt": "2026-11-20T03:00:00Z",
       "repository": {"nameWithOwner": "o/r"},
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
	got, err := searchWithResponse(t, body)
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

func TestParseResponse_DropsDuplicatesAndSortsByUpdatedAt(t *testing.T) {
	got, err := searchWithResponse(t, sampleResponse)
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
}

func TestParseResponse_ConvertsEventsInTimeOrder(t *testing.T) {
	got, err := searchWithResponse(t, sampleResponse)
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
       "repository": {"nameWithOwner": "o/r"},
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
	got, err := searchWithResponse(t, body)
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
       "repository": {"nameWithOwner": "o/r\u007f"},
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
	// go-gh は応答の文字列の制御文字の多くを ^[ のような表記に置き換えるため、
	// stripControl が除くかを確かめるには、Client を通さずに応答を読む。
	var resp struct{ Data firstPages }
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	item := buildResult(resp.Data.Viewer.Login, resp.Data.connections([]string{"authored"}), nil).Items[0]
	if item.Title != "Fix]52;c;aGk= バグ" {
		t.Errorf("Title = %q, want control characters removed and other characters kept", item.Title)
	}
	if item.Repo != "o/r" {
		t.Errorf("Repo = %q, want o/r", item.Repo)
	}

	want := []struct {
		actor  string
		target string
	}{
		{"Bob[2J B  ", ""},      // git の author 名
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
	_, err := searchWithResponse(t, `{"data": null, "errors": [{"message": "boom"}]}`)
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
       "repository": {"nameWithOwner": "o/r"},
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
	got, err := searchWithResponse(t, partialResponse)
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

func TestClientSearch_ReturnsHTTPError(t *testing.T) {
	client := newTestClient(t, fakeAPI{graphQL: func(graphQLRequest) (int, string) {
		return http.StatusUnauthorized, `{"message": "Bad credentials"}`
	}})
	_, err := client.Search(context.Background(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "HTTP 401: Bad credentials") {
		t.Errorf("err = %v, want the HTTP 401 error", err)
	}
}

// stallingAPI は、応答の途中まで、または応答をまったく返さないまま、要求が打ち切られるまで待つ。
// テストの終わりには release を閉じて、残った要求を終わらせる。
type stallingAPI struct {
	sendPartialBody bool
	release         chan struct{}
}

func (s stallingAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.sendPartialBody {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": {"viewer": {"login": "me"},`))
		w.(http.Flusher).Flush()
	}
	select {
	case <-r.Context().Done():
	case <-s.release:
	}
}

func TestClientSearch_ReturnsTimedOutErrorWhenResponseStalls(t *testing.T) {
	for name, sendPartialBody := range map[string]bool{"no response": false, "partial body": true} {
		t.Run(name, func(t *testing.T) {
			stalling := stallingAPI{sendPartialBody: sendPartialBody, release: make(chan struct{})}
			client := newTestClient(t, stalling)
			// server.Close は処理中の要求を待つため、それより先に release を閉じる。
			t.Cleanup(func() { close(stalling.release) })

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := client.Search(ctx, time.Now())
				done <- err
			}()

			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
					t.Errorf("err = %v, want a timed out error wrapping context.DeadlineExceeded", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Search did not return after its timeout")
			}
		})
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
	for _, part := range strings.Split("\n"+searchFragments, "\nfragment ") {
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

// 開いている issue を PR から参照すると、GitHub は updatedAt を進めて CrossReferencedEvent だけを記録する。
// 取らないと、その更新が "updated -" として出る。
func TestSearchQuery_FetchesCrossReferencesOfPullRequestsAndIssues(t *testing.T) {
	itemTypes := regexp.MustCompile(`itemTypes: \[[^\]]*\]`).FindAllString(searchFragments, -1)
	if len(itemTypes) != 2 {
		t.Fatalf("found %d itemTypes lists, want 2 (pull requests and issues): %q", len(itemTypes), itemTypes)
	}
	for _, list := range itemTypes {
		if !strings.Contains(list, "CROSS_REFERENCED_EVENT") {
			t.Errorf("%s does not include CROSS_REFERENCED_EVENT", list)
		}
	}
	if !strings.Contains(searchFragments, "... on CrossReferencedEvent {") {
		t.Error("timelineItem fragment does not read CrossReferencedEvent")
	}
}

// go-gh は operationName を送らないため、送る文書の操作は 1 つでなければならない。
func TestQueries_HaveOneOperationEach(t *testing.T) {
	for name, query := range map[string]string{"firstPages": firstPagesQuery, "nextPage": nextPageQuery} {
		if got := strings.Count("\n"+query, "\nquery "); got != 1 {
			t.Errorf("%s query has %d operations, want 1", name, got)
		}
		if !strings.HasPrefix(query, "query "+name+"(") {
			t.Errorf("%s query starts with %.30q, want the %s operation", name, query, name)
		}
	}
}

func TestClientSearch_PassesQueryVariables(t *testing.T) {
	var got graphQLRequest
	client := newTestClient(t, fakeAPI{graphQL: func(req graphQLRequest) (int, string) {
		got = req
		return http.StatusOK, sampleResponse
	}})
	since := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
	result, err := client.Search(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Errorf("len(Items) = %d, want 2", len(result.Items))
	}
	if got.Query != firstPagesQuery {
		t.Errorf("query = %.40q..., want the firstPages query", got.Query)
	}
	for name, q := range searchQueries(since, false) {
		if got.Variables[name] != q {
			t.Errorf("variable %s = %v, want %q", name, got.Variables[name], q)
		}
	}
	if got.Variables["first"] != float64(searchPageSize) {
		t.Errorf("variable first = %v, want %d", got.Variables["first"], searchPageSize)
	}
}

func TestSearchQuery_FirstPagesSearchesEveryQueryName(t *testing.T) {
	query := firstPagesOperation
	aliases := regexp.MustCompile(`(?m)^\s*(\w+): search\(query: \$(\w+),`).FindAllStringSubmatch(query, -1)
	var got []string
	for _, m := range aliases {
		if m[1] != m[2] {
			t.Errorf("search alias %q takes $%s, want the variable of the same name", m[1], m[2])
		}
		if !strings.Contains(query, "$"+m[1]+": String!") {
			t.Errorf("firstPages does not declare $%s", m[1])
		}
		got = append(got, m[1])
	}
	slices.Sort(got)
	want := slices.Sorted(maps.Keys(searchQueries(time.Now(), false)))
	if !slices.Equal(got, want) {
		t.Errorf("firstPages searches %q, want the names of searchQueries %q", got, want)
	}
}

func TestParseResponse_ReadsEverySearch(t *testing.T) {
	var searches []string
	for i, name := range slices.Sorted(maps.Keys(searchQueries(time.Now(), false))) {
		searches = append(searches, fmt.Sprintf(`%q: {"issueCount": 1, "nodes": [%s]}`, name, issueNodes(i+1, 1)))
	}
	body := `{"data": {"viewer": {"login": "me"}, ` + strings.Join(searches, ", ") + `}}`
	got, err := searchWithResponse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != len(searches) {
		t.Errorf("len(Items) = %d, want %d (one from each search)", len(got.Items), len(searches))
	}
}

func TestParseResponse_TreatsNullSearchAsEmpty(t *testing.T) {
	body := `{"data": {"viewer": {"login": "me"},
	  "authored": null,
	  "assigned": {"issueCount": 1, "nodes": [` + issueNodes(1, 1) + `]}
	}}`
	got, err := searchWithResponse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Viewer != "me" || len(got.Items) != 1 {
		t.Errorf("Viewer = %q, len(Items) = %d; want me and 1", got.Viewer, len(got.Items))
	}
}

// issueNodes は o/r の issue を、番号 first から n 件並べた検索結果のノードを作る。
func issueNodes(first, n int) string {
	nodes := make([]string, n)
	for i := range nodes {
		nodes[i] = fmt.Sprintf(`{"__typename": "Issue", "number": %d, "updatedAt": "2026-11-20T03:00:00Z",
		  "repository": {"nameWithOwner": "o/r"}, "timelineItems": {"nodes": []}}`, first+i)
	}
	return strings.Join(nodes, ",")
}

// pagingGitHub は、authored だけが issueCount 件ヒットする検索に、1 ページ searchPageSize 件ずつ答える。
// nextPage の要求の変数を記録し、nextPageStatus が 200 以外ならそのステータスで失敗する。
type pagingGitHub struct {
	issueCount     int
	nextPageStatus int // 0 なら 200

	nextPageCalls []map[string]any
}

func (f *pagingGitHub) page(startNumber int) (nodes string, pageInfo string) {
	n := min(searchPageSize, f.issueCount-startNumber+1)
	hasNext := startNumber+n <= f.issueCount
	return issueNodes(startNumber, n), fmt.Sprintf(`{"hasNextPage": %t, "endCursor": "cursor%d"}`, hasNext, startNumber+n)
}

func (f *pagingGitHub) graphQL(req graphQLRequest) (int, string) {
	if req.Query != nextPageQuery {
		nodes, pageInfo := f.page(1)
		return http.StatusOK, fmt.Sprintf(`{"data": {
		  "viewer": {"login": "me"},
		  "authored": {"issueCount": %d, "pageInfo": %s, "nodes": [%s]},
		  "assigned": {"issueCount": 0, "nodes": []},
		  "reviewRequested": {"issueCount": 0, "nodes": []},
		  "reviewedBy": {"issueCount": 0, "nodes": []}
		}}`, f.issueCount, pageInfo, nodes)
	}
	f.nextPageCalls = append(f.nextPageCalls, req.Variables)
	if f.nextPageStatus != 0 && f.nextPageStatus != http.StatusOK {
		return f.nextPageStatus, `{"message": "Server Error"}`
	}
	after, _ := req.Variables["after"].(string)
	startNumber, _ := strconv.Atoi(strings.TrimPrefix(after, "cursor"))
	nodes, pageInfo := f.page(startNumber)
	return http.StatusOK, fmt.Sprintf(`{"data": {"search": {"issueCount": %d, "pageInfo": %s, "nodes": [%s]}}}`, f.issueCount, pageInfo, nodes)
}

func TestClientSearch_FetchesNextPagesOnlyWhenHitsExceedFirstPage(t *testing.T) {
	for _, tc := range []struct {
		issueCount    int
		wantNextPages int
		wantItems     int
		wantTruncated bool
	}{
		{issueCount: searchPageSize, wantNextPages: 0, wantItems: searchPageSize},
		{issueCount: searchPageSize + 1, wantNextPages: 1, wantItems: searchPageSize + 1},
		{issueCount: MaxSearchHits, wantNextPages: maxSearchPages - 1, wantItems: MaxSearchHits},
		// 上限を超えた分は取らず、Truncated で知らせる
		{issueCount: MaxSearchHits + 1, wantNextPages: maxSearchPages - 1, wantItems: MaxSearchHits, wantTruncated: true},
	} {
		gh := &pagingGitHub{issueCount: tc.issueCount}
		got, err := newTestClient(t, fakeAPI{graphQL: gh.graphQL}).Search(context.Background(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if len(gh.nextPageCalls) != tc.wantNextPages || len(got.Items) != tc.wantItems || got.Truncated != tc.wantTruncated {
			t.Errorf("issueCount %d: next pages = %d, items = %d, Truncated = %t; want %d, %d, %t",
				tc.issueCount, len(gh.nextPageCalls), len(got.Items), got.Truncated, tc.wantNextPages, tc.wantItems, tc.wantTruncated)
		}
	}
}

func TestClientSearch_PassesSearchQueryAndCursorToNextPage(t *testing.T) {
	gh := &pagingGitHub{issueCount: searchPageSize + 1}
	since := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
	if _, err := newTestClient(t, fakeAPI{graphQL: gh.graphQL}).Search(context.Background(), since); err != nil {
		t.Fatal(err)
	}
	if len(gh.nextPageCalls) != 1 {
		t.Fatalf("next page calls = %d, want 1", len(gh.nextPageCalls))
	}
	variables := gh.nextPageCalls[0]
	for name, want := range map[string]any{
		"searchQuery": searchQueries(since, false)["authored"],
		"after":       fmt.Sprintf("cursor%d", searchPageSize+1),
		"first":       float64(searchPageSize),
	} {
		if variables[name] != want {
			t.Errorf("next page variable %s = %v, want %v", name, variables[name], want)
		}
	}
}

func TestClientSearch_KeepsFirstPageWhenNextPageFails(t *testing.T) {
	gh := &pagingGitHub{issueCount: searchPageSize + 1, nextPageStatus: http.StatusBadGateway}
	got, err := newTestClient(t, fakeAPI{graphQL: gh.graphQL}).Search(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != searchPageSize {
		t.Errorf("len(Items) = %d, want %d (the first page)", len(got.Items), searchPageSize)
	}
	const wantPrefix = "more authored results unavailable: HTTP 502: Server Error"
	if len(got.PartialErrors) != 1 || !strings.HasPrefix(got.PartialErrors[0], wantPrefix) {
		t.Errorf("PartialErrors = %q, want one starting with %q", got.PartialErrors, wantPrefix)
	}
}
