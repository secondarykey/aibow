// 読み込んだファイルの札(0030 の決定事項 20)。入力欄の上(送る前。トークン数と、外す ×)と、発言の吹き出しの上(送ったあと)に出す。
// 中身は見せない(モデルには展開して渡すが、画面ではファイル名だけ)
import { LuFileText, LuX } from "react-icons/lu";
import type { File as StoredFile } from "../../bindings/github.com/secondarykey/aibow";

export interface Chip {
  file: StoredFile;
  tokens?: number; // 送る前だけ(このファイルで増えるトークン数)
}

export function FileChips({ files, onRemove }: { files: Chip[]; onRemove?(index: number): void }) {
  if (!files.length) return null;
  return (
    <div className="file-chips">
      {files.map(({ file, tokens }, i) => (
        <span key={file.id} className="file-chip" title={file.name}>
          <LuFileText className="icon" />
          <span className="file-name">{file.name}</span>
          <span className="file-size">
            {sizeText(dataSize(file.data))}
            {tokens !== undefined && ` · ${tokens.toLocaleString()} tok`}
          </span>
          {onRemove && (
            <button type="button" className="file-remove" title="外す" onClick={() => onRemove(i)}>
              <LuX />
            </button>
          )}
        </span>
      ))}
    </div>
  );
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
