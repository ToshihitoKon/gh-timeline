// Package timeline は検索結果を表示済みの内容と突き合わせ、新しい更新だけを取り出す。
package timeline

import (
	"slices"
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
// 応答の updatedAt と一致する保証がないため。始点の直前で捨てた PR / issue が検索に返ってくると、
// 初めて見るものとして扱い、その回の始点以降の表示済みのイベントをもう一度出すことになる。
// 1 時間は searchLagOverlap より十分大きい幅として選んだ。残す件数はポーリング数回分の検索結果程度にとどまる。
const seenRetention = time.Hour

// Entry は 1 回のポーリングで見つかった、1 つの PR / issue の更新。
type Entry struct {
	// Item は Events を空にしたもの。Entry は履歴として残り続けるため、表示しない取得済みのイベントまで持たない。
	Item github.Item
	// Events はまだ表示していないイベント（古い順、1 件以上）。
	// タイトル・本文の編集やラベル変更のように、取得対象外の操作だけで
	// 更新された場合は、updatedAt の時刻の EventUpdated 1 件になる。
	Events []github.Event
}

// shownAt は Entry を並べる時刻。表示するイベントのうち最も新しいものの時刻。
func (e Entry) shownAt() time.Time {
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
	// firstSince は初回のポーリングで検索する範囲の始点。以後のポーリングもこれより前は検索しない。
	firstSince time.Time
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
	// ただし push の時刻が分からず表示しなかったコミットは含めない。後のポーリングで push の時刻が取れ、
	// 始点以降に push されたと分かったときに出すため。
	eventIDs map[string]bool
}

// NewTracker は Tracker を作る。firstSince は初回のポーリングで検索する範囲の始点。
//
// 初回の始点を最初のポーリングの開始時に決めないのは、初回が失敗して取り直すたびに
// 始点が後ろへずれ、失敗していた間の更新を取りこぼすため。
func NewTracker(firstSince time.Time) *Tracker {
	return &Tracker{firstSince: firstSince.Truncate(time.Second), seen: map[string]*seenItem{}}
}

// Since は次のポーリングで検索する範囲の始点を返す。
//
// 検索は updated:>= に秒単位の時刻を渡すため、イベントの時刻と比べる始点も秒に切り詰めて検索と揃える。
func (t *Tracker) Since() time.Time {
	if t.lastStartedAt.IsZero() {
		return t.firstSince
	}
	since := t.lastStartedAt.Add(-searchLagOverlap).Truncate(time.Second)
	// -since が searchLagOverlap より短いと、初回の始点より前の更新を新着として出してしまう
	if since.Before(t.firstSince) {
		return t.firstSince
	}
	return since
}

// Apply は p のポーリングの検索結果から、新しい更新を取り出す。
func (t *Tracker) Apply(p Poll, items []github.Item) Batch {
	batch := Batch{CheckedAt: p.StartedAt}
	for _, item := range items {
		if entry, ok := t.newEntry(item, p.Since); ok {
			batch.Entries = append(batch.Entries, entry)
		}
	}
	slices.SortStableFunc(batch.Entries, func(a, b Entry) int { return b.shownAt().Compare(a.shownAt()) })
	t.forgetBefore(p.Since.Add(-seenRetention))
	t.lastStartedAt = p.StartedAt
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
		isNew := isNewEvent(ev, s)
		if isNew {
			events = append(events, ev)
		}
		if isNew || !ev.IsPushTimeUnknown {
			s.eventIDs[ev.ID] = true
		}
	}
	if isUpdated {
		s.updatedAt = item.UpdatedAt
	}

	if len(events) == 0 {
		if !isUpdated {
			return Entry{}, false
		}
		events = []github.Event{{Type: github.EventUpdated, At: item.UpdatedAt}}
	}
	item.Events = nil
	return Entry{Item: item, Events: events}, true
}

// isNewEvent は ev を新着として出すかを返す。
//
// ID をまだ見ておらず、初めて見たときの検索範囲の始点以降のイベントを出す。
// 表示済みの PR / issue でも始点で絞るのは、取得するのは直近 10 件だけで、コメントの削除や
// force push で消えたコミットの分だけ、それより古いイベントが取得範囲に入ってくるため。
func isNewEvent(ev github.Event, s *seenItem) bool {
	if s.eventIDs[ev.ID] {
		return false
	}
	// After で比べないのは、検索の updated:>= が始点ちょうどに更新された PR / issue も返すため。
	// After では、その PR / issue のイベントが落ちて "updated -" だけが出る。
	return !ev.At.Before(s.firstSince)
}

// forgetBefore は updatedAt が threshold より前の PR / issue の記録を捨てる。
func (t *Tracker) forgetBefore(threshold time.Time) {
	for key, s := range t.seen {
		if s.updatedAt.Before(threshold) {
			delete(t.seen, key)
		}
	}
}
