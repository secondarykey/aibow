// 図(mermaid が描いた SVG)を書き出す: SVG で保存・PNG で保存・PNG をコピー。
// どれも画面に出ている図そのまま(ダークテーマなら暗い配色)で書き出す。
//
// mermaid の SVG は背景が透明で、ダークテーマの図は白い背景で開くと読めなくなるので、
// 画面と同じ背景の四角を一番下に敷く。画面では吹き出しの幅に合わせて縮めているが、書き出すときは本来の大きさにする。
// 保存はブラウザのダウンロード(<a download>。WebView2 でも使える)、コピーはブラウザのクリップボード(navigator.clipboard)

// PNG は拡大したときにぼやけないよう、本来の大きさの2倍で描く
const PNG_SCALE = 2;

interface PreparedSVG {
  text: string;   // ファイルにする SVG の文字列
  width: number;  // 本来の大きさ(viewBox の幅と高さ)
  height: number;
}

export function downloadSVG(svg: string, background: string): void {
  const { text } = prepare(svg, background);
  download(new Blob([text], { type: "image/svg+xml" }), "svg");
}

export async function downloadPNG(svg: string, background: string): Promise<void> {
  download(await toPNG(svg, background), "png");
}

// 画像としてコピーする(Slack や Word などに貼れる)。
// ClipboardItem に Promise を渡すと、PNG を作り終える前にコピーの操作を始められる(押した操作の直後に呼ぶ必要があるため)
export async function copyPNG(svg: string, background: string): Promise<void> {
  await navigator.clipboard.write([new ClipboardItem({ "image/png": toPNG(svg, background) })]);
}

function prepare(svg: string, background: string): PreparedSVG {
  const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
  const root = doc.documentElement;
  const [x, y, width, height] = (root.getAttribute("viewBox") ?? "0 0 0 0").split(/[\s,]+/).map(Number);
  if (width > 0 && height > 0) {
    root.setAttribute("width", String(width));
    root.setAttribute("height", String(height));
    root.style.removeProperty("max-width");
    const rect = doc.createElementNS("http://www.w3.org/2000/svg", "rect");
    for (const [k, v] of Object.entries({ x, y, width, height, fill: background })) rect.setAttribute(k, String(v));
    root.insertBefore(rect, root.firstChild);
  }
  const text = '<?xml version="1.0" encoding="UTF-8"?>\n' + new XMLSerializer().serializeToString(root);
  return { text, width, height };
}

// SVG を画像として読み込み、Canvas に描いて PNG にする。
// SVG の中に HTML(foreignObject)があると Canvas から取り出せなくなるが、aibow は htmlLabels: false で描くので無い(mermaid.ts)
async function toPNG(svg: string, background: string): Promise<Blob> {
  const { text, width, height } = prepare(svg, background);
  if (!(width > 0 && height > 0)) throw new Error("図の大きさが分かりません");
  const url = URL.createObjectURL(new Blob([text], { type: "image/svg+xml" }));
  try {
    const img = new Image();
    img.src = url;
    await img.decode();
    const canvas = document.createElement("canvas");
    canvas.width = Math.ceil(width * PNG_SCALE);
    canvas.height = Math.ceil(height * PNG_SCALE);
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("Canvas が使えません");
    ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
    return await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob(b => (b ? resolve(b) : reject(new Error("PNG にできませんでした"))), "image/png"));
  } finally {
    URL.revokeObjectURL(url);
  }
}

function download(blob: Blob, ext: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `diagram-${timestamp(new Date())}.${ext}`;
  a.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000); // ダウンロードが始まるまで URL を残しておく
}

// 「20261013-063005」。保存するファイルの名前が重ならないように付ける
function timestamp(t: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${t.getFullYear()}${pad(t.getMonth() + 1)}${pad(t.getDate())}-${pad(t.getHours())}${pad(t.getMinutes())}${pad(t.getSeconds())}`;
}
