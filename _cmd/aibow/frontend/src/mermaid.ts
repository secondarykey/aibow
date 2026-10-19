// mermaid で図を描く。mermaid は大きい(exe の中で約 5MB)ので、最初に図が出たときに読み込む。
// アプリを起動して最初の図は読み込みで 0.5 秒ほどかかり、2回目からは 0.1 秒ほどで描ける。
//
// モデルが書いた図は信用できない入力なので、次の2つで守る(緩めない)。
//   - securityLevel: "strict": クリックの動作(click … call)を止め、ラベルの HTML を無害にする
//   - htmlLabels: false: ラベルを HTML ではなく SVG の文字で描く。strict でも <img> は残り、
//     src が外の URL なら画像を取りに行ってしまうため(試して確かめた)。
//     PNG の書き出し(diagramExport.ts)も、SVG の中に HTML が無いことを前提にしている
import type { Mermaid } from "mermaid";

let loading: Promise<Mermaid> | null = null;
let queue: Promise<unknown> = Promise.resolve();
let count = 0;

function loadMermaid(): Promise<Mermaid> {
  loading ??= import("mermaid").then(m => m.default);
  return loading;
}

// 図の SVG を返す。書き方が間違っていたら、mermaid のエラーの文で reject する。
// mermaid は設定(テーマ)を全体で1つ持つので、描く処理は1つずつ順番に行う
export function renderMermaid(code: string, dark: boolean): Promise<string> {
  const job = queue.then(async () => {
    const mermaid = await loadMermaid();
    mermaid.initialize({ startOnLoad: false, securityLevel: "strict", htmlLabels: false, theme: dark ? "dark" : "default" });
    const id = `mermaid-${++count}`;
    try {
      await mermaid.parse(code); // 書き間違いは描く前に見つけ、分かりやすいエラーの文にする
      const { svg } = await mermaid.render(id, code);
      return svg;
    } finally {
      // 失敗したとき、mermaid が測るために body に足した要素が残ることがあるので消す
      document.getElementById("d" + id)?.remove();
    }
  });
  queue = job.catch(() => {});
  return job;
}

// 図の配色は、画面と同じ明るさ(theme.ts の useDarkTheme)に合わせる
