// Package github は gh コマンド経由で GitHub の検索 API を呼び、
// 自分が関わる PR / issue と、それぞれの直近のタイムラインイベントを取得する。
package github

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

//go:embed search.graphql
var searchQuery string

// searchPageSize は 1 つの検索条件で取得する件数。GitHub の検索 API が
// 1 ページで返せる上限の 100 にしている。ページングしないのは、ポーリング間隔
// （既定 5 分）の間に 100 件を超えて更新されることは想定しにくく、超えた場合も
// SearchResult.Truncated で利用者に知らせれば足りるため。
const searchPageSize = 100

// Kind は PR か issue かの区別。
type Kind int

const (
	KindPullRequest Kind = iota
	KindIssue
)

// Role は自分がその PR / issue にどう関わっているか。複数を同時に持ちうる。
type Role int

const (
	RoleAuthor Role = 1 << iota
	RoleAssignee
	RoleReviewRequested
	RoleReviewed
)

// EventType はタイムラインイベントの種類。
type EventType int

const (
	EventCommented EventType = iota
	EventApproved
	EventChangesRequested
	EventReviewed
	EventReplied // 既存のレビュースレッドへの返信だけのレビュー
	EventCommitted
	EventMerged
	EventClosed
	EventReopened
	EventReviewRequested
	EventReadyForReview
	EventConvertedToDraft
	EventForcePushed
	EventAssigned
)

// Event は PR / issue のタイムライン上の 1 つの出来事。
type Event struct {
	Type   EventType
	Actor  string // 行った人の login。コミットで GitHub ユーザーに紐づかない場合は author 名
	Target string // レビュー依頼先・アサイン先など、対象がある種類のみ
	At     time.Time

	commitOID string // コミットのみ。push の時刻と突き合わせるのに使う
}

// Item は検索でヒットした PR / issue。
type Item struct {
	Kind        Kind
	Repo        string // owner/name
	Number      int
	Title       string
	URL         string
	State       string // OPEN / CLOSED / MERGED
	StateReason string // issue のみ。COMPLETED / NOT_PLANNED など
	IsDraft     bool
	Author      string
	UpdatedAt   time.Time
	Roles       Role
	Events      []Event // 古い順

	headRepo string // PR のみ。head ブランチのあるリポジトリ（owner/name）
	headRef  string // PR のみ。head ブランチ名
}

// Key は PR / issue を一意に識別する文字列。
func (i Item) Key() string {
	return fmt.Sprintf("%s#%d", i.Repo, i.Number)
}

// SearchResult は 1 回の検索の結果。
type SearchResult struct {
	Viewer string // 認証中のユーザーの login
	Items  []Item // 新しく更新された順
	// Truncated は、いずれかの検索条件で searchPageSize を超えてヒットし、
	// 取りこぼしがあることを示す。
	Truncated bool
}

// Runner は gh コマンドを引数付きで実行し、標準出力を返す。テストで差し替える。
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// ExecGH は実際に gh コマンドを実行する Runner。
func ExecGH(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	// CLICOLOR_FORCE や GH_FORCE_TTY が設定された環境では、gh api がパイプ先にも
	// 色付け・整形した JSON を返し、パースに失敗する。利用者の環境変数によらず素の JSON を受け取る。
	cmd.Env = append(os.Environ(), "CLICOLOR_FORCE=0", "NO_COLOR=1", "GH_FORCE_TTY=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("gh %s: %w: %s", args[0], err, msg)
		}
		return nil, fmt.Errorf("gh %s: %w", args[0], err)
	}
	return out, nil
}

// Client は自分が関わる PR / issue を検索する。
type Client struct {
	run                Runner
	includeTeamReviews bool // 所属チーム宛てのレビュー依頼も対象にするか

	mu sync.Mutex
	// pushCache は PR ごとに、取得した push の一覧とそのときの updatedAt。
	// 検索範囲は前回のポーリングと重なるため、同じ更新で何度も取得しないようにする。
	pushCache map[string]pushCacheEntry
}

type pushCacheEntry struct {
	updatedAt time.Time
	pushes    []push
}

// NewClient は Client を作る。
func NewClient(run Runner, includeTeamReviews bool) *Client {
	return &Client{run: run, includeTeamReviews: includeTeamReviews, pushCache: map[string]pushCacheEntry{}}
}

// Search は since 以降に更新された、自分が関わる PR / issue を取得する。
func (c *Client) Search(ctx context.Context, since time.Time) (SearchResult, error) {
	args := []string{"api", "graphql", "-f", "query=" + searchQuery, "-F", fmt.Sprintf("first=%d", searchPageSize)}
	for name, q := range searchQueries(since, c.includeTeamReviews) {
		args = append(args, "-f", name+"="+q)
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return SearchResult{}, err
	}
	result, err := parseResponse(out)
	if err != nil {
		return SearchResult{}, err
	}
	c.retimeCommitsByPush(ctx, result.Items)
	return result, nil
}

// retimeCommitsByPush は、コミットでしか説明できない更新のある PR について、
// コミットの時刻を push された時刻に置き換える。
//
// committedDate は手元でコミットした時刻で、後からまとめて push すると
// 検索範囲や表示済みの時刻より古くなり、push を取りこぼすため。
// 取得できなかった PR は committedDate のままにし、検索自体は失敗させない。
func (c *Client) retimeCommitsByPush(ctx context.Context, items []Item) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 今回の対象だけを残すことで、検索範囲から外れた PR や更新された PR の分を捨てる。
	nextCache := map[string]pushCacheEntry{}
	for i := range items {
		item := &items[i]
		if !needsPushTimes(*item) {
			continue
		}
		key := item.Key()
		entry, ok := c.pushCache[key]
		if !ok || !entry.updatedAt.Equal(item.UpdatedAt) {
			pushes, err := fetchPushes(ctx, c.run, item.headRepo, item.headRef)
			if err != nil {
				continue
			}
			entry = pushCacheEntry{updatedAt: item.UpdatedAt, pushes: pushes}
		}
		nextCache[key] = entry
		applyPushTimes(item, entry.pushes)
	}
	c.pushCache = nextCache
}

// searchQueries は search.graphql の変数名ごとの検索クエリを返す。
//
// レビュー依頼は既定で user-review-requested（自分個人への依頼）に絞る。
// review-requested はチーム宛ての依頼も含み、CODEOWNERS でチームが自動指定される
// リポジトリでは自分が見る必要のない PR が大量に混ざるため。
// reviewed-by を加えているのは、レビューを提出すると review-requested から外れ、
// その後の作者の返信や追加コミットを追えなくなるため。
func searchQueries(since time.Time, includeTeamReviews bool) map[string]string {
	common := fmt.Sprintf("updated:>=%s archived:false sort:updated-desc", since.UTC().Format(time.RFC3339))
	reviewRequested := "user-review-requested:@me"
	if includeTeamReviews {
		reviewRequested = "review-requested:@me"
	}
	return map[string]string{
		"authored":        "author:@me " + common,
		"assigned":        "assignee:@me " + common,
		"reviewRequested": "is:pr " + reviewRequested + " " + common,
		"reviewedBy":      "is:pr reviewed-by:@me " + common,
	}
}
