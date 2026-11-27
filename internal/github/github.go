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
	"sync"
	"time"
)

//go:embed search.graphql
var searchQuery string

// SearchPageSize は 1 つの検索条件で取得する件数。GitHub の検索 API が
// 1 ページで返せる上限の 100 にしている。ページングしないのは、ポーリング間隔
// （既定 5 分）の間に 100 件を超えて更新されることは想定しにくく、超えた場合も
// SearchResult.Truncated で利用者に知らせれば足りるため。
const SearchPageSize = 100

// Kind は PR か issue かの区別。
type Kind int

const (
	KindPullRequest Kind = iota
	KindIssue
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
	// EventUpdated は個別のイベントを取得できなかった更新。タイトル・本文の編集など、取得対象外の操作だけで
	// updatedAt が進んだときに表示側が作る。新しい種類はこれより前に足す。ui のテストが
	// ここまでを数えて、すべての種類が表示の表にあるかを確かめるため。
	EventUpdated
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
	// IsPushTimeUnknown は、コミットの At を push の時刻に置き換えられず、committedDate のままであることを示す。
	IsPushTimeUnknown bool
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
	UpdatedAt   time.Time
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
	// Truncated は、いずれかの検索条件で SearchPageSize を超えてヒットし、
	// 取りこぼしがあることを示す。
	Truncated bool
	// PartialErrors は、結果の一部を取得できなかったときのメッセージ（重複を除く）。
	// GraphQL のエラーは、SAML SSO を強制する org の PR / issue に gh のトークンが認可されていない場合などに出る。
	// push を取得できなかった PR があれば、その件数も載せる。
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

	mu        sync.Mutex // pushCache を守る
	pushCache map[branchKey]branchPushes
}

// NewClient は Client を作る。
func NewClient(run Runner, includeTeamReviews bool) *Client {
	return &Client{run: run, includeTeamReviews: includeTeamReviews, pushCache: map[branchKey]branchPushes{}}
}

// Search は since 以降に更新された、自分が関わる PR / issue を取得する。
// gh が失敗しても、応答に data があれば部分的な結果として返す。
func (c *Client) Search(ctx context.Context, since time.Time) (SearchResult, error) {
	args := []string{"api", "graphql", "-f", "query=" + searchQuery, "-F", fmt.Sprintf("first=%d", SearchPageSize)}
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
	if numFailed := c.retimeCommitsByPush(ctx, result.Items); numFailed > 0 {
		result.PartialErrors = append(result.PartialErrors, pushTimesUnavailableMessage(numFailed))
	}
	return result, nil
}

// retimeCommitsByPush は、PR のコミットの時刻を push された時刻に置き換え、push を取得できなかった PR の数を返す。
//
// committedDate は手元でコミットした時刻で、後からまとめて push すると push より古い時刻になる。
// 初めて見る PR では検索範囲の始点より前として捨てられ、表示済みの PR では古い時刻で並ぶため。
// 取得できなかった PR は committedDate のままにし、検索自体は失敗させない。
//
// 取得した push はブランチごとにキャッシュし、ブランチの最新のコミットが変わったときだけ取り直す。
// 最新のコミットまで置き換えられなかったときはキャッシュしない。activity API への反映が遅れて
// 直近の push がまだ載っていないことがあり、キャッシュすると次の push まで置き換えられないため。
//
// 取得は並列に行う。結果は PR ごとの枠に書き、全件の取得を待ってから反映するため、枠への書き込みにロックは取らない。
func (c *Client) retimeCommitsByPush(ctx context.Context, items []Item) (numFailed int) {
	pushesByItem := make([][]push, len(items))
	errs := make([]error, len(items))
	var fetchTargets []int // push を取得する items の添字

	c.mu.Lock()
	for i, item := range items {
		if !needsPushTimes(item) {
			continue
		}
		if cached, ok := c.pushCache[branchOf(item)]; ok && cached.newestCommit == newestCommitID(item) {
			pushesByItem[i] = cached.pushes
			continue
		}
		fetchTargets = append(fetchTargets, i)
	}
	c.mu.Unlock()

	slots := make(chan struct{}, pushFetchConcurrency)
	var wg sync.WaitGroup
	for _, i := range fetchTargets {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			pushesByItem[i], errs[i] = fetchPushes(ctx, c.run, items[i].headRepo, items[i].headRef)
		})
	}
	wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range items {
		if !needsPushTimes(items[i]) {
			continue
		}
		if errs[i] != nil {
			numFailed++
		}
		newestCommit := newestCommitID(items[i])
		if applyPushTimes(&items[i], pushesByItem[i]) {
			c.pushCache[branchOf(items[i])] = branchPushes{newestCommit: newestCommit, pushes: pushesByItem[i]}
		}
	}
	return numFailed
}

// pushTimesUnavailableMessage は、push を取得できなかった PR の数を知らせる PartialErrors のメッセージを返す。
func pushTimesUnavailableMessage(numFailed int) string {
	if numFailed == 1 {
		return "push times unavailable for 1 pull request"
	}
	return fmt.Sprintf("push times unavailable for %d pull requests", numFailed)
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
