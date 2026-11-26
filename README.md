# gh-timeline

A terminal UI that streams updates on the GitHub pull requests and issues you are involved in.

```text
gh-timeline — @you
checked 14:05 · next 14:10 · every 5m0s
▐ --- 14:05 ---
▐ ● owner/repo IS#45 Something broken
▐ │   14:03 closed carol
▐ ● owner/repo PR#130 Fix typo
▐ │   14:01 review requested dave → you, erin

  --- 14:00 ---
  ● owner/repo PR#123 Add feature
  │   13:59 commented (2) bob
  │   13:58 approved alice
↑/↓: scroll   g/G: top/bottom   space: mark read   r: refresh   q: quit
```

## What it shows

gh-timeline polls GitHub every 5 minutes for pull requests and issues that:

- you opened
- are assigned to you
- request your review (personally; add `-team-reviews` to include your teams)
- you have reviewed

When a poll finds new activity, it adds a `--- HH:MM ---` block at the top of the timeline. Polls with no new activity add nothing. When the date changes, the separator includes it (`--- 11/21 09:00 ---`).

In the TUI, each pull request or issue starts with `●` and its events follow a `│` line. Blocks you have not read yet also have a `▐` at the left edge of every line, and their `●` and `│` are green instead of gray. Press `Space` to mark everything shown as read. What the first successful poll finds on startup starts out as read, so the marks show only what arrived after that.

Within a block, events are grouped by pull request or issue. The one with the newest event comes first, and its events follow newest first:

```text
--- 14:05 ---
owner/repo PR#130 Fix typo
  14:03 approved erin
  14:01 review requested dave → you, erin
owner/repo IS#42 Crash on startup
  13:58 commented frank
```

- `owner/repo PR#130 Fix typo`: the repository, number (`IS#` for issues), and title. The number is colored by state: green for open, gray for draft, purple for merged, red for closed. In the TUI, the repository and number are underlined and link to the pull request or issue; click them to open it in your browser (the key to hold while clicking depends on your terminal)
- `14:01`: when it happened (`11/19 23:59` if it was on an earlier day)
- `review requested`: the action
- `dave`: who did it
- `→ you, erin`: who was asked to review or assigned, for those actions

With `-show-url`, the URL follows each heading:

```text
owner/repo PR#130 Fix typo
  https://github.com/owner/repo/pull/130
  14:03 approved erin
```

Consecutive events of the same kind by the same person are combined, such as `commented (3)` or `→ bob, carol`. Your own actions are dimmed, and so is the heading when all of its events are yours.

Actions:

- `commented`, `approved`, `changes requested`, `reviewed`
- `pushed N commits`, `force-pushed`
- `merged`, `closed`, `reopened`
- `review requested`, `assigned`
- `ready for review`, `converted to draft`
- `updated`: other changes such as title, body, or label edits

## Install

Install the [GitHub CLI](https://cli.github.com/) and run `gh auth login` first. gh-timeline uses `gh` for authentication.

Install it as a gh extension:

```sh
gh extension install ToshihitoKon/gh-timeline
```

Upgrade with `gh extension upgrade timeline`.

You can also install it as a standalone command with Go. In that case, run `gh-timeline` instead of `gh timeline` in the examples below.

```sh
go install github.com/ToshihitoKon/gh-timeline/cmd/gh-timeline@latest
```

## Usage

```sh
gh timeline
```

On startup it shows activity from the last 30 minutes, then checks for new activity every 5 minutes.

```sh
# Look back 24 hours on startup and check every 2 minutes
gh timeline -since 24h -interval 2m

# Include review requests sent to your teams
gh timeline -team-reviews

# Print the current activity once without starting the TUI
gh timeline -dump
```

### Flags

- `-interval`: how often to check for updates (default `5m`, minimum `30s`)
- `-since`: how far back to look on startup (default `30m`)
- `-team-reviews`: also follow pull requests whose review is requested from your teams
- `-show-url`: show the URL under each pull request and issue, in both the TUI and `-dump`
- `-dump`: print the first poll once and exit, without starting the TUI
- `-version`: print the version and exit

### Keys

- `↑` / `↓` / `PgUp` / `PgDn`: scroll
  - The mouse wheel also scrolls in terminals that send it as `↑` / `↓` in full-screen apps, as they do for `less`. Text can still be selected by dragging.
- `g` / `G`: jump to the top / bottom
- `Space`: mark everything shown as read
- `r`: check for updates now
- `q` / `Ctrl+C`: quit

While you are at the top, new blocks keep the view at the top. While you are reading older blocks, the view stays where you are.

If a poll fails, the error appears in the footer and the next successful poll also picks up what the failed one missed.

## Tips

### Mouse wheel in tmux

With `set -g mouse on`, tmux does not pass the mouse wheel to full-screen apps that do not use the mouse, such as gh-timeline and `less`. To scroll them with the wheel, make tmux send the wheel as `↑` / `↓`, for example with [tmux-better-mouse-mode](https://github.com/NHDaly/tmux-better-mouse-mode):

```tmux
# Put these above the line that runs tpm
set -g @plugin 'nhdaly/tmux-better-mouse-mode'
set -g @emulate-scroll-for-no-mouse-alternate-buffer "on"
```

### Links in tmux

tmux passes the heading links to your terminal only when the terminal has the `hyperlinks` feature. Add this to `.tmux.conf`:

```tmux
set -as terminal-features ",*:hyperlinks"
```

Then reload the config with `tmux source-file ~/.tmux.conf` and detach and reattach, because tmux sets the features of a terminal when it attaches. `tmux display -p '#{client_termfeatures}'` lists `hyperlinks` once it takes effect. Without it, the headings cannot be clicked.

## Limitations

- Up to 100 results per search condition per poll. A warning appears in the footer if there are more.
- Up to the 10 most recent events per pull request or issue.
- The timeline is kept in memory only. After a restart, it starts again from `-since`.
- Colors are tuned for dark terminal backgrounds.
