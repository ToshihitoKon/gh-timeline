// Command gh-timeline は自分が関わる GitHub の PR / issue の更新を時系列で流す TUI。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ToshihitoKon/gh-timeline/internal/github"
	"github.com/ToshihitoKon/gh-timeline/internal/timeline"
	"github.com/ToshihitoKon/gh-timeline/internal/ui"
)

// version は GoReleaser がビルド時に -ldflags "-X main.version=..." で埋め込む。
var version = "dev"

// resolveVersion は表示するバージョンを返す。go install でビルドした場合は ldflags が
// 渡らず "dev" のままになるため、Go が埋め込むモジュールのバージョン（v0.1.0 など）を使う。
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

// minInterval はポーリング間隔の下限。1 回のポーリングの GraphQL のコストは 48 ポイントで、
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
	if _, err := exec.LookPath("gh"); err != nil {
		fmt.Fprintln(os.Stderr, "gh-timeline: gh command not found. Install GitHub CLI and run `gh auth login`.")
		os.Exit(1)
	}

	client := github.NewClient(github.ExecGH, *includeTeamReviews)
	tracker := timeline.NewTracker(time.Now().Add(-*lookback))

	if *dump {
		if err := runDump(client, tracker, *showURL, ui.FetchTimeout); err != nil {
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

// runDump は TUI を介さず、初回ポーリングと同じ内容をテキストで出力する。
func runDump(fetcher ui.Fetcher, tracker *timeline.Tracker, showURL bool, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	now := time.Now()
	since := tracker.Since()
	result, err := fetcher.Search(ctx, since)
	if err != nil {
		// gh が打ち切られたときのエラーは "signal: killed" だけで、時間切れだと読み取れない。
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("gave up after %s: %w", timeout, err)
		}
		return err
	}
	batch := tracker.Apply(now, result.Items)
	if len(result.PartialErrors) > 0 {
		fmt.Fprintln(os.Stderr, "gh-timeline: some results are missing:", strings.Join(result.PartialErrors, "; "))
	}
	if len(batch.Entries) == 0 {
		fmt.Println(ui.NoUpdatesText(since))
		return nil
	}
	fmt.Println(ui.RenderBatches([]timeline.Batch{batch}, 0, ui.RenderOptions{Viewer: result.Viewer, ShowURL: showURL}))
	if result.Truncated {
		fmt.Printf("(some results were truncated: over %d hits)\n", github.SearchPageSize)
	}
	return nil
}
