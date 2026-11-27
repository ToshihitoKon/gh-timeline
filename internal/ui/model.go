// Package ui は bubbletea を使った TUI 表示を提供する。
//
// 画面は上から順に、タイトル・状態行・タイムライン（viewport）・フッタの 4 段。
// タイムラインは追記専用で、ポーリングで更新が見つかるたびに
// "--- HH:MM ---" の区切り行から始まるまとまりを先頭に足していく。
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
)

// FetchTimeout は 1 回のポーリングで gh コマンドを待つ上限。
// 4 つの検索をまとめた GraphQL クエリは通常数秒で返るが、ネットワーク断などで
// gh が戻らないとポーリングの自己クロックが止まってしまうため、間隔（既定 5 分）より
// 十分短い値で打ち切る。
const FetchTimeout = 60 * time.Second

// spinnerFrameInterval はスピナーの 1 フレームあたりの時間（cc-dashboard と同じ 3fps）。
const spinnerFrameInterval = time.Second / 3

// chromeLines は viewport 以外に使う行数（タイトル・状態行・フッタ）。
const chromeLines = 3

// Fetcher は自分が関わる PR / issue を検索する。テストで差し替える。
type Fetcher interface {
	Search(ctx context.Context, since time.Time) (github.SearchResult, error)
}

type pollTickMsg struct{ seq int }

type fetchedMsg struct {
	startedAt time.Time
	since     time.Time
	result    github.SearchResult
	err       error
}

// Model は TUI 全体の状態。
type Model struct {
	fetcher  Fetcher
	tracker  *timeline.Tracker
	interval time.Duration
	showURL  bool

	viewer        string
	batches       []timeline.Batch
	watchingSince time.Time // 初回ポーリングでさかのぼった始点。更新が無いときの案内に使う
	lastCheckedAt time.Time
	nextPollAt    time.Time
	lastErr       error
	partialErrors []string
	isTruncated   bool
	isFetching    bool

	// readBatchCount は batches の古い側から何件を既読にしたか。既読にする操作は
	// 「表示中の最新まで」の一種類だけなので、Batch ごとに既読かどうかを持たずに済む。
	readBatchCount int
	hasFetched     bool // 取得に一度でも成功したか。初回に取得した分を既読にするために使う

	// pollSeq は予約中のポーリングを識別する番号。r キーで即時に取得したとき、
	// それ以前に予約した tick を無効にしてポーリングが二重に走らないようにする。
	pollSeq int

	viewport viewport.Model
	spinner  spinner.Model
	ready    bool // 最初の WindowSizeMsg を受け取るまで viewport は使えない
	width    int
}

// NewModel は Model を作る。showURL が true なら PR / issue の見出し行の次に URL の行を出す。
func NewModel(fetcher Fetcher, tracker *timeline.Tracker, interval time.Duration, showURL bool) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{
		Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
		FPS:    spinnerFrameInterval,
	}
	return Model{fetcher: fetcher, tracker: tracker, interval: interval, showURL: showURL, spinner: sp}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(func() tea.Msg { return pollTickMsg{seq: 0} }, m.spinner.Tick)
}

// startFetch は検索を開始する。検索範囲は tracker から開始時点で決め、
// Apply まで含めて tracker を触るのは Update（メインの goroutine）の中だけにする。
func (m *Model) startFetch(now time.Time) tea.Cmd {
	m.isFetching = true
	since := m.tracker.Since(now)
	if m.watchingSince.IsZero() {
		m.watchingSince = since
	}
	fetcher := m.fetcher
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
		defer cancel()
		result, err := fetcher.Search(ctx, since)
		return fetchedMsg{startedAt: now, since: since, result: result, err: err}
	}
}

// scheduleNextPoll は次のポーリングを予約する。固定周期の tick ではなく、
// 直前の取得が終わってから次を予約する自己クロック方式にして、
// 取得が長引いてもポーリングが積み上がらないようにしている。
func (m *Model) scheduleNextPoll(now time.Time) tea.Cmd {
	m.pollSeq++
	seq := m.pollSeq
	m.nextPollAt = now.Add(m.interval)
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return pollTickMsg{seq: seq} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		height := max(msg.Height-chromeLines, 1)
		if !m.ready {
			m.viewport = viewport.New(msg.Width, height)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = height
		}
		m.refreshContent()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			if m.isFetching {
				return m, nil
			}
			m.pollSeq++ // 予約済みの tick を無効にする
			return m, m.startFetch(time.Now())
		case " ":
			// viewport の既定ではスペースが 1 画面下へのスクロールに割り当たっているため、
			// viewport に渡さずここで止める。
			m.readBatchCount = len(m.batches)
			m.refreshContent()
			return m, nil
		case "G", "end":
			m.viewport.GotoBottom()
			return m, nil
		case "g", "home":
			m.viewport.GotoTop()
			return m, nil
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case pollTickMsg:
		if msg.seq != m.pollSeq || m.isFetching {
			return m, nil
		}
		return m, m.startFetch(time.Now())

	case fetchedMsg:
		m.isFetching = false
		m.lastCheckedAt = msg.startedAt
		m.lastErr = msg.err
		if msg.err == nil {
			m.viewer = msg.result.Viewer
			m.isTruncated = msg.result.Truncated
			m.partialErrors = msg.result.PartialErrors
			// 部分的な結果でも検索範囲を進める。SAML SSO のように毎回同じ PR / issue が取れない場合、
			// 範囲を止めると広がり続け、やがて検索の上限件数に達するため。
			batch := m.tracker.Apply(msg.startedAt, msg.since, msg.result.Items)
			hasUpdates := len(batch.Entries) > 0
			if hasUpdates {
				m.batches = append(m.batches, batch)
			}
			if !m.hasFetched {
				m.readBatchCount = len(m.batches)
				m.hasFetched = true
			}
			// 初回の取得では更新が無くても「Loading...」を「No updates since ...」に
			// 置き換える必要があるため、更新の有無にかかわらず描き直す。
			m.refreshContent()
		}
		return m, m.scheduleNextPoll(time.Now())

	case spinner.TickMsg:
		// スピナーは状態行（View で毎回組み立てる）にしか出さないため、
		// viewport の内容を作り直す必要はない。tick のチェーンは取得中かどうかに
		// かかわらず継続させ、再起動の経路を持たずに済むようにしている。
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// refreshContent は viewport の内容を作り直す。先頭を表示していた場合は
// 追記後も先頭に追従させる。過去の分を読んでいる最中は、先頭に足された行数だけ
// オフセットをずらし、読んでいた行が画面上で動かないようにする。
//
// 行数の差をそのまま先頭への追加分とみなせるのは、追記が先頭にしか起きず、
// 端末幅の変更では行を折り返さず切り詰めるため行数が変わらないから。
func (m *Model) refreshContent() {
	if !m.ready {
		return
	}
	wasAtTop := m.viewport.AtTop()
	prevLines := m.viewport.TotalLineCount()
	prevOffset := m.viewport.YOffset
	m.viewport.SetContent(m.renderTimeline())
	if wasAtTop {
		m.viewport.GotoTop()
		return
	}
	m.viewport.SetYOffset(prevOffset + m.viewport.TotalLineCount() - prevLines)
}

func (m Model) renderTimeline() string {
	if len(m.batches) == 0 {
		if m.watchingSince.IsZero() {
			return faintStyle.Render("Loading...")
		}
		return faintStyle.Render(fmt.Sprintf("No updates since %s.", m.watchingSince.Format("01/02 15:04")))
	}
	return RenderBatches(m.batches, m.readBatchCount, RenderOptions{
		Viewer:        m.viewer,
		Width:         m.width,
		ShowURL:       m.showURL,
		UseHyperlinks: true,
	})
}

func (m Model) View() string {
	if !m.ready {
		return "starting...\n"
	}
	viewer := "..."
	if m.viewer != "" {
		viewer = "@" + m.viewer
	}
	title := lipgloss.NewStyle().Bold(true).Render("gh-timeline — " + viewer)
	return title + "\n" + m.fitWidth(m.statusLine()) + "\n" + m.viewport.View() + "\n" + m.fitWidth(m.footer())
}

// fitWidth は折り返しで viewport の高さの計算がずれないよう、行を端末幅で切り詰める。
func (m Model) fitWidth(s string) string {
	return ansi.Truncate(s, m.width, "…")
}

func (m Model) statusLine() string {
	if m.isFetching {
		return m.spinner.View() + faintStyle.Render(" checking...")
	}
	if m.lastCheckedAt.IsZero() {
		return ""
	}
	text := fmt.Sprintf("checked %s · next %s · every %s",
		m.lastCheckedAt.Format("15:04"), m.nextPollAt.Format("15:04"), m.interval)
	return faintStyle.Render(text)
}

func (m Model) footer() string {
	keys := faintStyle.Render("↑/↓: scroll   g/G: top/bottom   space: mark read   r: refresh   q: quit")
	// エラーと警告はキー案内より前に出す。後ろに付けると、80 桁の端末ではキー案内だけで
	// 幅をほぼ使い切り、切り詰めで本文が消えてしまうため。
	switch {
	case m.lastErr != nil:
		// 失敗した回は tracker の検索範囲を進めないので、次の成功時にまとめて取り直される。
		return closedStyle.Render("error: "+oneLine(m.lastErr.Error())) + "   " + keys
	case len(m.partialErrors) > 0:
		return draftStyle.Render("some results are missing: "+oneLine(strings.Join(m.partialErrors, "; "))) + "   " + keys
	case m.isTruncated:
		return draftStyle.Render("some results were truncated (over 100 hits)") + "   " + keys
	}
	return keys
}

// oneLine は、複数行になりうるメッセージ（gh の stderr など）をフッタの 1 行に収める。
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
