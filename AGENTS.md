# AGENTS.md

このリポジトリで作業する AI エージェント向けの指示です。
ここには**全体に関わること**だけを書きます。個別の仕組みの詳細は、下の「作業内容ごとに読むもの」から辿ってください。
触らない部分の説明は読まなくて構いません。

## プロジェクトの目的

aibow は、ローカルLLMを使ったアプリを**自分で作りながら仕組みを学ぶ**ための学習プロジェクトです。
最終目標は次の2つです。

- ローカルLLMでバイブコーディングできるエージェントを自作し、その中身を理解する
- MCP の使い方と作り方(クライアント・サーバー両方)を理解する

完成品を早く作ることより、**各段階で何が起きているかが読んで分かること**を優先してください。

## 全体の構成(ステップ2-3: ツール呼び出し)

```
ウィンドウ(_cmd/aibow/frontend の React) ⇄ Wails v3 ⇄ aibow ⇄ yzma ⇄ llama.cpp(_cmd/aibow/lib\ の DLL)⇄ GPU
                                                     ⇅          ↑ _cmd/aibow/models\*.gguf / Ollama の保存フォルダ
                                          aibow/store(SQLite: %APPDATA%\aibow\aibow.db)
```

- **Ollama などの外部 LLM サーバーは使わない**。aibow のプロセスの中で llama.cpp を直接動かす。
  これはユーザーの明確な方針なので、LLM サーバーを別プロセスで立てる構成を提案・導入しないこと
- ただし **Ollama を「モデルの管理係」として使うのは OK**(ユーザーの方針)。Ollama が保存したファイルを aibow が直接読む。Ollama のサーバーや API は使わない
- 画面は Wails v3 のデスクトップアプリ。HTTP サーバーは立てない(以前のブラウザ版から移行した)。
  GUI の無い環境で確かめるための Wails の server モードは、確認用の一時ビルドだけで使う([_docs/env-cloud.md](_docs/env-cloud.md))

| 場所 | 中身 |
|---|---|
| ルート(`aibow` パッケージ) | LLM を動かす部分と会話の仕組み。Wails を import しないライブラリ |
| `store/` | 会話の保存先(SQLite) |
| `_cmd/aibow/` | デスクトップアプリ(Wails v3 + React)。別モジュール |
| `_docs/` | 仕組みごとの説明(下の表)と、機能の仕様(`_docs/features/`)。利用者向けの手順は `setup.md`(準備と起動)・`usage.md`(使い方と設定)・`structure.md`(構成とファイル) |
| `TODO.md` | 全体の進み具合とロードマップ |

## 作業内容ごとに読むもの

| 作業内容 | 読むもの |
|---|---|
| 生成・モデルの読み込みと切り替え・トークン化・プロンプトの書式・思考の振り分け・モデルの一覧 | [_docs/engine.md](_docs/engine.md) |
| ツール呼び出し・エージェントループ・書式の interface(`Format`)・ツールの追加 | [_docs/tools.md](_docs/tools.md) |
| 会話の履歴・会話ごとの設定・アプリ全体の設定・保存の約束(`Store`) | [_docs/conversations.md](_docs/conversations.md) |
| 会話の上限・compact(truncate / summary)・メーター | [_docs/compact.md](_docs/compact.md) |
| SQLite の実装・マイグレーション | [store/AGENTS.md](store/AGENTS.md) |
| アプリの起動・`ChatService`・環境変数の読み込み・Go と画面のやり取り | [_cmd/aibow/AGENTS.md](_cmd/aibow/AGENTS.md) |
| 画面(配置・会話の一覧・発言と返事の操作・フォーカス・スクロール・ウィンドウの移動とリサイズ・IME) | [_docs/ui.md](_docs/ui.md) |
| 返事の表示(Markdown・コードの色付け・mermaid の図と書き出し) | [_docs/rendering.md](_docs/rendering.md) |
| バージョン・ローカルデプロイ | [_docs/deploy.md](_docs/deploy.md) |
| 利用者向けの説明(README・準備と起動・使い方と設定・ファイル構成)を直す | README は外部の人向けの入口として短く保ち、詳しい説明は [_docs/setup.md](_docs/setup.md)・[_docs/usage.md](_docs/usage.md)・[_docs/structure.md](_docs/structure.md) に書く |
| アプリを起動して動作を確かめる(共通の手順と確認する項目) | [_docs/testing.md](_docs/testing.md) |
| ユーザーの Windows 環境(worktree)で起動して確かめる | [_docs/env-windows.md](_docs/env-windows.md) |
| クラウドセッション(GUI の無い Linux)で起動して確かめる | [_docs/env-cloud.md](_docs/env-cloud.md) |
| パラメータの調整・実際に動かして分かったことの記録 | [_docs/tuning.md](_docs/tuning.md) |
| 不具合の調査・`npm audit` の警告 | [_docs/known-issues.md](_docs/known-issues.md) |

## 作業の進め方

- 全体の進み具合とロードマップは `TODO.md`、1つ1つの機能の仕様は `_docs/features/<番号>-<名前>.md` にある。作業を始める前に両方を読む
- 機能の仕様は、ユーザーが Chat で相談して決めたものを書いた「発注書」。書かれている決定事項に従い、「やらないこと」は先回りして作らない
- 仕様の「未決事項」は勝手に決めず、案を示してユーザーに確認する。作業中に新しく決めたことは、その仕様のファイルに書き足す
- 着手したら仕様の「状態」と `TODO.md` を 🔧 作業中に、完了の条件を満たしたら ✅ 完了にする
- ステップは順番に進める。先のステップの機能を先回りして入れない
- 仕組みの詳細を書き足すときは、該当する `_docs/*.md` かパッケージの `AGENTS.md` に書く。このファイルには、全体に関わることと読む場所の案内だけを足す

## 技術方針

- **言語は Go**(1.27 以上。標準の `uuid` パッケージを使う)。Python など別言語の実装を持ち込まない
- Go の外部パッケージは次の3つだけ。それ以外は標準ライブラリで書く
  - yzma(github.com/hybridgroup/yzma): llama.cpp の呼び出し。llama.cpp との版の組み合わせは [_docs/engine.md](_docs/engine.md)
  - Wails v3: 画面。`_cmd/aibow` だけで使い、ルートの `go.mod` には入れない
  - `modernc.org/sqlite`: 会話の保存。`store` パッケージだけで使う
- C コンパイラなし(`CGO_ENABLED=0`)でビルドできる状態を保つ
- 外部パッケージ(Go・npm とも)を足すときは、理由と代替案を示してユーザーに確認してから足す(MCP の公式 Go SDK はステップ4以降で検討してよい)
- チャットテンプレートなど、学習の対象になる仕組みはライブラリ任せにせず自分で書く
- 設定は `AIBOW_` で始まる環境変数にし、[_docs/usage.md](_docs/usage.md) の表に追加する(今ある環境変数はその表と [_cmd/aibow/AGENTS.md](_cmd/aibow/AGENTS.md))。
  画面で変えられる設定の環境変数は「DB にまだ値が無いときの初期値」で、画面で変えたら DB の値が優先

## コードの書き方

- コメントは日本語。「何をしているか」より「なぜそうしているか」「ここで何を学べるか」を書く
- 1つの関数に処理を詰め込みすぎず、流れが上から読めるようにする
- エラーは握りつぶさず、原因と対処が分かる日本語のメッセージで画面かログに出す
- 学習用のログ(トークン数、再利用数、速度、終了理由など)は残す。仕組みが観察できることが目的の一部

## ユーザーとのやり取り

- **コマンドやツールを使うときは、それが何をするものかを必ず説明する**。説明なしにコマンドを並べない
- 新しい概念(例: KV キャッシュ、tool_call、MCP のトランスポート)を導入するときは、短い説明を添える
- ユーザーの環境: Windows、NVIDIA GPU(VRAM 8GB)、メモリ 64GB。シェルは bash(Windows 上の Git Bash など。PowerShell の `$env:` 構文は使えない)。パスは `/d/Go/Projects/aibow` の形で書く
- 主に使うモデルは Qwen3.5(4B / 9B)。ユーザーは Ollama でのモデル管理に慣れている

## 確認方法

変更後は最低限、次を実行してください。

```bash
gofmt -l .                       # 整形が必要なファイルを表示(何も出なければ OK。_cmd も含む)
go test ./...                    # 書式(公式テンプレートとの一致)、思考とツール呼び出しの振り分け、ツール、エージェントループ、Ollama の目録、会話の仕組み、compact、設定の引き継ぎ、SQLite(マイグレーション)のテスト(モデル不要)
(cd _cmd/aibow && wails3 build)  # バインディング生成 → 画面の型チェックとビルド(tsc + vite)→ Go のビルド
go vet ./... ./_cmd/aibow        # よくある間違いを静的チェック(./... は _cmd を含まないので並べる)
```

`_cmd/aibow` は `frontend/dist` を埋め込むので、画面を一度もビルドしていないと `go vet` / `go build` が「パターンに一致するファイルが無い」で失敗します。先に `wails3 build` を実行します。

アプリを起動して確かめるときは、**先に必ず [_docs/testing.md](_docs/testing.md) と、今いる環境のファイルを読んでください**。

- ユーザーの Windows 環境: [_docs/env-windows.md](_docs/env-windows.md)。worktree でやってはいけないこと(`lib\` / `models\` を作る、本体へのリンクを張る、ユーザーの DB やアプリに触れる)が書いてあります
- クラウドセッション(GUI の無い Linux): [_docs/env-cloud.md](_docs/env-cloud.md)。画面を server モードで確かめる方法が書いてあります
