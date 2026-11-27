# Changes

Entries from v0.3.0 on are written by [release-please](https://github.com/googleapis/release-please) from pull request titles. Breaking changes and how to migrate are listed under "⚠ BREAKING CHANGES". Entries up to v0.2.0 list only breaking changes.

## [0.4.1](https://github.com/ToshihitoKon/gh-timeline/compare/v0.4.0...v0.4.1) (2026-11-27)


### Bug Fixes

* show replies to review threads ([#12](https://github.com/ToshihitoKon/gh-timeline/issues/12)) ([820344f](https://github.com/ToshihitoKon/gh-timeline/commit/820344ff7502c23fe35b8dbdd111594d3bf6caa5))
* time pushed commits by when they were pushed ([#15](https://github.com/ToshihitoKon/gh-timeline/issues/15)) ([84a4606](https://github.com/ToshihitoKon/gh-timeline/commit/84a460635a0ceffb1aa10a178427f9b7b3775024))

## [0.4.0](https://github.com/ToshihitoKon/gh-timeline/compare/v0.3.0...v0.4.0) (2026-11-26)


### Features

* link each pull request and issue to its URL ([#10](https://github.com/ToshihitoKon/gh-timeline/issues/10)) ([c969a69](https://github.com/ToshihitoKon/gh-timeline/commit/c969a695df6b7ebfc75a7880109b910bb82bf9da))
* **ui:** scroll with the mouse wheel ([#8](https://github.com/ToshihitoKon/gh-timeline/issues/8)) ([af38490](https://github.com/ToshihitoKon/gh-timeline/commit/af384906cb3e789af0dcdd806bb983b6b3d34322))

## [0.3.0](https://github.com/ToshihitoKon/gh-timeline/compare/v0.2.0...v0.3.0) (2026-11-24)


### Features

* **ui:** add a left-edge gutter that marks unread blocks ([cad54e2](https://github.com/ToshihitoKon/gh-timeline/commit/cad54e2da49ceb178c4c0ffe892dfa20b26b4d87))
* **ui:** draw items and events as a graph in the gutter with an unread marker ([e93fbeb](https://github.com/ToshihitoKon/gh-timeline/commit/e93fbeb8d419815aa4bb81ed6b008747030469cd))
* **ui:** mark all blocks as read with space ([c0c230b](https://github.com/ToshihitoKon/gh-timeline/commit/c0c230bcaed500ea570b0d50d182d663ac21a9a6))


### Bug Fixes

* **ui:** show "No updates" when the first fetch finds nothing ([#6](https://github.com/ToshihitoKon/gh-timeline/issues/6)) ([5c80171](https://github.com/ToshihitoKon/gh-timeline/commit/5c801712d50fbd3bffc5b54152efe3b1de296cc7))
* **ui:** show footer errors and warnings before key hints ([a581094](https://github.com/ToshihitoKon/gh-timeline/commit/a581094839745c1f91faddf2d85460177e4e2425))

## v0.2.0

- The timeline (in the TUI and `-dump`) now groups events by pull request or issue within each `--- HH:MM ---` block.
  - Before: one line per event, `14:01 owner/repo PR#130 review requested dave → you, erin Fix typo`
  - After: a heading `owner/repo PR#130 Fix typo`, followed by indented event lines `  14:01 review requested dave → you, erin`
  - Migration: if you parse `-dump` output, read the repository, number, and title from the heading line, and the time, action, and user from the indented lines below it.

## v0.1.0

- First release.
