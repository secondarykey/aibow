# aibow の構成と関係の図（2026-10-31 時点）

元にしたもの：`AGENTS.md`（全体の構成）・`_docs/mcp.md`・`_docs/features/0024-server-tools.md`・`tools.go` の `policyOf`。
図は Mermaid。aibow の返事の表示（mermaid の図）や GitHub でそのまま描ける。

## 0. ひと目で比べる

| | ① ローカルのアプリ | ② サーバーにつないだアプリ | ③ ブラウザ | （参考）Cline など |
|---|---|---|---|---|
| 生成（llama.cpp） | アプリの中 | aibow-server | aibow-server | aibow-server |
| 会話の保存 | 手元の `aibow.db` | `server.db` | `server.db` | クライアント側 |
| ツールが動く PC | 手元 | **手元** | **サーバー** | クライアント側（自分のツールを送る） |
| MCP のサーバーを書く所 | `settings.json` | `settings.json`（手元） | `server.json` の `mcpServers` | — |
| 扱いの既定 | `tools.json`（PC の人） | `tools.json`（PC の人） | `server-tools.json`（管理者） | — |
| 既定の「使わない」 | 既定では使わない（会話で使える） | 同左 | **禁止**（会話でも使えない） | — |
| 作業フォルダ | 会話ごとに手元のパス | 手元のパス | 管理者の「資料のフォルダ」（名前だけ見える） | — |
| `run_command` | あり（確認つき） | あり（確認つき） | **出さない** | — |
| 確認のカード | Wails の中で往復 | Wails の中で往復 | SSE で届き、画面用の口で答える | — |

---

## 1. ローカル利用のアプリ（いちばん小さい形）

ネットワークを使わず、1つのプロセスの中で全部が動く。組み込みのツールも、aibow の中で自作の MCP サーバーを立てて **メモリ内の MCP 越し** に呼ぶ（0007 の 5-4）。

```mermaid
flowchart LR
  subgraph PC["自分の PC"]
    subgraph APP["aibow.exe（Wails v3 のデスクトップアプリ）"]
      UI["画面（React）"] <--> SVC["aibow/ui（Service・DirectHost）"]
      SVC <--> CONV["aibow（会話の仕組み・エージェントループ）"]
      CONV <--> ENG["aibow/engine → yzma → llama.cpp"]
      CONV <--> ST["aibow/store"]
      CONV <--> MCPC["aibow/mcp/client"]
      MCPC <-->|"メモリ内の MCP（Local）"| MCPS["aibow/mcp/server（自作サーバー）"]
      MCPS <--> TOOL["aibow/tool<br/>時刻・計算・list/read/write/edit_file・run_command"]
    end
    ENG <--> GPU[("GPU / モデル（GGUF・Ollama の保存フォルダ）")]
    ST <--> DB[("%APPDATA%\aibow\aibow.db")]
    TOOL <-->|"os.Root の中だけ"| WD[("会話の作業フォルダ")]
    MCPC <-->|"stdio（子プロセス）"| EXT1["MCP のサーバー（filesystem・Blender など）"]
    MCPC <-->|"Streamable HTTP"| EXT2["url の MCP のサーバー"]
    CFG[/"settings.json（mcpServers）<br/>tools.json（扱いの既定）"/] -. 読む .-> MCPC
  end
```

ポイント
- 誰が決めるか：**その PC を使う人**（`settings.json` を手で書き、`tools.json` は画面で変える）
- `${workdir}` を使う MCP のサーバーは作業フォルダごとに1つ起動、使わないものは全会話で1つ
- サーバーは要る最初の送信のときに起動し、アプリを閉じるまで使う

---

## 2. サーバーありのアプリ（`connection.json` / `serverUrl` で aibow-server につなぐ）

生成と会話の保存だけがサーバーに移る。**ツールは今も手元の PC で動く**（0013 の C）。

```mermaid
flowchart LR
  subgraph PC["自分の PC"]
    subgraph APP["aibow.exe"]
      UI["画面"] <--> SVC["aibow/ui（RemoteHost）"]
      SVC <--> CONV["aibow（会話の仕組み・エージェントループ）"]
      CONV <--> MCPC["aibow/mcp/client（Local と settings.json のサーバー）"]
      CONV <-->|"Generator"| APIC["aibow/api/client"]
      CONV <-->|"Store"| SR["aibow/store/remote"]
    end
    MCPC <--> TOOLS["手元のツール・MCP のサーバー"]
    TOOLS <--> WD[("手元の作業フォルダ")]
    CFG[/"settings.json・tools.json・connection.json"/] -.-> APP
  end

  subgraph SRV["サーバーの PC：aibow-server"]
    AUTH["aibow/auth（ログイン・アプリ用トークン）"]
    API["aibow/api（OpenAI 互換＋aibow 専用の口）"] <--> SEAT{{"順番待ちの席 api.Seat"}} <--> ENG["aibow/engine → llama.cpp → GPU"]
    STO["/_aibow/store/"] <--> SDB[("server.db")]
  end

  APIC <-->|"HTTP(S)・トークン"| API
  SR <-->|"HTTP(S)"| STO
  CLINE["Cline など OpenAI 互換のクライアント"] <-->|"HTTP(S)・API キー<br/>自分のツールを tools で送る"| API
```

ポイント
- アプリは llama.cpp を読み込まない。会話は `server.db` に置かれ、誰のものかは `ctx`（`aibow.WithUser`）で運ぶ
- サーバーの MCP や資料のフォルダは **アプリにも Cline にも出さない**（0024 の決定事項 13）

---

## 3. ブラウザでのツール（ステップ 9〜12）

会話の仕組みもツールも **サーバーの PC で動く**。決めるのは **管理者**。

```mermaid
flowchart LR
  BR["ブラウザ<br/>（aibow/web の同じ画面）"]

  subgraph SRV["サーバーの PC：aibow-server"]
    H["画面用の口 ui.Handler<br/>/_aibow/ui/call/{メソッド}・SSE /_aibow/ui/events"]
    H <--> SVC["aibow/ui（ServerHost・Browser）"]
    SVC <--> CONV["aibow（会話の仕組み）<br/>IgnoreWorkDir・利用者は ctx"]
    CONV <--> SEAT{{"api.Seat（OpenAI 互換の口と共有）"}} <--> ENG["engine → llama.cpp → GPU"]
    CONV <--> SDB[("server.db<br/>会話・利用者・プロファイル")]
    CONV <--> LOC["組み込み（Local）<br/>時刻・計算"]
    CONV <--> FT["作業フォルダのツール<br/>list/read/write/edit_file<br/>（run_command は出さない）"]
    CONV <--> MCPC["mcp/client：全員で1つの MCP のサーバー"]
    FT <-->|"os.Root"| FOL[("資料のフォルダ<br/>server.json の folders")]
    MCPC <-->|"${workdir} 付きはフォルダごとに1つ"| FOL
    MCPC <--> NET["fetch・検索など<br/>（社内の URL・外へ）"]
    CFG[/"server.json（mcpServers・folders）<br/>server-tools.json（管理者の既定）"/] -.-> CONV
  end

  BR <-->|"HTTP(S)・cookie のセッション"| H
  ADMIN(["管理者"]) -. 手で書く .-> CFG
  ADMIN -. 画面の「ツール」タブ .-> CFG
  ADMIN -. 配るプロファイルでフォルダを選ぶ .-> SDB
  GIT(["git は管理者が手で"]) -.-> FOL
```

確認の往復（12-1a）：今は Wails の中で済んでいる「ループを止めてカードを出し、答えを待つ」を、SSE と HTTP に分けたもの。

```mermaid
sequenceDiagram
  participant B as ブラウザ
  participant H as 画面用の口
  participant C as 会話の仕組み
  participant T as ツール
  B->>H: Send（長い HTTP の呼び出し）
  H->>C: 生成（席 api.Seat を持つ）
  C->>C: モデルが tool_call を書く → 扱いを決める
  alt 確認する（ask）
    C-->>B: SSE で confirm（差分・引数）
    Note over B,C: 待つのは 5 分まで。タブを閉じたら停止と同じ。<br/>SSE だけ切れたら PendingConfirm で出し直す
    B->>H: ConfirmTool（許可 / 拒否 / この会話では今後聞かない）
    H->>C: 答え（Remember なら会話の上書きに書く）
  end
  C->>T: 実行（ログ：誰の・どの会話の・どのツール）
  T-->>C: 結果
  C-->>B: 続きの返事
```

---

## 4. MCP 周り：つなぎ方・権限・ルール

### 4-1. aibow はクライアントでもサーバーでもある

```mermaid
flowchart TB
  subgraph CLIENT["aibow/mcp/client（クライアント）"]
    MGR["Manager：起動・停止・ツールを tool.Tool として見せる<br/>名前に サーバー名__ を付ける"]
    LOCAL["Local：組み込みをメモリ内の MCP で呼ぶ"]
  end
  subgraph SERVER["aibow/mcp/server（自作サーバー）"]
    S["tool のツールを MCP で出す<br/>書き換えるツールは MRTR で確認"]
  end
  TOOLPKG["aibow/tool"]

  MGR -->|"stdio / Streamable HTTP"| OTHER["既製の MCP のサーバー"]
  LOCAL -->|"NewInMemoryTransports"| S
  S --> TOOLPKG
  CC["Claude Code など"] -->|"stdio"| EXE["aibow-mcp.exe"] --> S
  MGR -->|"stdio（settings.json に書けば）"| EXE
```

### 4-2. ツールの扱いが決まる順番（`policyOf`。上から見て最初に決まったもの）

```mermaid
flowchart TD
  START(["モデルがツールを呼んだ / 一覧を作る"]) --> P{"プロファイルの決まりで<br/>使えない？（rules.off・0014）"}
  P -- はい --> OFF1["使わない（禁止）<br/>会話でも戻せない"]
  P -- いいえ --> A{"aibow-server の管理者が<br/>使わないにした？（server-tools.json）"}
  A -- はい --> OFF2["使わない（禁止）<br/>※ブラウザの会話だけ"]
  A -- いいえ --> CV{"会話ごとの上書き<br/>tool_policies がある？"}
  CV -- "off / ask / allow" --> R1["その値"]
  CV -- "on（使う）" --> ON["PC の既定の確認する/しないを使う<br/>（PC の off は打ち消す）"]
  CV -- なし --> PC{"PC の既定（tools.json）<br/>ツールごと → グループごと"}
  PC -- ある --> R2["その値<br/>（アプリの off は「既定では使わない」）"]
  PC -- ない --> D{"決まりの既定"}
  D -- "組み込みの読むだけ" --> ALLOW["確認しない"]
  D -- "書き換える・コマンド・MCP のツール" --> ASK["確認する"]
```

### 4-3. 誰が何を決められるか

```mermaid
flowchart LR
  subgraph WHO["決める人"]
    PCU(["PC の持ち主"])
    ADM(["aibow-server の管理者"])
    USR(["利用者（会話ごと）"])
  end
  subgraph WHAT["決めるもの"]
    SJ["settings.json：どの MCP を起動するか"]
    TJ["tools.json：アプリの扱いの既定"]
    SVJ["server.json：サーバーの MCP・資料のフォルダ"]
    STJ["server-tools.json：ブラウザの扱いの既定（使わない＝禁止）"]
    PROF["配るプロファイル：使えないツール・資料のフォルダ・誰に配るか"]
    CONVP["会話の上書き：使う / 使わない / 確認する<br/>カードの「この会話では今後聞かない」「今後許可しない」"]
  end
  PCU --> SJ & TJ
  ADM --> SVJ & STJ & PROF
  USR --> CONVP
  USR -. "ブラウザでは「確認しない」は<br/>カードからだけ" .-> CONVP
```

信頼のルール（要点）
- MCP のツールは中身が見えないので、決まりの既定は **確認する**。`readOnlyHint` は信じない（0007 の決定事項 9）
- `settings.json` に書いた MCP のサーバーは、名乗りを偽れるので **呼ぶ前の確認も残す**（aibow-mcp でも確認が 2 回出る）。aibow の中の自作サーバー（Local）だけ、サーバーの差分を信頼して確認を 1 回にする
- ブラウザの会話では、サーバーの MCP は全員で1つ。人をまたいで状態が残らないものを置く・fetch が社内の URL に届く危うさは管理者が判断（aibow は止めない）
- ツールの実行はログに残す（確認しないで実行したもの・資料のフォルダに書いたものは Notice）

---

プロファイルを配る流れ(誰に届くか・決まり・資料のフォルダ・グループ長)の図は [diagrams-profiles.md](diagrams-profiles.md)。
