// 読み込んだファイルの札(0030 の決定事項 20・25)。入力欄の上(送る前。トークン数と、外す ×)と、発言の吹き出しの上(送ったあと)に出す。
// テキストのファイルは中身を見せない(モデルには展開して渡すが、画面ではファイル名だけ)。
// 画像は小さな表示(高さ 120px ほど)にし、押すと画面いっぱいに重ねて大きく見せる(Esc か外を押すと閉じる)
import { useEffect, useState } from "react";
import { LuFileText, LuX } from "react-icons/lu";
import type { File as StoredFile } from "../../bindings/github.com/secondarykey/aibow";

export interface Chip {
  file: StoredFile;
  tokens?: number; // 送る前だけ(このファイルで増えるトークン数)
}

export function FileChips({ files, onRemove }: { files: Chip[]; onRemove?(index: number): void }) {
  const [viewing, setViewing] = useState<StoredFile | null>(null);
  if (!files.length) return null;
  return (
    <div className="file-chips">
      {files.map(({ file, tokens }, i) => {
        const size = sizeText(dataSize(file.data)) + (tokens !== undefined ? ` · ${tokens.toLocaleString()} tok` : "");
        const remove = onRemove && (
          <button type="button" className="file-remove" title="外す" onClick={() => onRemove(i)}>
            <LuX />
          </button>
        );
        if (file.kind === "image") {
          return (
            <span key={file.id} className="file-chip image" title={`${file.name}(${size})`}>
              <button type="button" className="file-thumb" onClick={() => setViewing(file)}>
                <img src={dataURL(file)} alt={file.name} />
              </button>
              {tokens !== undefined && <span className="file-size">{tokens.toLocaleString()} tok</span>}
              {remove}
            </span>
          );
        }
        return (
          <span key={file.id} className="file-chip" title={file.name}>
            <LuFileText className="icon" />
            <span className="file-name">{file.name}</span>
            <span className="file-size">{size}</span>
            {remove}
          </span>
        );
      })}
      {viewing && <ImageViewer file={viewing} onClose={() => setViewing(null)} />}
    </div>
  );
}

// ImageViewer は画像を画面いっぱいに重ねて大きく見せる。Esc か、画像の外を押すと閉じる
function ImageViewer({ file, onClose }: { file: StoredFile; onClose(): void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onClose]);
  return (
    <div className="image-viewer" onClick={onClose}>
      <img src={dataURL(file)} alt={file.name} onClick={e => e.stopPropagation()} />
    </div>
  );
}

// dataURL は画像の中身(Go の []byte は JSON で base64 の文字列)を、そのまま img に渡せる形にする
function dataURL(f: StoredFile): string {
  return `data:${f.format};base64,${f.data ?? ""}`;
}

// dataSize は base64 の文字列から、もとのバイト数を出す(4 文字で 3 バイト。最後の = は詰め物)
function dataSize(b64: string | null): number {
  if (!b64) return 0;
  const pad = b64.endsWith("==") ? 2 : b64.endsWith("=") ? 1 : 0;
  return (b64.length / 4) * 3 - pad;
}

function sizeText(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${n} B`;
}
