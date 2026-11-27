package timeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
)

// FetchTimeout は 1 回のポーリングで GitHub API の応答を待つ上限。
// 4 つの検索をまとめた GraphQL クエリは通常数秒で返るが、ネットワーク断などで
// 応答が戻らないとポーリングの自己クロックが止まってしまうため、間隔（既定 5 分）より
// 十分短い値で打ち切る。
const FetchTimeout = 60 * time.Second

// Fetcher は自分が関わる PR / issue を検索する。テストで差し替える。
type Fetcher interface {
	Search(ctx context.Context, since time.Time) (github.SearchResult, error)
}

// Poll は 1 回のポーリングの開始時刻と検索範囲の始点。
// TUI も -dump も、StartPoll で作り、Fetch で検索し、同じ値を Apply に渡す。
type Poll struct {
	StartedAt time.Time
	Since     time.Time
}

// StartPoll は startedAt に始めるポーリングの検索範囲を決める。
func (t *Tracker) StartPoll(startedAt time.Time) Poll {
	return Poll{StartedAt: startedAt, Since: t.Since()}
}

// Fetch は p の検索範囲を検索し、timeout で打ち切る。
func (p Poll) Fetch(fetcher Fetcher, timeout time.Duration) (github.SearchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fetcher.Search(ctx, p.Since)
}

// MissingResultsWarning は、結果の一部を取得できなかったことを知らせる文言を返す。
// 取得できなかったものが無ければ空文字列を返す。
func MissingResultsWarning(result github.SearchResult) string {
	if len(result.PartialErrors) == 0 {
		return ""
	}
	return "some results are missing: " + strings.Join(result.PartialErrors, "; ")
}

// TruncatedWarning は、検索の上限件数を超えて取りこぼしたことを知らせる文言を返す。
// 取りこぼしが無ければ空文字列を返す。
func TruncatedWarning(result github.SearchResult) string {
	if !result.Truncated {
		return ""
	}
	return fmt.Sprintf("some results were truncated: over %d hits", github.MaxSearchHits)
}
