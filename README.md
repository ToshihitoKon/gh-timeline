<h1 align="center">gh-timeline</h1>

<p align="center">
  自分が関わる GitHub の PR / issue の動きを、ターミナルにタイムラインとして流し続ける TUI
</p>

<p align="center">
  <a href="https://github.com/ToshihitoKon/gh-timeline/blob/main/LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <img alt="Go" src="https://img.shields.io/github/go-mod/go-version/ToshihitoKon/gh-timeline">
</p>

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

GitHub の通知画面を開きに行かなくても、レビュー依頼・承認・コメント・マージといった動きがターミナルの片隅に流れてくる。tmux のペインや別ウィンドウに常駐させておく使い方を想定している。

## 特徴

- **`gh` があれば動く**
  - 認証は GitHub CLI に任せるため、トークンの設定は不要
- **自分に関係するものだけを追う**
  - 自分が作成した PR / issue
  - 自分がアサインされている PR / issue
  - 自分個人にレビューが依頼されている PR
  - 自分がレビューした PR（レビュー後の作者の返信や追加コミットも追える）
- **5 分ごとの区切りで、新しいものが上**
  - 更新が見つかったときだけ `--- HH:MM ---` の区切りを先頭に足す。更新が無い回は何も増えない
  - 日付をまたぐと `--- 11/21 09:00 ---` のように日付も出す
- **1 イベント 1 行**
  - `時刻 リポジトリ 番号 アクション ユーザー タイトル` の順に並ぶ
  - 同じ人の連続したコメントやレビュー依頼は `commented (3)` や `→ bob, carol` のように 1 行にまとめる
  - 自分の操作は薄く表示し、他の人の動きが目に入りやすくする

## インストール

[GitHub CLI](https://cli.github.com/)（`gh`）をインストールし、`gh auth login` を済ませておく。

```sh
go install github.com/ToshihitoKon/gh-timeline/cmd/gh-timeline@latest
```

ソースからビルドする場合:

```sh
git clone https://github.com/ToshihitoKon/gh-timeline.git
cd gh-timeline
go build -o gh-timeline ./cmd/gh-timeline
```

## 使い方

```sh
gh-timeline
```

起動直後に過去 30 分間の更新を表示し、その後は 5 分ごとに新しい更新を先頭に足していく。

```sh
# 起動時に過去 1 日分をさかのぼり、2 分ごとに確認する
gh-timeline -since 24h -interval 2m

# チーム宛てのレビュー依頼も対象にする
gh-timeline -team-reviews

# TUI を起動せず、今の更新を一度だけ出力する
gh-timeline -dump
```

### フラグ

- `-interval`: ポーリング間隔（既定: `5m`、下限: `30s`）
- `-since`: 起動時にさかのぼる幅（既定: `30m`）
- `-team-reviews`: 所属チーム宛てのレビュー依頼も対象にする。CODEOWNERS でチームが自動指定されるリポジトリでは件数が大きく増える
- `-dump`: TUI を起動せず、初回のポーリング結果を一度だけテキストで出力する
- `-version`: バージョンを表示して終了する

### キー操作

- `↑` / `↓` / `PgUp` / `PgDn`: スクロール
- `g` / `G`: 先頭 / 末尾へ移動
- `r`: 次のポーリングを待たずに今すぐ取得する
- `q` / `Ctrl+C`: 終了

先頭を表示している間は、新しい更新が届くと自動で先頭に追従する。過去の分を読んでいる間は、読んでいる行が画面上で動かないよう表示位置を保つ。

## 表示の読み方

```text
14:01 owner/repo PR#130 review requested dave → you, erin Fix typo
```

左から順に次の要素が並ぶ。

- `14:01`: イベントの時刻（前日以前のものは `11/19 23:59` のように日付付き）
- `owner/repo`: リポジトリ
- `PR#130`: PR なら `PR#番号`、issue なら `IS#番号`
- `review requested`: アクション
- `dave`: 操作したユーザー
- `→ you, erin`: レビュー依頼先・アサイン先（該当するアクションのときだけ）
- `Fix typo`: タイトル

列幅はそろえず、要素ごとに色を変えて区切りを見分けられるようにしている。

- `PR#130` / `IS#45`: PR / issue の状態で色が変わる
  - open: 緑
  - draft: 灰
  - merged、完了で閉じた issue: 紫
  - closed: 赤
- アクション: 基本は橙
  - `approved`: 緑
  - `changes requested`、`closed`: 赤
  - `merged`: 紫
- ユーザーと依頼先: シアン

表示するアクションは次のとおり。

- `commented` / `approved` / `changes requested` / `reviewed`
- `pushed N commits` / `force-pushed`
- `merged` / `closed` / `reopened`
- `review requested` / `assigned`
- `ready for review` / `converted to draft`
- `updated`: タイトル・本文の編集やラベル変更など、個別のイベントとして取得しない更新

## 仕組み

- 1 回のポーリングで、4 つの検索条件（作成者・アサイン・レビュー依頼・レビュー済み）を GraphQL の 1 リクエストにまとめて `gh api graphql` で送る
- 検索範囲は前回のポーリングから 10 分さかのぼって重ねる。GitHub の検索インデックスへの反映が遅れても取りこぼさないためで、重なった分は表示済みの更新時刻と比べて捨てる
- 取得に失敗した回は検索範囲を進めず、フッタにエラーを出す。次に成功したときに失敗した回の分もまとめて取り直す

## 制限

- 1 回のポーリングで検索条件ごとに最大 100 件まで取得する。超えた場合はフッタに警告を出す
- 1 件の PR / issue につき、直近 10 件のイベントまでしか取得しない
- 表示した更新の記録はメモリ上にだけ持つため、再起動すると `-since` の範囲から表示し直す
- 配色はダーク背景のターミナルを前提にしている

## ライセンス

[MIT](LICENSE)
