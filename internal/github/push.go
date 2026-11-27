package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"time"
)

// pushActivityPageSize は 1 つのブランチについて取得する push の件数。
// 突き合わせるコミットは timelineItems の直近 10 件までなので必要な push も 10 件までだが、
// 同じブランチの作成・削除なども同じ一覧に混ざるため余裕を持たせている。
const pushActivityPageSize = 30

// push はブランチへの 1 回の push。
type push struct {
	After string // push 後の head のコミット
	At    time.Time
}

// needsPushTimes は、PR に push の時刻で置き換えるコミットがあり、push を取得できるかを返す。
func needsPushTimes(item Item) bool {
	if item.Kind != KindPullRequest || item.headRepo == "" || item.headRef == "" {
		return false
	}
	return slices.ContainsFunc(item.Events, func(ev Event) bool { return ev.Type == EventCommitted })
}

// fetchPushes は repo の ref ブランチへの push を古い順に返す。
func fetchPushes(ctx context.Context, run Runner, repo, ref string) ([]push, error) {
	query := url.Values{}
	query.Set("ref", "refs/heads/"+ref)
	query.Set("per_page", fmt.Sprint(pushActivityPageSize))
	out, err := run(ctx, "api", fmt.Sprintf("repos/%s/activity?%s", repo, query.Encode()))
	if err != nil {
		return nil, err
	}
	var activities []struct {
		After        string    `json:"after"`
		Timestamp    time.Time `json:"timestamp"`
		ActivityType string    `json:"activity_type"`
	}
	if err := json.Unmarshal(out, &activities); err != nil {
		return nil, fmt.Errorf("parse repository activity: %w", err)
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
	sort.SliceStable(pushes, func(i, j int) bool { return pushes[i].At.Before(pushes[j].At) })
	return pushes, nil
}

// applyPushTimes は、各コミットの時刻を、そのコミットを含む最初の push の時刻に置き換える。
// push 後の head より前にあり、それより前の push に含まれていないコミットを、その push に含まれたとみなす。
//
// コミットの並びは committedDate 順をブランチ上の順として扱う。
// rebase や cherry-pick でも committedDate はその操作の時刻になるため、通常は一致する。
func applyPushTimes(item *Item, pushes []push) {
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
	sort.SliceStable(item.Events, func(i, j int) bool { return item.Events[i].At.Before(item.Events[j].At) })
}
