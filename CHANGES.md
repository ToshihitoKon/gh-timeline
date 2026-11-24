# Changes

Breaking changes and how to migrate. Other changes are listed in the
[release notes](https://github.com/ToshihitoKon/gh-timeline/releases).

## Unreleased

- `Space` now marks everything shown as read, and every timeline line in the TUI starts with a one-column gutter that shows `▎` for unread blocks.
  - Before: `Space` scrolled down one page, and timeline lines started at the left edge.
  - After: `Space` marks everything as read, and timeline lines start one column to the right. `-dump` output is unchanged.
  - Migration: to scroll down one page, use `PgDn` or `f`.

## v0.2.0

- The timeline (in the TUI and `-dump`) now groups events by pull request or issue within each `--- HH:MM ---` block.
  - Before: one line per event, `14:01 owner/repo PR#130 review requested dave → you, erin Fix typo`
  - After: a heading `owner/repo PR#130 Fix typo`, followed by indented event lines `  14:01 review requested dave → you, erin`
  - Migration: if you parse `-dump` output, read the repository, number, and title from the heading line, and the time, action, and user from the indented lines below it.

## v0.1.0

- First release.
