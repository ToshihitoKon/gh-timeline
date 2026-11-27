// Command gh-timeline は自分が関わる GitHub の PR / issue の更新を時系列で流す TUI。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
	"github.com/ToshihitoKon/gh-timeline/internal/ui"
)

// version は GoReleaser がビルド時に -ldflags "-X main.version=..." でタグ名（v0.1.0 など）を埋め込む。
var version = "dev"

// resolveVersion は表示するバージョンを返す。
func resolveVersion() string {
	info, _ := debug.ReadBuildInfo()
	return pickVersion(version, info)
}

// pickVersion は ldflags で埋め込まれた版を優先する。go install でビルドした場合は ldflags が
// 渡らず "dev" のままになるため、Go が埋め込むモジュールのバージョン（v0.1.0 など）を使う。
// info は build info を読めなかったとき nil。
func pickVersion(embedded string, info *debug.BuildInfo) string {
	if embedded != "dev" {
		return embedded
	}
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return embedded
}

// minInterval はポーリング間隔の下限。1 回のポーリングの GraphQL のコストはふつう 12 ポイントだが、
// どの検索条件も github.MaxSearchHits まで続きのページを取ると 48 ポイントになり、下限はこの場合で決めている。
// 30 秒間隔では 1 時間に 5,760 ポイントとなり、上限の 5,000 ポイントを超えて同じトークンを使う
// 他の gh コマンドまで止まる。1 分間隔なら 2,880 ポイントで、他の gh コマンドの分が残る。
const minInterval = time.Minute

func main() {
	interval := flag.Duration("interval", 5*time.Minute, "polling interval")
	lookback := flag.Duration("since", 30*time.Minute, "how far back to look on the first poll")
	includeTeamReviews := flag.Bool("team-reviews", false, "also follow PRs whose review is requested from your teams")
	showURL := flag.Bool("show-url", false, "show the URL under each pull request and issue")
	dump := flag.Bool("dump", false, "print updates once and exit, without starting the TUI")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(resolveVersion())
		return
	}
	if err := checkFlags(*interval, *lookback); err != nil {
		fmt.Fprintln(os.Stderr, "gh-timeline:", err)
		os.Exit(2)
	}
	client, err := github.NewClient(*includeTeamReviews)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gh-timeline:", err)
		os.Exit(1)
	}
	tracker := timeline.NewTracker(time.Now().Add(-*lookback))

	if *dump {
		if err := runDump(os.Stdout, os.Stderr, client, tracker, time.Now(), *showURL, timeline.FetchTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "gh-timeline:", err)
			os.Exit(1)
		}
		return
	}

	// マウスイベントを取る WithMouseCellMotion ではターミナルのドラッグ選択が効かなくなるため、
	// alternate screen に入るだけにして、ホイールを ↑/↓ キーに変換するターミナルの機能に任せる。
	p := tea.NewProgram(ui.NewModel(client, tracker, *interval, *showURL), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gh-timeline:", err)
		os.Exit(1)
	}
}

// checkFlags はフラグの値が受け付けられる範囲にあるかを確かめる。
func checkFlags(interval, lookback time.Duration) error {
	if interval < minInterval {
		return fmt.Errorf("-interval must be at least %s", minInterval)
	}
	if lookback < 0 {
		return errors.New("-since must not be negative")
	}
	return nil
}

// runDump は TUI を介さず、startedAt に始めた初回ポーリングと同じ内容をテキストで出力する。
// 更新と取りこぼしの警告を stdout に、結果の一部を取得できなかったことを stderr に出す。
func runDump(stdout, stderr io.Writer, fetcher timeline.Fetcher, tracker *timeline.Tracker, startedAt time.Time, showURL bool, timeout time.Duration) error {
	poll := tracker.StartPoll(startedAt)
	result, err := poll.Fetch(fetcher, timeout)
	if err != nil {
		// 時間切れは github.Client が "timed out" と書いて返すため、ここで包み直さない。
		return err
	}
	batch := tracker.Apply(poll, result.Items)
	if warning := timeline.MissingResultsWarning(result); warning != "" {
		fmt.Fprintln(stderr, "gh-timeline:", warning)
	}
	if len(batch.Entries) == 0 {
		fmt.Fprintln(stdout, ui.NoUpdatesText(poll.Since))
	} else {
		fmt.Fprintln(stdout, ui.RenderBatches([]timeline.Batch{batch}, 0, ui.RenderOptions{Viewer: result.Viewer, ShowURL: showURL}))
	}
	if warning := timeline.TruncatedWarning(result); warning != "" {
		fmt.Fprintf(stdout, "(%s)\n", warning)
	}
	return nil
}
