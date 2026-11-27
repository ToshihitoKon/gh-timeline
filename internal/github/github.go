// Package github は gh コマンド経由で GitHub の検索 API を呼び、
// 自分が関わる PR / issue と、それぞれの直近のタイムラインイベントを取得する。
package github

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	// ID はイベントを見分ける値。コミットは oid、それ以外は node id。
	// コミットに PullRequestCommit の node id を使わないのは、push の activity と oid で突き合わせるため。
	ID     string
	Type   EventType
	Actor  string // 行った人の login。コミットで GitHub ユーザーに紐づかない場合は author 名
	Target string // レビュー依頼先・アサイン先など、対象がある種類のみ
	At     time.Time
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
	// PartialErrors は、結果の一部を取得できなかったときの GraphQL のエラーメッセージ（重複を除く）。
	// SAML SSO を強制する org の PR / issue に gh のトークンが認可されていない場合などに出る。
	PartialErrors []string
}

// Runner は gh コマンドを引数付きで実行し、標準出力を返す。テストで差し替える。
// gh が非ゼロで終わったときも、得られた標準出力はエラーと一緒に返す。
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
	if err == nil {
		return out, nil
	}
	// 打ち切られた gh の終了理由は "signal: killed" となり、タイムアウトだと分からない。
	// 途中までの標準出力は使えないため返さない。
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("gh %s: timed out: %w", args[0], ctx.Err())
	}
	// gh api graphql は、応答に errors があると data があっても非ゼロで終わるが、応答は標準出力に出す。
	// 部分的な結果を呼び出し側で使えるよう、標準出力も返す。
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return out, fmt.Errorf("gh %s: %w: %s", args[0], err, msg)
	}
	return out, fmt.Errorf("gh %s: %w", args[0], err)
}

// Client は自分が関わる PR / issue を検索する。
type Client struct {
	run                Runner
	includeTeamReviews bool // 所属チーム宛てのレビュー依頼も対象にするか
}

// NewClient は Client を作る。
func NewClient(run Runner, includeTeamReviews bool) *Client {
	return &Client{run: run, includeTeamReviews: includeTeamReviews}
}

// Search は since 以降に更新された、自分が関わる PR / issue を取得する。
// seenKeys は表示済みの PR / issue の Key で、これに含まれない PR だけコミットの時刻を push の時刻に置き換える。
// gh が失敗しても、応答に data があれば部分的な結果として返す。
func (c *Client) Search(ctx context.Context, since time.Time, seenKeys map[string]bool) (SearchResult, error) {
	args := []string{"api", "graphql", "-f", "query=" + searchQuery, "-F", fmt.Sprintf("first=%d", searchPageSize)}
	for name, q := range searchQueries(since, c.includeTeamReviews) {
		args = append(args, "-f", name+"="+q)
	}
	out, runErr := c.run(ctx, args...)
	result, err := parseResponse(out)
	if err != nil {
		// gh が失敗していれば、応答を読めなかった理由は gh の stderr の方がよく表している。
		if runErr != nil {
			return SearchResult{}, runErr
		}
		return SearchResult{}, err
	}
	c.retimeCommitsByPush(ctx, result.Items, seenKeys)
	return result, nil
}

// retimeCommitsByPush は、初めて見る PR のコミットの時刻を push された時刻に置き換える。
//
// committedDate は手元でコミットした時刻で、後からまとめて push すると検索範囲の始点より古くなり、
// 初めて見る PR では範囲外として捨てられるため。表示済みの PR は新しいコミットを oid で見分けるので要らない。
// 取得できなかった PR は committedDate のままにし、検索自体は失敗させない。
// 取得した push をキャッシュしないのは、検索が成功すれば PR は表示済みになり、同じ PR で取り直すことがないため。
func (c *Client) retimeCommitsByPush(ctx context.Context, items []Item, seenKeys map[string]bool) {
	for i := range items {
		item := &items[i]
		if !needsPushTimes(*item) || seenKeys[item.Key()] {
			continue
		}
		pushes, err := fetchPushes(ctx, c.run, item.headRepo, item.headRef)
		if err != nil {
			continue
		}
		applyPushTimes(item, pushes)
	}
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
