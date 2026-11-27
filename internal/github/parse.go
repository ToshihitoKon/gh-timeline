package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// ghostLogin は削除済みアカウントの表示名。GitHub の Web 表示に合わせている。
const ghostLogin = "ghost"

type response struct {
	Data struct {
		Viewer          struct{ Login string } `json:"viewer"`
		Authored        searchConnection       `json:"authored"`
		Assigned        searchConnection       `json:"assigned"`
		ReviewRequested searchConnection       `json:"reviewRequested"`
		ReviewedBy      searchConnection       `json:"reviewedBy"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type searchConnection struct {
	IssueCount int       `json:"issueCount"`
	Nodes      []rawItem `json:"nodes"`
}

type rawActor struct {
	Login string `json:"login"`
}

type rawItem struct {
	Typename    string    `json:"__typename"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	State       string    `json:"state"`
	StateReason string    `json:"stateReason"`
	IsDraft     bool      `json:"isDraft"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Author      *rawActor `json:"author"`
	Repository  struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	TimelineItems struct {
		Nodes []rawEvent `json:"nodes"`
	} `json:"timelineItems"`
	Reviews struct {
		Nodes []rawEvent `json:"nodes"`
	} `json:"reviews"` // PR のみ
}

type rawEvent struct {
	Typename    string     `json:"__typename"`
	CreatedAt   time.Time  `json:"createdAt"`
	SubmittedAt *time.Time `json:"submittedAt"`
	State       string     `json:"state"`
	Body        string     `json:"body"`
	Author      *rawActor  `json:"author"`
	Actor       *rawActor  `json:"actor"`
	Comments    *struct {
		TotalCount int                `json:"totalCount"`
		Nodes      []rawReviewComment `json:"nodes"`
	} `json:"comments"`
	Commit *struct {
		CommittedDate time.Time `json:"committedDate"`
		Author        *struct {
			Name string    `json:"name"`
			User *rawActor `json:"user"`
		} `json:"author"`
	} `json:"commit"`
	RequestedReviewer *struct {
		Login string `json:"login"`
		Name  string `json:"name"` // Team の場合
	} `json:"requestedReviewer"`
	Assignee *rawActor `json:"assignee"`
}

type rawReviewComment struct {
	ReplyTo *struct{} `json:"replyTo"`
}

func parseResponse(body []byte) (SearchResult, error) {
	var resp response
	if err := json.Unmarshal(body, &resp); err != nil {
		return SearchResult{}, fmt.Errorf("parse GraphQL response: %w", err)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, len(resp.Errors))
		for i, e := range resp.Errors {
			msgs[i] = e.Message
		}
		return SearchResult{}, errors.New("GraphQL error: " + strings.Join(msgs, "; "))
	}

	result := SearchResult{Viewer: resp.Data.Viewer.Login}
	byKey := map[string]*Item{}
	var order []string
	for _, conn := range []struct {
		role Role
		conn searchConnection
	}{
		{RoleAuthor, resp.Data.Authored},
		{RoleAssignee, resp.Data.Assigned},
		{RoleReviewRequested, resp.Data.ReviewRequested},
		{RoleReviewed, resp.Data.ReviewedBy},
	} {
		if conn.conn.IssueCount > len(conn.conn.Nodes) {
			result.Truncated = true
		}
		for _, raw := range conn.conn.Nodes {
			item, ok := convertItem(raw)
			if !ok {
				continue
			}
			key := item.Key()
			if existing, found := byKey[key]; found {
				existing.Roles |= conn.role
				continue
			}
			item.Roles = conn.role
			byKey[key] = &item
			order = append(order, key)
		}
	}

	for _, key := range order {
		result.Items = append(result.Items, *byKey[key])
	}
	sort.SliceStable(result.Items, func(i, j int) bool {
		return result.Items[i].UpdatedAt.After(result.Items[j].UpdatedAt)
	})
	return result, nil
}

// convertItem は検索結果の 1 件を Item にする。PR / issue 以外
// （権限不足で中身が空のノードなど）は ok=false を返す。
func convertItem(raw rawItem) (Item, bool) {
	var kind Kind
	switch raw.Typename {
	case "PullRequest":
		kind = KindPullRequest
	case "Issue":
		kind = KindIssue
	default:
		return Item{}, false
	}

	item := Item{
		Kind:        kind,
		Repo:        raw.Repository.NameWithOwner,
		Number:      raw.Number,
		Title:       raw.Title,
		URL:         raw.URL,
		State:       raw.State,
		StateReason: raw.StateReason,
		IsDraft:     raw.IsDraft,
		Author:      loginOrGhost(raw.Author),
		UpdatedAt:   raw.UpdatedAt,
	}
	for _, re := range slices.Concat(raw.TimelineItems.Nodes, raw.Reviews.Nodes) {
		if ev, ok := convertEvent(re); ok {
			item.Events = append(item.Events, ev)
		}
	}
	sort.SliceStable(item.Events, func(i, j int) bool {
		return item.Events[i].At.Before(item.Events[j].At)
	})
	return item, true
}

func convertEvent(raw rawEvent) (Event, bool) {
	switch raw.Typename {
	case "IssueComment":
		return Event{Type: EventCommented, Actor: loginOrGhost(raw.Author), At: raw.CreatedAt}, true
	case "PullRequestReview":
		// 提出前（PENDING）のレビューは submittedAt が null。
		// 他人の PENDING は API から見えず、自分の下書きを「更新」として出す意味もない。
		if raw.SubmittedAt == nil {
			return Event{}, false
		}
		return Event{Type: reviewEventType(raw), Actor: loginOrGhost(raw.Author), At: *raw.SubmittedAt}, true
	case "PullRequestCommit":
		if raw.Commit == nil {
			return Event{}, false
		}
		return Event{Type: EventCommitted, Actor: commitActor(raw), At: raw.Commit.CommittedDate}, true
	case "MergedEvent":
		return actorEvent(EventMerged, raw), true
	case "ClosedEvent":
		return actorEvent(EventClosed, raw), true
	case "ReopenedEvent":
		return actorEvent(EventReopened, raw), true
	case "ReadyForReviewEvent":
		return actorEvent(EventReadyForReview, raw), true
	case "ConvertToDraftEvent":
		return actorEvent(EventConvertedToDraft, raw), true
	case "HeadRefForcePushedEvent":
		return actorEvent(EventForcePushed, raw), true
	case "ReviewRequestedEvent":
		ev := actorEvent(EventReviewRequested, raw)
		if r := raw.RequestedReviewer; r != nil {
			ev.Target = r.Login
			if ev.Target == "" {
				ev.Target = r.Name
			}
		}
		return ev, true
	case "AssignedEvent":
		ev := actorEvent(EventAssigned, raw)
		if raw.Assignee != nil {
			ev.Target = raw.Assignee.Login
		}
		return ev, true
	}
	return Event{}, false
}

func reviewEventType(raw rawEvent) EventType {
	switch raw.State {
	case "APPROVED":
		return EventApproved
	case "CHANGES_REQUESTED":
		return EventChangesRequested
	}
	if isReplyOnly(raw) {
		return EventReplied
	}
	return EventReviewed
}

// isReplyOnly は、本文が無く、コメントがすべて既存スレッドへの返信のレビューかを返す。
// 新しいスレッドや本文を含むレビューは GitHub の画面でもレビューとして出るため、返信として扱わない。
func isReplyOnly(raw rawEvent) bool {
	c := raw.Comments
	if raw.Body != "" || c == nil || c.TotalCount == 0 || c.TotalCount > len(c.Nodes) {
		return false
	}
	return !slices.ContainsFunc(c.Nodes, func(n rawReviewComment) bool { return n.ReplyTo == nil })
}

func actorEvent(t EventType, raw rawEvent) Event {
	return Event{Type: t, Actor: loginOrGhost(raw.Actor), At: raw.CreatedAt}
}

// commitActor は GitHub ユーザーに紐づかないコミット（メールアドレスが未登録など）でも
// 誰のコミットか分かるよう、login が無ければ git の author 名を使う。
func commitActor(raw rawEvent) string {
	a := raw.Commit.Author
	switch {
	case a == nil:
		return ghostLogin
	case a.User != nil && a.User.Login != "":
		return a.User.Login
	case a.Name != "":
		return a.Name
	default:
		return ghostLogin
	}
}

func loginOrGhost(a *rawActor) string {
	if a == nil || a.Login == "" {
		return ghostLogin
	}
	return a.Login
}
