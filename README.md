# isucon2026

このリポジトリは、ISUCON2026（2026-10-31）の競技時間中に、ISUCON を解く AI エージェントが書いたコードと設定です。
当日の git の履歴 19 commit を、公開できる範囲に絞って作り直して公開しています。
問題（リファレンス実装・ベンチマーカーなど）は、公式リポジトリ [isucon/isucon2026](https://github.com/isucon/isucon2026) で公開されています。

最終スコアは `625857` で、全チーム中1位でした。([ISUCON2026 受賞チームと全チームのスコア](https://isucon.net/archives/60126600.html))
最終走行のベンチマーカーのログは [`tmp/result-f5.txt`](tmp/result-f5.txt) にあります。
スコアの推移は「[ISUCON2026 オンラインライブ中継](https://www.youtube.com/watch?v=7EnEvey_0RI)」でも確認できます。

宣伝：2026-11-26 のイベント「[ISUCON2026 優勝AIエージェント、8時間分の生ログを眺める会](https://herp-inc.connpass.com/event/412593/)」にて、解説をします。

## 記事とポッドキャスト

エージェントの作り方と当日の経過は、次の記事とポッドキャストで話しています。

- [ISUCON2026 で優勝しました（当日編）](https://zenn.dev/luvtechno/articles/isucon2026-competition-day)（Zenn）
- [ISUCON2026 で優勝しました（準備編）](https://zenn.dev/luvtechno/articles/isucon2026-preparation)（Zenn）
- [142. ISUCON2026で優勝する方法 w/ kawasy](https://fukabori.fm/episode/142)（fukabori.fm）
- [#27 緊急！ISUCON2026優勝者に聞く、完全自律AIエージェントの作り方と今後のOSS](https://yaoyorozu-oss.henteko07.com/episode/4b51fca1-644f-4b3d-bcd7-42c44f9cf507)（八百万のOSS）
- [ISUCON 2026を制した謎のAI使い(?) (kawasy)](https://oss4.fun/episode/107/)（趣味でOSSをやっている者だ）

## 構成

4台のホストと、運営が用意したルーターで競技環境が構成されています。ベンチマーカーはルーター越しに各ホストへリクエストを送り、リージョン間の遅延はルーターが作っていました。

競技終了時点の役割は次のとおりです。

| ホスト | リージョン | 役割 |
| --- | --- | --- |
| asia1 | asia | アプリ（`:80`） |
| asia2 | asia | 書き込みを受けて順番を決め、ファイルに記録してほかの3台へ送るアプリ（ほかの3台が `192.168.4.10:8091` へ繋ぐ） |
| europe1 | europe | アプリ（`:80`） |
| america1 | america | アプリ（`:80`） |

各ホストのアプリは自分のメモリにデータのコピーを持ち、読み取りはそこから返します。書き込みはすべて asia2 へ送り、asia2 が順番を決めてファイルに記録し、同じ順番でほかのホストへ送ります。アプリは PostgreSQL に接続しません。PostgreSQL を読むのは、初期データから過去データのファイル一式を作る `packbuild` だけです。nginx は途中で経路から外し、競技終了時点では止まっていました。問題に含まれていた `isureal-guard`（攻撃を繰り返す IP を BAN する別プロセス）も、BAN の判定をアプリの中（`webapp/go/guard.go`）へ移し、競技終了時点では止まっていました。各ホストの環境変数は `servers/<ホスト>/home/isucon/env.sh` にあります。

## ディレクトリ

| パス | 中身 |
| --- | --- |
| `webapp/` | アプリ（Go）とスキーマ。初回 commit が当日配布されたリファレンス実装です |
| `webapp/go/cmd/` | `isureal`（アプリ）・`packbuild`（初期データの DB から過去データのファイル一式を作る）・`sigbuild`（過去の画像の署名を、決めた期限について前もって計算して表にする） |
| `hosts/` | エージェントが workspace で管理していたホストの設定（nginx・PostgreSQL・sysctl）。`hosts/guard/` は問題に含まれていた `isureal-guard` です |
| `servers/<ホスト>/` | 競技終了後にホストから採取した設定のうち、競技時間内に変わったもの。ホスト上のパスのまま置いています |
| `scripts/` | エージェントが書いたデプロイ・ベンチの実行・計測・分析・実験のスクリプト |
| `tools/browser.mjs` | ログイン済みのブラウザへ CDP で繋ぎ、ポータルを操作するスクリプト（競技ハーネスの一部） |
| `tmp/` | 当日の計測の結果。ポータルのジョブの結果（`result-*.txt`）、実験ごとの設定とスコア（`exp-*.txt`・`exp-summary.txt`）、pprof・top・mpstat（`prof/`）、ネットワークの統計（`net/`）、ジョブの一覧（`jobs.tsv`） |
| [`.isucon/plateau-ledger.md`](.isucon/plateau-ledger.md) | エージェント自身が当日の判断を書き残した記録。律速だと判断したもの、試した手と前後のスコア、検討して試さなかった手、監査役（スコアが伸びなくなったときに、これまでの判断を見直すサブエージェント）の観点ごとの結果が書いてあります。参照しているファイルのうち、`tmp/logs/`（下の「含めていないもの」）と `tools/plateau`（競技ハーネスの道具）は含めていません |

`hosts/` と `servers/` は中身が重なっています。`hosts/` はエージェントが手元で書いた設定で、`servers/` は競技終了時点にホストに実際に在った設定です。`hosts/` に無いもの（`/etc/nginx/nginx.conf`・systemd の drop-in・`env.sh`）は `servers/` にだけあります。`env.sh` の `ISUREAL_DB_PASSWORD` は、リファレンス実装のデフォルトと同じ値です。

`tmp/jobs.tsv` の `commit` の列は、このリポジトリの commit を指します。ハーネスだけを変えた commit はありませんでした。

## 初回 commit

初回 commit は、競技開始の直後にエージェントが作ったものです。当日配布されたリファレンス実装（`webapp/`・`hosts/guard/`）に、エージェントがそれまでに書いた計測の道具（`scripts/bench.mjs`・`hosts/nginx/isucon-log.conf`）と `tools/browser.mjs` が入っています。エージェントの変更は、この commit からの差分で読めます。

commit message は当日のままです。初回 commit の message にある「マニュアルの写し」は、ISUCON 運営の文書なので含めていません。

## そのままでは動かないもの

- ポータルを操作する次のスクリプトは、ログイン済みのブラウザ（`ISUCON_BROWSER_CDP_URL`）を前提にしています。当日はエージェントを起動する側がこのブラウザを用意していたので、このリポジトリだけでは動きません
  - `scripts/bench.mjs`・`scripts/quick.mjs`・`scripts/runbench.sh`・`scripts/probe.sh`・`scripts/exp.sh`〜`scripts/exp5.sh`
- ホストへ入るスクリプト（`scripts/deploy.sh` など）は `.ssh/config` を読みます。このファイルは含めていません
- 競技環境は終了後に削除されています。ルーターのアドレスは `ROUTER_HOST` に置き換えています

## 含めていないもの

- `tmp/logs/`：ベンチごと・ホストごとのアクセスログ（5.3GB）。ledger と `scripts/` が参照していますが、大きいので含めていません
- `tmp/bench-*.txt`：ジョブの進み具合を数秒おきに記録したもの。中身は `tmp/jobs.tsv` と `tmp/result-*.txt` にあります
- ホストのそのほかのファイル、認証情報、クラウドのアカウントやインスタンスの情報

## ISUCONについて

「ISUCON」は、さくらインターネット株式会社の商標または登録商標です。

## ライセンス

[MIT](LICENSE)

初回 commit の `webapp/` と `hosts/guard/` は、当日配布された ISUCON2026 のリファレンス実装です。公式リポジトリ [isucon/isucon2026](https://github.com/isucon/isucon2026) の [LICENSE](https://github.com/isucon/isucon2026/blob/main/LICENSE)（MIT、`Copyright (c) 2026 ISUCON2026 Contributors`）に従います。それ以降の commit の変更は、`Copyright (c) 2026 Yoshinori Kawasaki` の MIT です。
