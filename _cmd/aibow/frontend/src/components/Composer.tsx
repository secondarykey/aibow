// 入力欄。送信ボタンは枠の中の右下に Enter のマークで置く(停止ボタンは生成中の返事の吹き出しの中にある)。
// ファイルを読み込む(0030):送信ボタンの左のボタンで選ぶ・入力欄のあたりにドロップ・貼り付け。読み込んだものは枠の上に札で並べる
import { useEffect, useRef, useState } from "react";
import { LuCornerDownLeft, LuPaperclip } from "react-icons/lu";
import type { ClipboardEvent, DragEvent, FormEvent, KeyboardEvent, ReactNode } from "react";
import type { FileCheck } from "../../bindings/github.com/secondarykey/aibow";
import { FileChips } from "./FileChips";

interface Props {
  text: string;           // 打っている文。設定の画面を開いている間は入力欄が消えるので、文は App が持っておく
  onTextChange(text: string): void;
  canSend: boolean;
  blockedReason?: string; // 送信できない理由(ボタンのツールチップに出す)
  home?: boolean;         // 新しい会話の画面(真ん中に出す。上の区切り線と背景を付けない)
  focusKey?: number;      // 変わるたびに入力欄にフォーカスを移す(会話を開いた・「+ 新規」を押した、など)
  onSend(text: string): void; // 読み込んだファイルは、呼んだ側(App)が持っていて一緒に送る
  top?: ReactNode;        // 入力欄の枠の上に出すもの(作業フォルダ)
  files: FileCheck[];     // 読み込んだファイル(0030。送るまで札で出す)
  onAddFiles(files: File[]): void;
  onRemoveFile(index: number): void;
}

// テキストを打つ入力欄か(ボタンやチェックボックス、スライダーは対象外)
function isTextInput(el: Element | null): boolean {
  if (el instanceof HTMLTextAreaElement) return true;
  return el instanceof HTMLInputElement && ["text", "search", "number"].includes(el.type);
}

// focusUnlessTyping は入力欄にフォーカスを移す。ただし、ほかの入力欄で打っている最中なら奪わない。
//
// 会話の名前の変更は、一覧のダブルクリックで入力欄を出す。ダブルクリックの1回目・2回目のクリックで
// 「会話を開く → 開き終わったら入力欄へ」が動くので、開き終わるのが入力欄を出したあとになり、
// 名前の入力欄からフォーカスを奪って(blur で確定扱いになり)編集が閉じてしまっていた。
// 返事が終わったときに、会話の名前を打っている最中だった場合も同じ。
function focusUnlessTyping(el: HTMLTextAreaElement | null) {
  if (!el) return;
  const active = document.activeElement;
  if (active !== el && isTextInput(active)) return;
  el.focus();
}

export function Composer({ text, onTextChange: setText, canSend, blockedReason, home, focusKey, onSend, top, files, onAddFiles, onRemoveFile }: Props) {
  const input = useRef<HTMLTextAreaElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  const composing = useRef(false); // IME で変換中か
  const [dragging, setDragging] = useState(false); // ファイルを持って入力欄の上にいる(枠の色を変える)

  // 入力に合わせて高さを伸ばす(最大 200px)
  useEffect(() => {
    const el = input.current;
    if (!el) return;
    el.style.height = "";
    el.style.height = Math.min(el.scrollHeight, 200) + "px";
  }, [text]);

  // 返事が終わったら、すぐ次を打てるように入力欄に戻る
  useEffect(() => {
    if (canSend) focusUnlessTyping(input.current);
  }, [canSend]);

  // 一覧で会話を選んだり「+ 新規」を押したりしたあと、すぐ打てるように入力欄に戻る。
  // 押したボタンにフォーカスが残るので、こちらから移す(生成中で送れなくても、打つことはできる)
  useEffect(() => {
    focusUnlessTyping(input.current);
  }, [focusKey]);

  const submit = (e?: FormEvent) => {
    e?.preventDefault();
    const t = text.trim();
    if ((!t && !files.length) || !canSend) return;
    setText("");
    onSend(t);
  };

  // ドロップ(0030)。Wails のランタイムも document でファイルのドラッグを受けて断る(「ドロップできない」カーソルにする)ので、
  // ここで受けたら上に伝えない(stopPropagation)。preventDefault はブラウザがファイルを開いて画面を移ってしまうのを止める
  const isFileDrag = (e: DragEvent) => e.dataTransfer.types.includes("Files");
  const onDragOver = (e: DragEvent) => {
    if (!isFileDrag(e)) return;
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = "copy";
    setDragging(true);
  };
  const onDragLeave = (e: DragEvent) => {
    // 中の要素に移っただけ(relatedTarget がまだ内側)なら、出ていない
    if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
  };
  const onDrop = (e: DragEvent) => {
    if (!isFileDrag(e)) return;
    e.preventDefault();
    e.stopPropagation();
    setDragging(false);
    onAddFiles(Array.from(e.dataTransfer.files));
  };
  // 貼り付け(0030)。ファイルをコピーしたとき(エクスプローラーなど)だけ読み込む。
  // 文も一緒に入っている(表計算の表は、文と画像の両方が入る)ときは、今までどおり文を貼る
  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const fs = Array.from(e.clipboardData.files);
    if (!fs.length || e.clipboardData.types.includes("text/plain")) return;
    e.preventDefault();
    onAddFiles(fs);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // IME で変換中の Enter は確定なので送信しない。
    // isComposing だけだと確定の Enter の直後を取りこぼすことがあるので、
    // compositionstart〜end の記録と、IME 処理中のキーに付く keyCode 229 でも確かめる
    if (composing.current || e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <footer className={home ? "home" : undefined} onDragOver={onDragOver} onDragLeave={onDragLeave} onDrop={onDrop}>
      {top}
      {files.length > 0 && (
        <div className="composer-files">
          <FileChips files={files} onRemove={onRemoveFile} />
        </div>
      )}
      <form className={"composer" + (dragging ? " dragging" : "")} onSubmit={submit}>
        <textarea
          ref={input}
          rows={1}
          value={text}
          placeholder="メッセージを入力(Enterで送信 / Shift+Enterで改行)"
          onChange={e => setText(e.target.value)}
          onKeyDown={onKeyDown}
          onCompositionStart={() => (composing.current = true)}
          onCompositionEnd={() => (composing.current = false)}
          onPaste={onPaste}
        />
        <input
          ref={picker}
          type="file"
          multiple
          hidden
          onChange={e => {
            onAddFiles(Array.from(e.target.files ?? []));
            e.target.value = ""; // 同じファイルをもう一度選んでも change が来るように
          }}
        />
        <button className="attach" type="button" title="ファイルを読み込む" onClick={() => picker.current?.click()}>
          <LuPaperclip />
        </button>
        <button
          className="send"
          type="submit"
          disabled={!canSend || (!text.trim() && !files.length)}
          title={canSend ? "送信(Enter)" : blockedReason}
        >
          <LuCornerDownLeft />
        </button>
      </form>
    </footer>
  );
}
