// 左側: 会話の一覧(更新の新しい順)。新規作成・切り替え・名前の変更・削除。下に設定パネルの開閉ボタン。
// 左上のハンバーガーで一覧を畳める。畳んだときもアイコンだけの細い列を残し、新規と設定は押せるようにする
import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { LuMenu, LuSettings, LuSquarePen } from "react-icons/lu";
import type { Conversation } from "../../bindings/aibow";

interface Props {
  list: Conversation[];
  currentId: string | null;
  generatingId: string | null;
  disabled: boolean;
  onOpen(id: string): void;
  onNew(): void;
  onDelete(id: string): void;
  onRename(id: string, title: string): void;
  settingsOpen: boolean;
  onToggleSettings(): void;
  open: boolean; // 一覧を開いているか
  onToggleOpen(): void;
}

export function ConversationList({
  list, currentId, generatingId, disabled, onOpen, onNew, onDelete, onRename, settingsOpen, onToggleSettings,
  open, onToggleOpen,
}: Props) {
  // 削除の確認を出している会話。消すと戻せないので、もう一度押してもらう
  const [confirming, setConfirming] = useState<string | null>(null);
  // 名前を変えている会話(ダブルクリックで入力欄になる)
  const [editing, setEditing] = useState<string | null>(null);

  const menu = (
    <button className="icon-btn" title={open ? "会話の一覧を閉じる" : "会話の一覧を開く"} onClick={onToggleOpen}>
      <LuMenu />
    </button>
  );
  const settingsCls = settingsOpen ? "settings active" : "settings";

  if (!open) {
    return (
      <nav className="conversations collapsed">
        {menu}
        <button className="icon-btn" title="新しい会話" disabled={disabled} onClick={onNew}><LuSquarePen /></button>
        <span className="spacer" />
        <button className={"icon-btn " + settingsCls} title="設定" onClick={onToggleSettings}><LuSettings /></button>
      </nav>
    );
  }

  return (
    <nav className="conversations">
      <div className="nav-top">
        {menu}
        <button className="new" disabled={disabled} onClick={onNew}>+ 新規</button>
      </div>
      <ul>
        {list.map(c => {
          const title = c.title || "新しい会話";
          const generating = c.id === generatingId;
          const cls = ["conversation", c.id === currentId && "current", generating && "generating"].filter(Boolean).join(" ");
          if (editing === c.id) {
            return (
              <li key={c.id} className={cls + " editing"}>
                <TitleInput
                  initial={c.title}
                  onDone={title => {
                    setEditing(null);
                    if (title !== null && title.trim() && title.trim() !== c.title) onRename(c.id, title);
                  }}
                />
              </li>
            );
          }
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
              <button
                className="title"
                title={`${title}(ダブルクリックで名前を変更)`}
                disabled={disabled}
                onClick={() => onOpen(c.id)}
                onDoubleClick={() => setEditing(c.id)}
              >
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
      <div className="footer">
        <button className={settingsCls} onClick={onToggleSettings}><LuSettings /> 設定</button>
      </div>
    </nav>
  );
}

// 名前の入力欄。Enter かフォーカスが外れたら確定、Esc で取り消し(onDone に null)
function TitleInput({ initial, onDone }: { initial: string; onDone(title: string | null): void }) {
  const [text, setText] = useState(initial);
  const input = useRef<HTMLInputElement>(null);
  const composing = useRef(false); // IME で変換中か
  const done = useRef(false); // Enter / Esc のあとの blur で、もう一度確定しないように

  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);

  const finish = (title: string | null) => {
    if (done.current) return;
    done.current = true;
    onDone(title);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    // IME で変換中の Enter は確定なので、名前の確定にしない(Composer と同じ確かめ方)
    if (composing.current || e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (e.key === "Enter") finish(text);
    if (e.key === "Escape") finish(null);
  };

  return (
    <input
      ref={input}
      type="text"
      className="title-input"
      value={text}
      onChange={e => setText(e.target.value)}
      onKeyDown={onKeyDown}
      onCompositionStart={() => (composing.current = true)}
      onCompositionEnd={() => (composing.current = false)}
      onBlur={() => finish(text)}
    />
  );
}
