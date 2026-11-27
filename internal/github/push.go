package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// pushActivityPageSize は 1 つのブランチについて取得する push の件数。
// 突き合わせるコミットは timelineItems の直近 10 件までなので必要な push も 10 件までだが、
// 同じブランチの作成・削除なども同じ一覧に混ざるため余裕を持たせている。
const pushActivityPageSize = 30

// pushFetchConcurrency は push の取得を同時に行う PR の数の上限。
// GitHub は同じ利用者からの同時リクエストを secondary rate limit で制限するため、
// 1 件ずつ取るより速くなる範囲で小さく抑えている。
const pushFetchConcurrency = 4

// push はブランチへの 1 回の push。
type push struct {
	After string // push 後の head のコミット
	At    time.Time
}

// branchKey は push を取得するブランチ。
type branchKey struct {
	repo string // owner/name
	ref  string // ブランチ名
}

func branchOf(item Item) branchKey {
	return branchKey{repo: item.headRepo, ref: item.headRef}
}

// branchPushes は、ブランチの最新のコミットが newestCommit だったときに取得した push。
type branchPushes struct {
	newestCommit string
	pushes       []push
}

// needsPushTimes は、PR に push の時刻で置き換えるコミットがあり、push を取得できるかを返す。
// issue は head ブランチを持たないため、ここで false になる。
func needsPushTimes(item Item) bool {
	if item.headRepo == "" || item.headRef == "" {
		return false
	}
	return slices.ContainsFunc(item.Events, func(ev Event) bool { return ev.Type == EventCommitted })
}

// newestCommitID は PR のコミットのうち committedDate が最も新しいものの oid を返す。
// applyPushTimes と同じく committedDate 順をブランチ上の順として扱い、これを head とみなす。
func newestCommitID(item Item) string {
	for _, ev := range slices.Backward(item.Events) {
		if ev.Type == EventCommitted {
			return ev.ID
		}
	}
	return ""
}

// fetchPushes は repo の ref ブランチへの push を古い順に返す。
func fetchPushes(ctx context.Context, rest *api.RESTClient, repo, ref string) ([]push, error) {
	query := url.Values{}
	query.Set("ref", "refs/heads/"+ref)
	query.Set("per_page", fmt.Sprint(pushActivityPageSize))
	var activities []struct {
		After        string    `json:"after"`
		Timestamp    time.Time `json:"timestamp"`
		ActivityType string    `json:"activity_type"`
	}
	if err := rest.DoWithContext(ctx, http.MethodGet, fmt.Sprintf("repos/%s/activity?%s", repo, query.Encode()), nil, &activities); err != nil {
		return nil, describeTimeout(ctx, err)
	}
	var pushes []push
	for _, a := range activities {
		switch a.ActivityType {
		// git push -u のようにブランチを作った push は branch_creation として記録されるが、
		// コミットを含むため push の境界として扱う。外すと、そのコミットが次の push に数えられる。
		case "push", "force_push", "branch_creation":
			pushes = append(pushes, push{After: a.After, At: a.Timestamp})
		}
	}
	slices.SortStableFunc(pushes, func(a, b push) int { return a.At.Compare(b.At) })
	return pushes, nil
}

// applyPushTimes は、各コミットの時刻を、そのコミットを含む最初の push の時刻に置き換える。
// push 後の head より前にあり、それより前の push に含まれていないコミットを、その push に含まれたとみなす。
//
// コミットの並びは committedDate 順をブランチ上の順として扱う。
// rebase や cherry-pick でも committedDate はその操作の時刻になるため、通常は一致する。
//
// どの push にも割り当てられなかったコミットには IsPushTimeUnknown を立て、すべて置き換えられたかを返す。
func applyPushTimes(item *Item, pushes []push) (isComplete bool) {
	var commits []*Event
	for i := range item.Events {
		if item.Events[i].Type == EventCommitted {
			commits = append(commits, &item.Events[i])
		}
	}
	next := 0 // まだどの push にも割り当てていない最初のコミット
	for _, p := range pushes {
		head := slices.IndexFunc(commits, func(ev *Event) bool { return ev.ID == p.After })
		if head < next {
			continue
		}
		for _, ev := range commits[next : head+1] {
			ev.At = p.At
		}
		next = head + 1
	}
	for _, ev := range commits[next:] {
		ev.IsPushTimeUnknown = true
	}
	sortByTime(item.Events)
	return next == len(commits)
}
