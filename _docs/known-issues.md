# 既知の問題

原因が分かっていない不具合と、分かっていてそのままにしている問題の記録です。似た症状を調べるときに読み、再発したら書き足してください。

## npm audit の「2 low severity vulnerabilities」(katex。2026-10-12)

`_cmd/aibow/frontend` で `npm install` / `npm audit` をすると、low の脆弱性が2件と出る。分かっていて、そのままにしている。

- 中身は1件。katex(数式を描くパッケージ)0.11.0 以上 0.18.2 未満の [GHSA-238p-pmpm-9mq7](https://github.com/advisories/GHSA-238p-pmpm-9mq7)。
  mermaid 12.1.0 が katex 0.16 系(`^0.16.47`)を使っているので、mermaid も一緒に数えられる。深刻度は low、CVSS は 0
- 内容: ページにすでに別の問題(プロトタイプ汚染 = すべてのオブジェクトの元になる部分を書き換えられること)があると、それを使って katex の安全のための制限(`trust`)をすり抜けられる。katex だけでは起きない
- aibow で起きない理由
  - mermaid は数式を描くときに `trust` を有効にしておらず、描いた結果もさらに無害化している
  - aibow は `htmlLabels: false`(`mermaid.ts`)で図を描く。この設定では、図に数式(`$$x^2 + y^2$$`)を書いても katex は読み込まれないことを試して確かめた。
    katex のファイルは exe に入っているが、実行されない
- **`npm audit fix --force` はしない**。直し方として mermaid 10.8.0(2年以上前の版)に下げることになり、今の mermaid の機能と修正を失う
- katex だけを直った版(0.18 系)に `overrides` で固定することもしない。mermaid 12.1.0 は 0.16 系を前提にしていて、0.16 → 0.18 の変更は互換性が保証されないため
- 解消するとき: mermaid が katex を 0.18.2 以上に上げた版を出したら、mermaid を上げる(出てから2週間以上たった版。`_cmd/aibow/AGENTS.md`)。
  `htmlLabels` を true に戻すなど mermaid の設定を変えたときは、katex が読み込まれないかを確かめ直す

## モデルの読み込み中に GGML_ASSERT で落ちた(2026-10-12)

- Windows + CUDA で、Ollama の qwen3.5:4b を読み込む途中に `GGML_ASSERT(ctx->mem_buffer != NULL) failed`(ggml.c:1644、メモリ確保の失敗)で1回だけ落ちた
- 同じ条件で10回起動し直しても再現せず、原因は未特定。同じモデルファイルは Linux(CPU)の aibow と llama.cpp 単体では正常に動く
- 再発したら `AIBOW_LLAMA_LOG=1` でログを取り、そのとき動いていた他のアプリも確認する
- C 側の assert はプロセスごと終了させるので、Go 側の「元のモデルに戻す」処理では防げない
- exe の異常終了の記録(`aibow_crash.log`。Go の `debug.SetCrashOutput`)にも残らない。Go のランタイムを通らず、C の標準エラー出力に書かれて終わるため。再発を追うなら `wails3 dev` で `AIBOW_LLAMA_LOG=1` にしてターミナルを見る
