package github

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
)

// ghostLogin は削除済みアカウントの表示名。GitHub の Web 表示に合わせている。
const ghostLogin = "ghost"

type response struct {
	// Data は、エラーで結果を何も返せないときに null になる。
	Data *struct {
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
	Repository  struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	TimelineItems struct {
		Nodes []rawEvent `json:"nodes"`
	} `json:"timelineItems"`
	Reviews struct {
		Nodes []rawEvent `json:"nodes"`
	} `json:"reviews"` // PR のみ
	HeadRefName    string `json:"headRefName"` // PR のみ
	HeadRepository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"` // PR のみ。fork が削除されると null
}

type rawEvent struct {
	Typename    string     `json:"__typename"`
	ID          string     `json:"id"`
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
		OID           string    `json:"oid"`
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
	// 権限の無い PR / issue は errors に載り、該当のノードは空になる。
	// 1 件のために全体を失敗させず、data があれば残りの結果を使う。
	var msgs []string
	for _, e := range resp.Errors {
		if !slices.Contains(msgs, e.Message) {
			msgs = append(msgs, e.Message)
		}
	}
	if resp.Data == nil {
		return SearchResult{}, errors.New("GraphQL error: " + strings.Join(msgs, "; "))
	}

	result := SearchResult{Viewer: resp.Data.Viewer.Login, PartialErrors: msgs}
	seenKeys := map[string]bool{}
	for _, conn := range []searchConnection{resp.Data.Authored, resp.Data.Assigned, resp.Data.ReviewRequested, resp.Data.ReviewedBy} {
		if conn.IssueCount > len(conn.Nodes) {
			result.Truncated = true
		}
		for _, raw := range conn.Nodes {
			item, ok := convertItem(raw)
			if !ok {
				continue
			}
			key := item.Key()
			if seenKeys[key] {
				continue
			}
			seenKeys[key] = true
			result.Items = append(result.Items, item)
		}
	}
	slices.SortStableFunc(result.Items, func(a, b Item) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
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
		Repo:        stripControl(raw.Repository.NameWithOwner),
		Number:      raw.Number,
		Title:       stripControl(raw.Title),
		URL:         raw.URL,
		State:       raw.State,
		StateReason: raw.StateReason,
		IsDraft:     raw.IsDraft,
		UpdatedAt:   raw.UpdatedAt,
		headRef:     raw.HeadRefName,
	}
	if raw.HeadRepository != nil {
		item.headRepo = raw.HeadRepository.NameWithOwner
	}
	for _, re := range slices.Concat(raw.TimelineItems.Nodes, raw.Reviews.Nodes) {
		if ev, ok := convertEvent(re); ok {
			ev.Actor, ev.Target = stripControl(ev.Actor), stripControl(ev.Target)
			item.Events = append(item.Events, ev)
		}
	}
	sortByTime(item.Events)
	item.Events = dropClosedByMerge(item.Events)
	return item, true
}

// sortByTime はイベントを古い順に並べる。同じ時刻のイベントは元の並びを保つ。
func sortByTime(events []Event) {
	slices.SortStableFunc(events, func(a, b Event) int { return a.At.Compare(b.At) })
}

// dropClosedByMerge は、マージ時に GitHub が MergedEvent と同時に記録する ClosedEvent を除く。
// "closed" まで出すと、マージとは別に閉じられたように読めるため。
// 並び順で「merged の直後の closed」を探さず時刻で照らし合わせるのは、同時刻の 2 件の順序が決まっていないため。
// マージ済みの PR の closed をすべて除かないのは、マージ前に閉じて開き直した回の closed を残すため。
func dropClosedByMerge(events []Event) []Event {
	var mergedAts []time.Time
	for _, ev := range events {
		if ev.Type == EventMerged {
			mergedAts = append(mergedAts, ev.At)
		}
	}
	return slices.DeleteFunc(events, func(ev Event) bool {
		return ev.Type == EventClosed && slices.ContainsFunc(mergedAts, ev.At.Equal)
	})
}

// actorEventTypes は、行った人と時刻のほかに読む項目が無い GraphQL のイベント型と、その EventType。
var actorEventTypes = map[string]EventType{
	"MergedEvent":             EventMerged,
	"ClosedEvent":             EventClosed,
	"ReopenedEvent":           EventReopened,
	"ReadyForReviewEvent":     EventReadyForReview,
	"ConvertToDraftEvent":     EventConvertedToDraft,
	"HeadRefForcePushedEvent": EventForcePushed,
}

func convertEvent(raw rawEvent) (Event, bool) {
	if t, ok := actorEventTypes[raw.Typename]; ok {
		return actorEvent(t, raw), true
	}
	switch raw.Typename {
	case "IssueComment":
		return Event{ID: raw.ID, Type: EventCommented, Actor: loginOrGhost(raw.Author), At: raw.CreatedAt}, true
	case "PullRequestReview":
		// 提出前（PENDING）のレビューは submittedAt が null。
		// 他人の PENDING は API から見えず、自分の下書きを「更新」として出す意味もない。
		if raw.SubmittedAt == nil {
			return Event{}, false
		}
		return Event{ID: raw.ID, Type: reviewEventType(raw), Actor: loginOrGhost(raw.Author), At: *raw.SubmittedAt}, true
	case "PullRequestCommit":
		if raw.Commit == nil {
			return Event{}, false
		}
		return Event{ID: raw.Commit.OID, Type: EventCommitted, Actor: commitActor(raw), At: raw.Commit.CommittedDate}, true
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
	return Event{ID: raw.ID, Type: t, Actor: loginOrGhost(raw.Actor), At: raw.CreatedAt}
}

// commitActor は GitHub ユーザーに紐づかないコミット（メールアドレスが未登録など）でも
// 誰のコミットか分かるよう、login が無ければ git の author 名を使う。
func commitActor(raw rawEvent) string {
	a := raw.Commit.Author
	if a == nil {
		return ghostLogin
	}
	return cmp.Or(loginOf(a.User), a.Name, ghostLogin)
}

func loginOrGhost(a *rawActor) string {
	return cmp.Or(loginOf(a), ghostLogin)
}

// loginOf は a の login を返す。a が nil なら空文字列。
func loginOf(a *rawActor) string {
	if a == nil {
		return ""
	}
	return a.Login
}

// stripControl は C0 / C1 制御文字と DEL を除く。描画側ではなく取り込む段階で除くのは、
// TUI と -dump の出力先ごとに除き漏れが出ないようにするため。
// タブと改行は除かずに半角スペースへ置き換える。除くと前後の語がつながってしまう。
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}
