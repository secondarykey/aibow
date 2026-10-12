// mermaid で図を描く。mermaid は大きい(exe の中で約 5MB)ので、最初に図が出たときに読み込む。
// アプリを起動して最初の図は読み込みで 0.5 秒ほどかかり、2回目からは 0.1 秒ほどで描ける。
//
// モデルが書いた図は信用できない入力なので、次の2つで守る(緩めない)。
//   - securityLevel: "strict": クリックの動作(click … call)を止め、ラベルの HTML を無害にする
//   - htmlLabels: false: ラベルを HTML ではなく SVG の文字で描く。strict でも <img> は残り、
//     src が外の URL なら画像を取りに行ってしまうため(試して確かめた)
import { useEffect, useState } from "react";
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

// 図を SVG のファイルにして保存する(ブラウザのダウンロード。WebView2 でも <a download> で保存できる)。
// 画面に出ている図そのまま(ダークテーマなら暗い配色)で保存する。mermaid の SVG は背景が透明で、
// ダークテーマの図は白い背景で開くと読めなくなるので、画面と同じ背景の四角を一番下に敷く
export function downloadSVG(svg: string, background: string): void {
  const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
  const root = doc.documentElement;
  // 画面では吹き出しの幅に合わせて縮めているが(width="100%" と max-width)、ファイルでは本来の大きさにする
  const [x, y, w, h] = (root.getAttribute("viewBox") ?? "0 0 0 0").split(/[\s,]+/).map(Number);
  if (w > 0 && h > 0) {
    root.setAttribute("width", String(w));
    root.setAttribute("height", String(h));
    root.style.removeProperty("max-width");
    const rect = doc.createElementNS("http://www.w3.org/2000/svg", "rect");
    for (const [k, v] of Object.entries({ x, y, width: w, height: h, fill: background })) rect.setAttribute(k, String(v));
    root.insertBefore(rect, root.firstChild);
  }
  const text = '<?xml version="1.0" encoding="UTF-8"?>\n' + new XMLSerializer().serializeToString(root);
  const url = URL.createObjectURL(new Blob([text], { type: "image/svg+xml" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `diagram-${timestamp(new Date())}.svg`;
  a.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000); // ダウンロードが始まるまで URL を残しておく
}

// 「20261013-063005」。保存するファイルの名前が重ならないように付ける
function timestamp(t: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${t.getFullYear()}${pad(t.getMonth() + 1)}${pad(t.getDate())}-${pad(t.getHours())}${pad(t.getMinutes())}${pad(t.getSeconds())}`;
}

// OS のライト / ダークの設定(画面の CSS と同じ prefers-color-scheme)。図の配色を合わせるために使う
export function usePrefersDark(): boolean {
  const query = "(prefers-color-scheme: dark)";
  const [dark, setDark] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const m = window.matchMedia(query);
    const onChange = () => setDark(m.matches);
    m.addEventListener("change", onChange);
    return () => m.removeEventListener("change", onChange);
  }, []);
  return dark;
}
