import type { FormEvent, KeyboardEvent, MouseEvent } from "react";

// Go が返した error は、JavaScript では Error(message が Go のエラーの文)として届く
export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// isUnreachable は、aibow-server につながらなかったエラーか。
// Go の api/client の ErrUnreachable と、ブラウザの backendWeb.ts が出す文は、どちらもこの文で始まる
export function isUnreachable(err: unknown): boolean {
  return errorMessage(err).includes("aibow-server につながりません");
}

// operationEvents は、エラーを上に並べる画面(設定・管理・会話の設定)を包む要素に付ける受け口。
// その画面のエラーは「直前の操作の結果」なので、次の操作を始めたら clear で消す(2026-11-08、ユーザーと決めた)。
// 以前は足すだけで消えなかったので、ログインを3回間違えると同じ「名前かパスワードが違います」が3つ並んだ。
// 消してから操作が動くので、同じ失敗をやり直したときも、一度消えてから出直す(やり直したことが見える)。
//
// 部品ごとにエラーの持ち方を直さずに済むよう、操作そのもの(押す・送る・Enter・選ぶ)を外枠で拾う。
// capture で拾うのは、ボタンの onClick が Go を呼ぶより先に消すため(後から届いたエラーは残る)。
// 打っている途中(文字を入れる・消す)は操作に数えない。直している間もエラーを読めるように。
export function operationEvents(clear: () => void) {
  return {
    onClickCapture: (e: MouseEvent) => {
      if ((e.target as Element).closest("button")) clear();
    },
    onSubmitCapture: () => clear(),
    // Enter で動かす欄(つなぎ先の「確かめる」など)。IME で変換を決める Enter は操作ではない
    onKeyDownCapture: (e: KeyboardEvent) => {
      if (e.key === "Enter" && !e.nativeEvent.isComposing && e.target instanceof HTMLInputElement) clear();
    },
    // 選んだらすぐ変わる欄(選択肢・チェック)
    onChangeCapture: (e: FormEvent) => {
      const t = e.target;
      if (t instanceof HTMLSelectElement || (t instanceof HTMLInputElement && (t.type === "checkbox" || t.type === "radio"))) clear();
    },
  };
}
