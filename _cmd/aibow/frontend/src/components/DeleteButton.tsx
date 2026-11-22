// 削除の形(0050 の「削除の形」)。どの画面の削除も、この形にそろえる:
//   1. 最初のボタン:ゴミ箱のアイコン + 「削除」の赤い枠(danger-outline)。一覧の行の中など狭い所は、アイコンだけ(説明は title)
//   2. 押すと確かめる段:「〜を削除しますか?」+ 赤く塗った「削除」(danger)・「キャンセル」
// 重い削除の追加の確認(利用者の削除の、ログインの名前を打つ欄など)は extra と confirmDisabled で足す。
// worktree の「破棄」も同じ形(label を「破棄」にする)。
// 会話の一覧(行ごと確かめる段に変わる)と読み込んだファイル(ファイル名の表示の中)は、置き場所が違うので部品は使わず、言葉と見た目だけそろえている
// 赤くするのは style.css の button.danger / button.danger-outline の1か所。親で絞ったボタンの規則は :not(.danger) で外し、赤を上書きしない(0056)
import { useState, type ReactNode } from "react";
import { LuTrash2 } from "react-icons/lu";

interface Props {
  question: ReactNode;     // 確かめる文(「〜を削除しますか?」と、消えるものがあれば一言)
  onConfirm(): void;
  label?: string;          // ボタンの言葉(既定は「削除」。worktree は「破棄」)
  title?: string;          // 最初のボタンの説明(何が消えて、何が残るか)
  iconOnly?: boolean;      // 狭い所ではアイコンだけ
  disabled?: boolean;
  block?: boolean;         // 確かめる文が長いとき、文を1行使って、ボタンを下に並べる
  extra?: ReactNode;       // 確かめる段の、ボタンの前に置くもの(名前を打つ欄など)
  confirmDisabled?: boolean; // 確かめる段の「削除」を押せないとき(名前がまだ合っていない、など)
  onCancel?(): void;       // キャンセルしたとき(extra の中身を戻すため)
}

export function DeleteButton({ question, onConfirm, label = "削除", title, iconOnly, disabled, block, extra, confirmDisabled, onCancel }: Props) {
  const [confirming, setConfirming] = useState(false);
  if (!confirming) {
    return (
      <button
        type="button"
        className={"danger-outline" + (iconOnly ? " icon-only" : " with-icon")}
        disabled={disabled}
        title={title ?? (iconOnly ? label : undefined)}
        aria-label={iconOnly ? label : undefined}
        onClick={() => setConfirming(true)}
      >
        <LuTrash2 />{!iconOnly && ` ${label}`}
      </button>
    );
  }
  return (
    <div className={"delete-confirm" + (block ? " block" : "")}>
      <p className="confirm-text">{question}</p>
      <div className="delete-confirm-row">
        {extra}
        <button type="button" className="danger" disabled={disabled || confirmDisabled} onClick={() => { setConfirming(false); onConfirm(); }}>
          {label}
        </button>
        <button type="button" onClick={() => { setConfirming(false); onCancel?.(); }}>キャンセル</button>
      </div>
    </div>
  );
}
