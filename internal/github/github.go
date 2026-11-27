// Package github は gh と同じ認証情報で GitHub の検索 API を呼び、
// 自分が関わる PR / issue と、それぞれの直近のタイムラインイベントを取得する。
package github

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
)

//go:embed search.graphql
var searchFragments string

//go:embed first_pages.graphql
var firstPagesOperation string

//go:embed next_page.graphql
var nextPageOperation string

// 操作ごとに別の文書にして送る。go-gh の GraphQLClient は operationName を送れず、
// 1 つの文書に操作が 2 つあると、GitHub はどちらを実行するか決められずに失敗するため。
var (
	firstPagesQuery = firstPagesOperation + "\n" + searchFragments
	nextPageQuery   = nextPageOperation + "\n" + searchFragments
)

// searchPageSize は 1 つの検索条件で 1 回に取得する件数。GraphQL のコストは件数にほぼ比例する一方、
// 2 回目以降のポーリングの検索範囲は前回の開始から十数分ほどで、ヒットはふつう数件にとどまる。
// 25 にするのは、firstPages のコストが 12 ポイントに下がり、MaxSearchHits の 100 件をちょうど 4 ページで取れるため。
const searchPageSize = 25

// maxSearchPages は 1 つの検索条件で取得するページ数の上限。上限が無いと、-since を長くした初回の
// ポーリングなどでヒットが多いとき、コストと gh の呼び出しが際限なく増える。4 ページにするのは、
// すべての検索条件で上限まで取ったときのコストが、100 件を 1 ページで取っていたときと同じ 48 ポイントに収まるため。
const maxSearchPages = 4

// MaxSearchHits は 1 つの検索条件で取得する件数の上限。超えてヒットした分は SearchResult.Truncated で知らせる。
const MaxSearchHits = searchPageSize * maxSearchPages

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
	EventReferenced // 他の PR / issue から参照された。参照元は Event.Source
	// EventUpdated は個別のイベントを取得できなかった更新。タイトル・本文の編集など、取得対象外の操作だけで
	// updatedAt が進んだときに timeline.Tracker が作る。誰の操作かは分からないため Actor は空にする。
	EventUpdated

	// EventTypeCount は種類の数。種類はこれより前に足す。
	EventTypeCount
)

// Event は PR / issue のタイムライン上の 1 つの出来事。
type Event struct {
	// ID はイベントを見分ける値。コミットは oid、それ以外は node id。
	// コミットに PullRequestCommit の node id を使わないのは、push の activity と oid で突き合わせるため。
	ID     string
	Type   EventType
	Actor  string // 行った人の login。コミットで GitHub ユーザーに紐づかない場合は author 名
	Target string // レビュー依頼先・アサイン先など、対象がある種類のみ
	// Source は参照元の PR / issue。EventReferenced のみで、参照元を読めなかったときは nil。
	// Target に入れないのは、Target が人やチームを指し、描画側がユーザーとして出すため。
	Source *Ref
	At     time.Time
	// IsPushTimeUnknown は、コミットの At を push の時刻に置き換えられず、committedDate のままであることを示す。
	IsPushTimeUnknown bool
}

// Ref は PR / issue を指す。
type Ref struct {
	Kind   Kind
	Repo   string // owner/name
	Number int
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
	return itemKey(i.Repo, i.Number)
}

func itemKey(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

// SearchResult は 1 回の検索の結果。
type SearchResult struct {
	Viewer string // 認証中のユーザーの login
	Items  []Item // 新しく更新された順
	// Truncated は、いずれかの検索条件で MaxSearchHits を超えてヒットし、
	// 取りこぼしがあることを示す。
	Truncated bool
	// PartialErrors は、結果の一部を取得できなかったときのメッセージ（重複を除く）。
	// GraphQL のエラーは、SAML SSO を強制する org の PR / issue に gh のトークンが認可されていない場合などに出る。
	// push を取得できなかった PR があれば、その件数も載せる。
	PartialErrors []string
}

// Client は自分が関わる PR / issue を検索する。
type Client struct {
	graphQL            *api.GraphQLClient
	rest               *api.RESTClient
	includeTeamReviews bool // 所属チーム宛てのレビュー依頼も対象にするか

	mu        sync.Mutex // pushCache を守る
	pushCache map[branchKey]branchPushes
}

// NewClient は gh と同じホストと認証情報（GH_HOST、GH_TOKEN、gh auth login）で GitHub API を呼ぶ Client を作る。
func NewClient(includeTeamReviews bool) (*Client, error) {
	// ホストとトークンを go-gh の各クライアントに任せず先に決めるのは、keyring に保存された
	// トークンを読むための gh auth token の実行を、GraphQL と REST のクライアントで 2 回行わないため。
	host, _ := auth.DefaultHost()
	token, _ := auth.TokenForHost(host)
	if token == "" {
		return nil, fmt.Errorf("no GitHub token for %s: run `gh auth login` or set GH_TOKEN", host)
	}
	// GH_DEBUG が設定されていると go-gh は HTTP のログを stderr に書き、TUI の画面を崩すため読まない。
	return newClient(api.ClientOptions{Host: host, AuthToken: token, LogIgnoreEnv: true}, includeTeamReviews)
}

// newClient は opts で GitHub API のクライアントを作る。テストでは opts.Transport を差し替えて GitHub を呼ばない。
// GraphQL と REST のクライアントは、opts.Transport が無ければ http.DefaultTransport の接続を共有する。
func newClient(opts api.ClientOptions, includeTeamReviews bool) (*Client, error) {
	graphQL, err := api.NewGraphQLClient(opts)
	if err != nil {
		return nil, err
	}
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, err
	}
	return &Client{graphQL: graphQL, rest: rest, includeTeamReviews: includeTeamReviews, pushCache: map[branchKey]branchPushes{}}, nil
}

// queryGraphQL は query を実行し、応答の data と、errors のメッセージ（部分的なエラー）を返す。
func queryGraphQL[T any](ctx context.Context, client *api.GraphQLClient, query string, variables map[string]any) (*T, []string, error) {
	var data *T // 応答の data が null なら nil のまま
	err := client.DoWithContext(ctx, query, variables, &data)
	var graphQLErr *api.GraphQLError
	switch {
	case errors.As(err, &graphQLErr):
		// 権限の無い PR / issue は errors に載り、該当のノードは空になる。
		// 1 件のために全体を失敗させず、data があれば残りの結果を使う。
		if data == nil {
			return nil, nil, err
		}
		// path を付けないのは、同じ理由で取れなかった複数のノードのメッセージを 1 つにまとめるため。
		var msgs []string
		for _, e := range graphQLErr.Errors {
			msgs = append(msgs, e.Message)
		}
		return data, msgs, nil
	case err != nil:
		return nil, nil, describeTimeout(ctx, err)
	case data == nil:
		return nil, nil, errors.New("GraphQL response has no data")
	}
	return data, nil, nil
}

// describeTimeout は、ctx の期限切れで打ち切られた API 呼び出しのエラーを、時間切れと書いたエラーに置き換える。
// http.Client のエラーは `Post "https://...": context deadline exceeded` となり、時間切れだと読み取りにくいため。
func describeTimeout(ctx context.Context, err error) error {
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("GitHub API: timed out: %w", ctx.Err())
	}
	return err
}

// Search は since 以降に更新された、自分が関わる PR / issue を取得する。
// 応答に errors があっても、data があれば部分的な結果として返す。
func (c *Client) Search(ctx context.Context, since time.Time) (SearchResult, error) {
	queries := searchQueries(since, c.includeTeamReviews)
	variables := map[string]any{"first": searchPageSize}
	for name, q := range queries {
		variables[name] = q
	}
	pages, partialErrors, err := queryGraphQL[firstPages](ctx, c.graphQL, firstPagesQuery, variables)
	if err != nil {
		return SearchResult{}, err
	}
	// map の順のまま使わないのは、条件をまたいで重複した PR / issue のどの検索結果を残すかが、ポーリングごとに変わるため
	conns := pages.connections(slices.Sorted(maps.Keys(queries)))
	partialErrors = append(partialErrors, c.fetchRemainingPages(ctx, conns, queries)...)
	result := buildResult(pages.Viewer.Login, conns, partialErrors)
	if numFailed := c.retimeCommitsByPush(ctx, result.Items); numFailed > 0 {
		result.PartialErrors = append(result.PartialErrors, pushTimesUnavailableMessage(numFailed))
	}
	return result, nil
}

// fetchRemainingPages は、1 ページ目に収まらなかった検索条件の続きを maxSearchPages まで取得して conns に足し、
// 取得できなかったときのメッセージを返す。取得できなければその検索条件はそこで止め、取れた分だけを使う。
//
// ページの取得の合間に更新された PR / issue は前のページへ移り、今回は取りこぼすことがある。
// 更新時刻が今回のポーリングの開始より後になるため、次のポーリングの検索範囲に入る。
func (c *Client) fetchRemainingPages(ctx context.Context, conns []namedConnection, queries map[string]string) []string {
	var msgs []string
	for _, nc := range conns {
		conn := nc.conn
		for numPages := 1; numPages < maxSearchPages && conn.IssueCount > len(conn.Nodes) && conn.PageInfo.HasNextPage; numPages++ {
			page, pageErrors, err := queryGraphQL[nextPage](ctx, c.graphQL, nextPageQuery, map[string]any{
				"first": searchPageSize, "searchQuery": queries[nc.name], "after": conn.PageInfo.EndCursor,
			})
			msgs = append(msgs, pageErrors...)
			if err != nil {
				msgs = append(msgs, fmt.Sprintf("more %s results unavailable: %v", nc.name, err))
				break
			}
			conn.Nodes = append(conn.Nodes, page.Search.Nodes...)
			conn.PageInfo = page.Search.PageInfo
		}
	}
	return msgs
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
			pushesByItem[i], errs[i] = fetchPushes(ctx, c.rest, items[i].headRepo, items[i].headRef)
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

// searchQueries は first_pages.graphql の変数名ごとの検索クエリを返す。変数名は、応答の data で
// 検索結果に付く別名も兼ねる。検索条件を足すときは first_pages.graphql にも足す。
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
