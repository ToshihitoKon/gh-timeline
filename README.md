# gh-timeline

自分が関わる GitHub の PR / issue の更新を、時系列のタイムラインとして流し続ける TUI ツール。

```text
gh-timeline — @you
checked 14:05 · next 14:10 · every 5m0s
--- 14:05 ---
14:03 owner/repo IS#45 closed carol Something broken
14:01 owner/repo PR#130 review requested dave → you, erin Fix typo

--- 14:00 ---
13:59 owner/repo PR#123 commented (2) bob Add feature
13:58 owner/repo PR#123 approved alice Add feature
↑/↓: scroll   g/G: top/bottom   r: refresh   q: quit
```

各行は「時刻・リポジトリ・種別と番号・アクション・ユーザー・タイトル」の順に並ぶ。レビュー依頼先やアサイン先は `→` の後に出す。列幅はそろえず、要素ごとに色を変えて区切りが分かるようにしている。`PR#123` / `IS#45` は PR / issue の状態（open: 緑、draft: 灰、merged: 紫、closed: 赤）で色分けする。

## 主な機能

- 次の PR / issue の更新を一定間隔（既定 5 分）でポーリングして表示する
  - 自分が作成した PR / issue
  - 自分がアサインされている PR / issue
  - 自分個人にレビューが依頼されている PR（`-team-reviews` でチーム宛ての依頼も対象にする）
  - 自分がレビューした PR（レビュー後の作者の返信や追加コミットを追うため）
- 更新が見つかったときだけ `--- HH:MM ---` の区切り行を挟んで先頭に追記する（新しい更新ほど上）。日付が変わると `--- 11/21 09:00 ---` のように日付も出す
- 前回の表示以降のコメント・レビュー・コミット・マージなどのイベントを、1 イベント 1 行で新しい順に並べる
  - 同じ人の連続したコメントやレビュー依頼は 1 行にまとめる
  - 自分の操作は薄く表示する

## 必要なもの

- [GitHub CLI](https://cli.github.com/)（`gh`）がインストールされ、`gh auth login` 済みであること

## インストール

```sh
go install github.com/ToshihitoKon/gh-timeline/cmd/gh-timeline@latest
```

## 使い方

```sh
gh-timeline
```

起動直後に過去 30 分間の更新を表示し、その後は 5 分ごとに新しい更新を追記する。

### フラグ

- `-interval`
  - ポーリング間隔（既定: `5m`、下限: `30s`）
- `-since`
  - 初回のポーリングでさかのぼる幅（既定: `30m`）
- `-team-reviews`
  - 所属チーム宛てのレビュー依頼も対象にする
- `-dump`
  - TUI を起動せず、初回のポーリング結果を一度だけテキストで出力する
- `-version`
  - バージョンを表示して終了する

### キー操作

- `↑` / `↓` / `PgUp` / `PgDn`: スクロール
- `g` / `G`: 先頭 / 末尾へ移動
- `r`: 次のポーリングを待たずに今すぐ取得する
- `q` / `Ctrl+C`: 終了

先頭を表示している間は、新しい更新が追記されると自動で先頭に追従する。過去の分にスクロールしている間は、読んでいる行が画面上で動かないよう表示位置を保つ。

## 制限

- 1 回のポーリングで検索条件ごとに最大 100 件まで取得する。超えた場合はフッタに警告を出す
- 1 件の PR / issue につき、直近 10 件のイベントまでしか取得しない
- タイトル・本文の編集やラベルの変更は個別のイベントとして取得しないため、`updated` とだけ表示する
- 表示した更新の記録はメモリ上にだけ持つため、再起動すると `-since` の範囲から表示し直す
