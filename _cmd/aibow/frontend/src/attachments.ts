// attachments.ts: 入力欄で読み込んだファイル(0030)。送信するまでここで持つ。
//
// 読み込み(選ぶ・ドロップ・貼り付け)のたびに Go の CheckFile で確かめる。Go がテキストか画像か・文字コード・
// 会話の上限に入るか・画像を読めるモデルかを決め、だめなら理由と数字のエラーを返す(画面は札を足さずにエラーを出す)。
// 会話のモデルをまだ読み込んでいなければ、数えずに受け付けることがある(uncounted。送信のときに数える)。
// 確かめたもの(FileCheck)は送信のときに Send に渡す。打っている文と同じく、会話を切り替えても持ったままにする
// (送るときに Go がもう一度、会話の上限に入るかを確かめる)。
import { useCallback, useRef, useState } from "react";
import type { ConversationSettings, FileCheck } from "../bindings/github.com/secondarykey/aibow";
import { ChatService } from "./backend";
import { errorMessage } from "./errors";

// Go の aibow.ImageMaxBytes(画像の上限。テキストの上限 1 MB より大きい)と aibow.FilesPerSend と同じ。
// 大きいファイルを base64 にして Go に送る前に断るため(テキストか画像かは Go が中身で決めるので、ここでは大きいほうの上限で断る)
const MAX_BYTES = 10 << 20;
const FILES_PER_SEND = 10;

export interface Attachments {
  files: FileCheck[];
  add(fs: File[]): Promise<void>;
  remove(index: number): void;
  take(): FileCheck[]; // 送信するときに取り出す(入力欄の札は消える)
  restore(fs: FileCheck[]): void; // 送れなかったときに戻す
}

// target は送り先: 開いている会話の ID(新しい会話の画面では NIL)と、新しい会話の画面の設定(開いている会話では null)
export function useAttachments(target: () => { id: string; draft: ConversationSettings | null }, onError: (text: string) => void): Attachments {
  const [files, setFiles] = useState<FileCheck[]>([]);
  // 続けて読み込んだときも、付けたばかりのファイルを一緒に数えられるように、今の札をここにも持つ
  const current = useRef<FileCheck[]>([]);
  const set = (fs: FileCheck[]) => {
    current.current = fs;
    setFiles(fs);
  };

  const add = useCallback(async (fs: File[]) => {
    for (const f of fs) {
      if (current.current.length >= FILES_PER_SEND) {
        onError(`1 つの発言に付けられるファイルは ${FILES_PER_SEND} 個までです`);
        return;
      }
      if (f.size > MAX_BYTES) {
        onError(`${f.name} は大きすぎるため読み込めません(画像は 1 枚 ${MAX_BYTES >> 20} MB まで、テキストは 1 MB まで)`);
        continue;
      }
      try {
        const { id, draft } = target();
        // もう付けているファイルは、中身ではなく数とトークン数だけを渡す(画像は重いので、読み込むたびに送り直さない)
        const pendingTokens = current.current.reduce((n, c) => n + c.tokens, 0);
        const c = await ChatService.CheckFile(id, draft, f.name, await readBase64(f), current.current.length, pendingTokens);
        set([...current.current, c]);
      } catch (err) {
        onError(errorMessage(err));
      }
    }
  }, [target, onError]);

  const remove = useCallback((index: number) => set(current.current.filter((_, i) => i !== index)), []);
  const take = useCallback(() => {
    const fs = current.current;
    set([]);
    return fs;
  }, []);
  // 送れなかったら戻す。その間に読み込んだものがあれば、後ろに残す
  const restore = useCallback((fs: FileCheck[]) => set([...fs, ...current.current]), []);

  return { files, add, remove, take, restore };
}

// readBase64 はファイルの中身を base64 にする(Go の []byte は JSON で base64 の文字列になる。Wails もブラウザも同じ)
function readBase64(f: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => {
      const url = r.result as string; // data:<形式>;base64,<中身>
      resolve(url.slice(url.indexOf(",") + 1));
    };
    r.onerror = () => reject(new Error(`${f.name} を読めません: ${r.error?.message ?? ""}`));
    r.readAsDataURL(f);
  });
}
