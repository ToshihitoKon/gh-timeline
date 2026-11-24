# PR のタイトルとマージ

- PR はスカッシュマージで取り込む
  - スカッシュマージでは PR のタイトルがそのまま main のコミットになり、release-please はそのコミットから CHANGES.md とリリースノートを作る
- PR のタイトルは Conventional Commits の形式で書く
  - 例: `feat(ui): show labels`、`fix: ...`、`docs: ...`
  - `feat` と `fix` は CHANGES.md に載り、次の版を上げる。`docs`、`chore`、`ci`、`test`、`refactor` などは載らない
- 利用者の使い方を壊す変更には、タイトルの type / scope の直後に `!` を付ける
  - 例: `feat!: drop the time from -dump event lines`
  - 対象: 受け付けるフラグ、README やフッターで案内しているキー操作、`-dump` の出力の形
  - TUI の画面を読み取って動くものはないため、TUI の見た目（色、記号、行の並べ方）の変更は対象にせず、`feat` / `fix` として扱う
  - 案内していないキー（bubbles の viewport が既定で割り当てたキーなど）の意味を変えても対象にしない
  - 1.0 未満の間は、`!` を付けてもマイナー版が上がる（`release-please-config.json` の `bump-minor-pre-major`）
- 破壊的変更の PR では、本文にコミットの差し替えブロックを書き、以前の挙動・新しい挙動・移行のしかたを `BREAKING CHANGE:` のフッターに書く
  - release-please はこのブロックをスカッシュコミットの代わりに読み、CHANGES.md とリリースノートの「⚠ BREAKING CHANGES」に載せる
  - フッターの後ろに続く行まで移行の説明として読まれうるため、フッターを PR 本文の末尾に直に書かない
  - テンプレートを消し忘れた PR すべてで見本が使われてしまうため、見本は PR テンプレートに書かない
  - 例:

    ```text
    BEGIN_COMMIT_OVERRIDE
    feat!: drop the time from -dump event lines

    BREAKING CHANGE: `-dump` no longer prints the time on event lines. Before: `  14:01 commented alice`. After: `  commented alice`. Migration: use the time on the `--- HH:MM ---` line above, which is when the update was found.
    END_COMMIT_OVERRIDE
    ```
- dependabot の PR には触らない
  - タイトルが `chore(deps): ...` のため CHANGES.md には載らず、依存の更新だけではリリース PR も作られない

# CHANGES.md

- 手で編集しない。release-please がリリース PR の中で書き足す
  - v0.2.0 以前の節は、release-please を入れる前に手で書いたもの

# リリース

- main に `feat` / `fix` が取り込まれると、release-please がリリース PR を作り、以後の取り込みのたびに更新する
- リリース PR をマージすると、release-please が `vX.Y.Z` のタグと Release を作り、同じ `release-please` ワークフローで GoReleaser がバイナリを載せる
  - タグを手で打たない
- Release には `gh-timeline-<os>-<arch>` の名前でバイナリをそのまま載せる。`gh extension install` / `gh extension upgrade` がこの名前で探すため、アーカイブ形式や名前を変えない
