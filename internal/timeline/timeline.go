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
// 重複して取れた分は Tracker が表示済みのイベントの ID と比べて捨てるため、
// 幅を広めに取っても二重に表示されることはない。
const searchLagOverlap = 10 * time.Minute

// seenRetention は、検索範囲の始点よりどれだけ前に更新された PR / issue まで表示済みの記録を残すか。
//
// 始点ちょうどで捨てないのは、検索の updated: が検索インデックス上の時刻で絞り込み、
// 応答の updatedAt と一致する保証がないため。始点の直前で捨てた PR / issue が更新されないまま
// 検索に返ってくると、初めて見るものとして push の時刻を取り直すことになる。
// 1 時間は searchLagOverlap より十分大きい幅として選んだ。残す件数はポーリング数回分の検索結果程度にとどまる。
const seenRetention = time.Hour

// Entry は 1 回のポーリングで見つかった、1 つの PR / issue の更新。
type Entry struct {
	Item github.Item
	// Events はまだ表示していないイベント（古い順）。
	// タイトル・本文の編集やラベル変更のように、取得対象外の操作だけで
	// 更新された場合は空になる。
	Events []github.Event
}

// shownAt は Entry を並べる時刻。表示するイベントのうち最も新しいものの時刻で、
// イベントが無ければ updatedAt。
func (e Entry) shownAt() time.Time {
	if len(e.Events) == 0 {
		return e.Item.UpdatedAt
	}
	latest := e.Events[0].At
	for _, ev := range e.Events[1:] {
		if ev.At.After(latest) {
			latest = ev.At
		}
	}
	return latest
}

// Batch は 1 回のポーリングで見つかった更新のまとまり。
type Batch struct {
	CheckedAt time.Time
	Entries   []Entry // 表示するイベントの新しい順
}

// Tracker は表示済みの更新を覚えておき、新しい更新だけを Batch にする。
type Tracker struct {
	lookback time.Duration
	// seen は PR / issue の Key ごとの、表示済みの内容。
	seen map[string]*seenItem
	// lastStartedAt は直近に成功したポーリングの開始時刻。
	// 失敗したポーリングでは進めないため、次の検索範囲が失敗した回の分も含む。
	lastStartedAt time.Time
}

// seenItem は 1 つの PR / issue について、表示済みの内容。
type seenItem struct {
	// firstSince は、初めて見たポーリングの検索範囲の始点。
	firstSince time.Time
	// updatedAt はこれまでに見た updatedAt の最大値。
	// イベントを伴わない更新（タイトルの編集など）を見分けるためだけに使う。
	updatedAt time.Time
	// eventIDs は取得したイベントの ID。検索範囲の始点より前で表示しなかったものも含め、
	// 次のポーリングで新着として出さないようにする。
	eventIDs map[string]bool
}

// NewTracker は Tracker を作る。lookback は初回のポーリングでさかのぼる幅。
func NewTracker(lookback time.Duration) *Tracker {
	return &Tracker{lookback: lookback, seen: map[string]*seenItem{}}
}

// Since は now に開始するポーリングで検索する範囲の始点を返す。
func (t *Tracker) Since(now time.Time) time.Time {
	if t.lastStartedAt.IsZero() {
		return now.Add(-t.lookback)
	}
	return t.lastStartedAt.Add(-searchLagOverlap)
}

// SeenKeys は表示済みの PR / issue の Key を返す。
// 検索は別の goroutine で行うため、Tracker の状態を共有せず複製を渡す。
func (t *Tracker) SeenKeys() map[string]bool {
	keys := make(map[string]bool, len(t.seen))
	for key := range t.seen {
		keys[key] = true
	}
	return keys
}

// Apply は startedAt に開始したポーリングの検索結果から、新しい更新を取り出す。
// 検索範囲の始点は Since(startedAt) とみなす。
func (t *Tracker) Apply(startedAt time.Time, items []github.Item) Batch {
	since := t.Since(startedAt)
	batch := Batch{CheckedAt: startedAt}
	for _, item := range items {
		if entry, ok := t.newEntry(item, since); ok {
			batch.Entries = append(batch.Entries, entry)
		}
	}
	sort.SliceStable(batch.Entries, func(i, j int) bool {
		return batch.Entries[i].shownAt().After(batch.Entries[j].shownAt())
	})
	t.forgetBefore(since.Add(-seenRetention))
	t.lastStartedAt = startedAt
	return batch
}

// newEntry は item のまだ表示していない更新を Entry にし、表示済みとして覚える。
// 表示する更新が無ければ ok=false を返す。
func (t *Tracker) newEntry(item github.Item, since time.Time) (entry Entry, ok bool) {
	key := item.Key()
	s, isSeen := t.seen[key]
	if !isSeen {
		s = &seenItem{firstSince: since, eventIDs: map[string]bool{}}
		t.seen[key] = s
	}
	isUpdated := !isSeen || item.UpdatedAt.After(s.updatedAt)

	var events []github.Event
	for _, ev := range item.Events {
		if isNewEvent(ev, s, isSeen) {
			events = append(events, ev)
		}
		s.eventIDs[ev.ID] = true
	}
	if isUpdated {
		s.updatedAt = item.UpdatedAt
	}

	if len(events) == 0 && !isUpdated {
		return Entry{}, false
	}
	return Entry{Item: item, Events: events}, true
}

// isNewEvent は ev を新着として出すかを返す。
//
// 初めて見る PR / issue は検索範囲の始点より後のイベントを出す。
// 表示済みのものは、時刻によらず ID をまだ見ていないイベントを出す。手元で前にコミットして後から push した
// コミットは committedDate が前回の表示より古くなり、時刻では取りこぼすため。
//
// ただしコミット以外は、初めて見たときの始点より後のものに限る。取得するのは直近 10 件だけなので、
// コメントの削除や force push で消えたコミットの分だけ、それより古いイベントが取得範囲に入ってくる。
// コミット以外の時刻は GitHub が記録した時刻で、後から古い時刻のものが現れることはない。
func isNewEvent(ev github.Event, s *seenItem, isSeen bool) bool {
	if !isSeen {
		return ev.At.After(s.firstSince)
	}
	if s.eventIDs[ev.ID] {
		return false
	}
	return ev.Type == github.EventCommitted || ev.At.After(s.firstSince)
}

// forgetBefore は updatedAt が threshold より前の PR / issue の記録を捨てる。
func (t *Tracker) forgetBefore(threshold time.Time) {
	for key, s := range t.seen {
		if s.updatedAt.Before(threshold) {
			delete(t.seen, key)
		}
	}
}
