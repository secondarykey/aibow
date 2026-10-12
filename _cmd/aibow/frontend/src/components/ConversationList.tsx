// 左側: 会話の一覧(更新の新しい順)。新規作成・切り替え・削除
import { useState } from "react";
import type { Conversation } from "../../bindings/aibow";

interface Props {
  list: Conversation[];
  currentId: string | null;
  generatingId: string | null;
  disabled: boolean;
  onOpen(id: string): void;
  onNew(): void;
  onDelete(id: string): void;
}

export function ConversationList({ list, currentId, generatingId, disabled, onOpen, onNew, onDelete }: Props) {
  // 削除の確認を出している会話。消すと戻せないので、もう一度押してもらう
  const [confirming, setConfirming] = useState<string | null>(null);

  return (
    <nav className="conversations">
      <button className="new" disabled={disabled} onClick={onNew}>+ 新規</button>
      <ul>
        {list.map(c => {
          const title = c.title || "新しい会話";
          const generating = c.id === generatingId;
          const cls = ["conversation", c.id === currentId && "current", generating && "generating"].filter(Boolean).join(" ");
          if (confirming === c.id) {
            return (
              <li key={c.id} className={cls + " confirming"}>
                <span className="confirm-text">削除しますか?</span>
                <button className="danger" onClick={() => { setConfirming(null); onDelete(c.id); }}>削除</button>
                <button onClick={() => setConfirming(null)}>やめる</button>
              </li>
            );
          }
          return (
            <li key={c.id} className={cls}>
              <button className="title" title={title} disabled={disabled} onClick={() => onOpen(c.id)}>
                {generating && <span className="dot" title="返事を生成中" />}
                {title}
              </button>
              <button
                className="delete"
                title={generating ? "返事を生成中の会話は削除できません" : "削除"}
                disabled={disabled || generating}
                onClick={() => setConfirming(c.id)}
              >×</button>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
