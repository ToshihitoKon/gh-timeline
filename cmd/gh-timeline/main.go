// Command gh-timeline は自分が関わる GitHub の PR / issue の更新を時系列で流す TUI。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
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

// minInterval はポーリング間隔の下限。GraphQL の検索は 1 回で 4 クエリ分を消費し、
// 検索 API には分単位のレート制限もあるため、極端に短い間隔を指定できないようにする。
const minInterval = 30 * time.Second

func main() {
	interval := flag.Duration("interval", 5*time.Minute, "polling interval")
	lookback := flag.Duration("since", 30*time.Minute, "how far back to look on the first poll")
	includeTeamReviews := flag.Bool("team-reviews", false, "also follow PRs whose review is requested from your teams")
	dump := flag.Bool("dump", false, "print updates once and exit, without starting the TUI")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(resolveVersion())
		return
	}
	if *interval < minInterval {
		fmt.Fprintf(os.Stderr, "gh-timeline: -interval must be at least %s\n", minInterval)
		os.Exit(2)
	}
	if _, err := exec.LookPath("gh"); err != nil {
		fmt.Fprintln(os.Stderr, "gh-timeline: gh command not found. Install GitHub CLI and run `gh auth login`.")
		os.Exit(1)
	}

	client := github.NewClient(github.ExecGH, *includeTeamReviews)
	tracker := timeline.NewTracker(*lookback)

	if *dump {
		if err := runDump(client, tracker); err != nil {
			fmt.Fprintln(os.Stderr, "gh-timeline:", err)
			os.Exit(1)
		}
		return
	}

	p := tea.NewProgram(ui.NewModel(client, tracker, *interval))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gh-timeline:", err)
		os.Exit(1)
	}
}

// runDump は TUI を介さず、初回ポーリングと同じ内容をテキストで出力する。
func runDump(client *github.Client, tracker *timeline.Tracker) error {
	now := time.Now()
	since := tracker.Since(now)
	result, err := client.Search(context.Background(), since)
	if err != nil {
		return err
	}
	batch := tracker.Apply(now, since, result.Items)
	if len(batch.Entries) == 0 {
		fmt.Printf("No updates since %s.\n", since.Format("01/02 15:04"))
		return nil
	}
	fmt.Println(ui.RenderBatch(batch, time.Time{}, result.Viewer, 0))
	if result.Truncated {
		fmt.Println("(some results were truncated: over 100 hits)")
	}
	return nil
}
