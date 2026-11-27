# Changes

Entries from v0.3.0 on are written by [release-please](https://github.com/googleapis/release-please) from pull request titles. Breaking changes and how to migrate are listed under "⚠ BREAKING CHANGES". Entries up to v0.2.0 list only breaking changes.

## [0.5.0](https://github.com/ToshihitoKon/gh-timeline/compare/v0.4.1...v0.5.0) (2026-11-27)


### ⚠ BREAKING CHANGES

* the minimum `-interval` is now `1m`. Before: values of `30s` or more were accepted. After: values under `1m` are rejected with `-interval must be at least 1m0s`. Migration: pass `-interval 1m` or longer.

### Bug Fixes

* count commits from the branch-creation push ([#30](https://github.com/ToshihitoKon/gh-timeline/issues/30)) ([5c977d9](https://github.com/ToshihitoKon/gh-timeline/commit/5c977d9df25fee6998309a742270e1aab1755949))
* drop the closed event recorded a second after the merge ([#71](https://github.com/ToshihitoKon/gh-timeline/issues/71)) ([370601f](https://github.com/ToshihitoKon/gh-timeline/commit/370601f37d99540b0ebe62d2c43a6cc27c482b71))
* keep polling when GraphQL returns partial errors ([#32](https://github.com/ToshihitoKon/gh-timeline/issues/32)) ([5c8730f](https://github.com/ToshihitoKon/gh-timeline/commit/5c8730f26837f678d60224e80969b815400ce4ae))
* keep the search start fixed until the first poll succeeds ([#39](https://github.com/ToshihitoKon/gh-timeline/issues/39)) ([4cb62c9](https://github.com/ToshihitoKon/gh-timeline/commit/4cb62c9ad034c1e0c7d0c98d25c8cd55660d3e72))
* print the same -version for every install method ([#57](https://github.com/ToshihitoKon/gh-timeline/issues/57)) ([02a655e](https://github.com/ToshihitoKon/gh-timeline/commit/02a655ed30aeefba021f272b4a5a2123b904d578))
* reduce GraphQL query cost ([0c22871](https://github.com/ToshihitoKon/gh-timeline/commit/0c228718df989e4c046f7f1b5e184c66e7bc5037))
* share poll steps and warnings between the TUI and -dump ([#63](https://github.com/ToshihitoKon/gh-timeline/issues/63)) ([7bf4fa6](https://github.com/ToshihitoKon/gh-timeline/commit/7bf4fa66a05d22ee2fcef1a7a3591a835b0bc78b))
* show cross-references instead of "updated -" when a pull request links an issue ([#69](https://github.com/ToshihitoKon/gh-timeline/issues/69)) ([7f09625](https://github.com/ToshihitoKon/gh-timeline/commit/7f0962541123dce7b153b4b3196c4722653ab95c))
* small fixes in parsing and rendering ([#59](https://github.com/ToshihitoKon/gh-timeline/issues/59)) ([e73406c](https://github.com/ToshihitoKon/gh-timeline/commit/e73406cb6acd47813b9df313dfe13515df400934))
* stop waiting for gh after it is killed ([#60](https://github.com/ToshihitoKon/gh-timeline/issues/60)) ([e6207fb](https://github.com/ToshihitoKon/gh-timeline/commit/e6207fb420317a3b32c8ec78d6d9a4d44ed5f323))
* strip control characters from GitHub strings ([#31](https://github.com/ToshihitoKon/gh-timeline/issues/31)) ([d98c879](https://github.com/ToshihitoKon/gh-timeline/commit/d98c879d33f932d1b89743fff662658a998cf9c7))
* time commits by push for every pull request ([#61](https://github.com/ToshihitoKon/gh-timeline/issues/61)) ([906ebcd](https://github.com/ToshihitoKon/gh-timeline/commit/906ebcd936dd0f0d041c880679bf4f467f576dc3))
* time out -dump and reject a negative -since ([#29](https://github.com/ToshihitoKon/gh-timeline/issues/29)) ([b7cf983](https://github.com/ToshihitoKon/gh-timeline/commit/b7cf983191e294dfaab2f84924c4ce396445f5f6))
* track shown events by ID instead of by time ([#37](https://github.com/ToshihitoKon/gh-timeline/issues/37)) ([d28eedf](https://github.com/ToshihitoKon/gh-timeline/commit/d28eedf46c43bcf9f58a49d29d1db2071692ea42))
* **ui:** small display fixes ([#34](https://github.com/ToshihitoKon/gh-timeline/issues/34)) ([b01e265](https://github.com/ToshihitoKon/gh-timeline/commit/b01e265b9f60d3a7047fd0eabc37e142ffb027bb))


### Performance Improvements

* call the GitHub API in process instead of starting gh ([#67](https://github.com/ToshihitoKon/gh-timeline/issues/67)) ([5fc6a57](https://github.com/ToshihitoKon/gh-timeline/commit/5fc6a5716750a8aec720ba4fd655d8b058087c9a))
* fetch fewer search results per poll ([#65](https://github.com/ToshihitoKon/gh-timeline/issues/65)) ([0477f83](https://github.com/ToshihitoKon/gh-timeline/commit/0477f834072f410ffae606e9341312f80e1804cc))
* fetch push activity in parallel ([#38](https://github.com/ToshihitoKon/gh-timeline/issues/38)) ([245a495](https://github.com/ToshihitoKon/gh-timeline/commit/245a495834a04c0bb3387f1ab775fd45ca7ec2d6))
* **ui:** keep rendering cheap as the history grows ([#64](https://github.com/ToshihitoKon/gh-timeline/issues/64)) ([5c2b7aa](https://github.com/ToshihitoKon/gh-timeline/commit/5c2b7aa5650dde4deccd33096b80967064b3027e))
* **ui:** re-render only when the timeline changes ([#35](https://github.com/ToshihitoKon/gh-timeline/issues/35)) ([5b5d912](https://github.com/ToshihitoKon/gh-timeline/commit/5b5d912bd52915d1e0512789d09c27749a7cfe8f))

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
