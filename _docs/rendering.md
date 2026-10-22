# 返事の表示(Markdown・コードの色付け・mermaid の図)

返事の本文と要約の文を、Markdown として表示する仕組みの説明です。コードブロックの色付けと、mermaid の図(書き出しを含む)もここにあります。
画面の配置や操作は [ui.md](ui.md) にあります。

## ファイル

| ファイル | 役割 |
|---|---|
| `components/RenderedText.tsx` | 文をブロックに分け、コードブロック以外を marked で HTML にする。リンクは OS のブラウザで開く |
| `markdown.ts` | marked の設定と、安全のための出力の差し替え |
| `components/CodeBlock.tsx` | コードブロック(コピー・色付け)と mermaid の図(切り替え・保存・コピー) |
| `highlight.ts` | highlight.js の読み込みと色付け |
| `mermaid.ts` | mermaid の読み込みと図を描く処理。OS のライト / ダーク(`usePrefersDark`) |
| `diagramExport.ts` | 図の書き出し(SVG で保存・PNG で保存・PNG をコピー) |
| `components/SplitButton.tsx` | ドロップダウン付きのボタン(図の保存とコピー) |

使っているパッケージ(marked・highlight.js・mermaid)と版の決まりは [_cmd/aibow/AGENTS.md](../_cmd/aibow/AGENTS.md) にあります。

## Markdown

- 表示するのは返事の本文と要約の文。ユーザーの発言と思考の箱は文字のまま
- Markdown の解析は学習の対象ではないので、marked に任せる。`breaks: true` で1つの改行も改行にする
- 文をブロックに分け(`lexer`)、コードブロックだけは React で出す(`CodeBlock.tsx`)。残りは続いている分をまとめて marked で HTML にする
  - 箇条書きの中のコードブロックは marked がそのまま HTML にするので、コピーボタン・色付け・図は付かない
- 生成中は届くたびに全体を解析し直す。閉じていない書式(`**生成` など)は、閉じる記号が届くまで記号のまま見える
- 見た目は `style.css` の `.md`。吹き出しの中なので見出しを小さく・余白を詰め、色はテーマの変数から取る(ダークテーマでもそのまま合う)

## 安全(モデルの出力は信用できない入力として扱う)

- `markdown.ts` で marked の出力を差し替えている
  - 文章の中の HTML はタグとして解釈させず、文字として見せる
  - リンクは http / https だけを `<a>` にする。それ以外(`javascript:`・相対パスなど)は文字にする
  - 画像は読み込まない(外のサーバーに取りに行かない)。http / https ならリンクとして出す
- リンクを押したら、Go を呼ぶ係の `openURL`(`backend.ts`)で開く。デスクトップは Wails の `Browser.OpenURL` で OS のふだんのブラウザ、ブラウザでは新しいタブ(0016 の 17)。ウィンドウの中で開くと画面がそのページに移り、チャットに戻れなくなるため
- mermaid は `securityLevel: "strict"`(クリックの動作を止める)と `htmlLabels: false`(ラベルを SVG の文字で描く)で描く(下の「mermaid の図」)

## コードブロック

- 右上にそのコードだけをコピーするボタン(`CopyButton.tsx`)
- 言語が書かれていれば highlight.js で色を付ける(よく使う 37 言語の `lib/common`)。
  言語が無い・知らない言語のときは色を付けない(言語の推測は遅く、よく外れるので使わない)。色は `style.css` の `--hl-*`(GitHub の配色が元)
- 言語が書かれていれば、ブロックにマウスを乗せたときの吹き出し(`title`)に出す(``` の後ろの最初の語を小文字にしたもの。例: `bash`)。言語が無ければ出さない。mermaid の図は `mermaid`(2026-10-16、ユーザーの指示)。
  コピーなどのボタンの上では、ボタンの説明が出る。箇条書きの中のコードブロックは marked がそのまま HTML にするので出ない

## mermaid の図

- 言語が `mermaid` のコードブロックを図にする
- 図にするのは閉じる記号(```)が届いてから(生成中は途中のコードで、描くとエラーになる)。それまでと、書き間違いのときはコードのまま見せ、エラーの文を添える
- 配色は OS のライト / ダークに合わせる(`usePrefersDark`)。mermaid は設定を全体で1つ持つので、描く処理は1つずつ順番に行う
- 図の上の行に、左から「コードの切り替え・保存・コピー」
  - 切り替えはコードのアイコン(`LuCode`)のトグル。押している間はコードを見せ、アクセントの色になる
  - 保存とコピーはドロップダウン付きのボタン(本体はいつもの動き、横の ▾ でメニュー)。UI を増やさないため、アイコンの数は変えずにメニューに入れる
    - 保存: ふだんは SVG、メニューで PNG
    - コピー: ふだんはコードの文字(ほかのコードブロックと同じ)、メニューで PNG の画像
- `securityLevel: "strict"` だけだと、ラベルの `<img>` が残り、外の URL の画像を取りに行くことを確かめている。そのため `htmlLabels: false` にしている。
  この設定には、`npm audit` で出る katex の問題を実行させない役目([known-issues.md](known-issues.md))と、PNG の書き出しの前提(下)の役目もある。変えるときは両方を確かめ直す

## 図の書き出し(`diagramExport.ts`)

- 保存は `<a download>`(WebView2 でも使える。Go は使わない)、PNG のコピーは `navigator.clipboard.write`
- 画面に出ている配色のまま書き出す。mermaid の SVG は背景が透明なので、吹き出しと同じ背景(`--panel`)の四角を一番下に敷く(ダークテーマの図を白い背景で開いても読めるように)
- 画面では吹き出しの幅に縮めているが、書き出すときは本来の大きさ(viewBox の幅と高さ)。PNG は拡大してもぼやけにくいよう2倍で描く
- PNG は SVG を画像として Canvas に描いて作る。SVG の中に HTML(foreignObject)があると Canvas から取り出せないが、`htmlLabels: false` なので無い
- ファイル名は `diagram-20261013-063005.svg` / `.png`。PNG の結果(コピーしました・失敗の理由)は図の上の行に短く出す

## 読み込みと重さ

- highlight.js と mermaid は、最初に必要になったときに読み込む(起動時の JS は増やさない)
- 2026-10-12 に測った値: mermaid は exe の中で約 5MB、アプリを起動して最初の図は約 0.6 秒、2回目からは約 0.1 秒。highlight.js は約 150KB
