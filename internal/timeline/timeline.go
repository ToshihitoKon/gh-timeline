// Package timeline は検索結果を表示済みの内容と突き合わせ、新しい更新だけを取り出す。
package timeline

import (
	"sort"
	"time"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
)

// searchLagOverlap は前回のポーリング開始時刻からさかのぼって検索する幅。
//
// GitHub の検索インデックスは更新から数分遅れて反映されることがあり、
// 前回の開始時刻ちょうどから検索すると、その間に更新された PR / issue を取りこぼす。
// 重複して取れた分は Tracker が表示済みの updatedAt と比べて捨てるため、
// 幅を広めに取っても二重に表示されることはない。
const searchLagOverlap = 10 * time.Minute

// Entry は 1 回のポーリングで見つかった、1 つの PR / issue の更新。
type Entry struct {
	Item github.Item
	// Events は前回表示した時点より後のイベント（古い順）。
	// タイトル・本文の編集やラベル変更のように、取得対象外の操作だけで
	// 更新された場合は空になる。
	Events []github.Event
}

// LatestAt は Entry の中で最も新しい出来事の時刻。
func (e Entry) LatestAt() time.Time {
	if n := len(e.Events); n > 0 && e.Events[n-1].At.After(e.Item.UpdatedAt) {
		return e.Events[n-1].At
	}
	return e.Item.UpdatedAt
}

// IsOnlyBy は Entry のイベントがすべて login によるものかを返す。
// イベントが無い場合は誰の操作か分からないため false。
func (e Entry) IsOnlyBy(login string) bool {
	if len(e.Events) == 0 {
		return false
	}
	for _, ev := range e.Events {
		if ev.Actor != login {
			return false
		}
	}
	return true
}

// Batch は 1 回のポーリングで見つかった更新のまとまり。
type Batch struct {
	CheckedAt time.Time
	Entries   []Entry // 古い順
}

// Tracker は表示済みの更新を覚えておき、新しい更新だけを Batch にする。
type Tracker struct {
	lookback time.Duration
	// seen は PR / issue ごとに、表示済みの updatedAt。
	seen map[string]time.Time
	// lastStartedAt は直近に成功したポーリングの開始時刻。
	// 失敗したポーリングでは進めないため、次の検索範囲が失敗した回の分も含む。
	lastStartedAt time.Time
}

// NewTracker は Tracker を作る。lookback は初回のポーリングでさかのぼる幅。
func NewTracker(lookback time.Duration) *Tracker {
	return &Tracker{lookback: lookback, seen: map[string]time.Time{}}
}

// Since は now に開始するポーリングで検索する範囲の始点を返す。
func (t *Tracker) Since(now time.Time) time.Time {
	if t.lastStartedAt.IsZero() {
		return now.Add(-t.lookback)
	}
	return t.lastStartedAt.Add(-searchLagOverlap)
}

// Apply は startedAt に開始し since 以降を検索した結果から、新しい更新を取り出す。
func (t *Tracker) Apply(startedAt, since time.Time, items []github.Item) Batch {
	batch := Batch{CheckedAt: startedAt}
	for _, item := range items {
		key := item.Key()
		// 初めて見る PR / issue は検索範囲の始点より後のイベントを、
		// 表示済みのものは前回表示した updatedAt より後のイベントを出す。
		lowerBound := since
		if seenAt, ok := t.seen[key]; ok {
			if !item.UpdatedAt.After(seenAt) {
				continue
			}
			lowerBound = seenAt
		}
		t.seen[key] = item.UpdatedAt
		batch.Entries = append(batch.Entries, Entry{Item: item, Events: eventsAfter(item.Events, lowerBound)})
	}
	sort.SliceStable(batch.Entries, func(i, j int) bool {
		return batch.Entries[i].LatestAt().Before(batch.Entries[j].LatestAt())
	})
	t.lastStartedAt = startedAt
	return batch
}

func eventsAfter(events []github.Event, t time.Time) []github.Event {
	var after []github.Event
	for _, ev := range events {
		if ev.At.After(t) {
			after = append(after, ev)
		}
	}
	return after
}
