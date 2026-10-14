// 左側: 会話の一覧(更新の新しい順)。新規作成・切り替え・名前の変更・会話の設定(行の歯車)・削除。
// 下にアプリの名前とバージョン、右に設定の画面の開閉ボタン(アイコンだけ)。
// ウィンドウの上から下までの列。上のハンバーガーで一覧を畳める。畳んだときもアイコンだけの細い列を残し、
// ハンバーガー・新規・設定はそこに集める。上の空いているところはタイトルバーと同じくドラッグでウィンドウを動かせる。
// 「+ 新規」は一覧の一番上の行に置く(スクロールしても隠れないよう、一覧の外に固定する)。
// 開いているときも畳んだときも同じアイコンを使い、開いているときだけ文字を添える
import { useEffect, useRef, useState } from "react";
import { ChatService } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { KeyboardEvent } from "react";
import { LuMenu, LuPlus, LuSettings } from "react-icons/lu";
import { toggleMaximiseOnDoubleClick } from "./TitleBar";
import type { Conversation } from "../../bindings/github.com/secondarykey/aibow";

interface Props {
  list: Conversation[];
  currentId: string | null;
  generatingId: string | null;
  waitingId: string | null; // ツールの実行前の確認を待っている会話(生成中の印を「確認待ち」にする)
  disabled: boolean;
  onOpen(id: string): void;
  onNew(): void;
  onDelete(id: string): void;
  onRename(id: string, title: string): void;
  convSettingsId: string | null;       // 会話の設定の画面を開いている会話
  onConvSettings(id: string): void;    // 行の歯車: その会話の設定の画面を開く / 閉じる
  settingsOpen: boolean;
  onToggleSettings(): void;
  open: boolean; // 一覧を開いているか
  onToggleOpen(): void;
  width: number; // 開いているときの幅(px)。一覧の右の仕切り(Splitter)で変えられる
}

export function ConversationList({
  list, currentId, generatingId, waitingId, disabled, onOpen, onNew, onDelete, onRename, convSettingsId, onConvSettings,
  settingsOpen, onToggleSettings,
  open, onToggleOpen, width,
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
      <nav className="conversations collapsed" onDoubleClick={toggleMaximiseOnDoubleClick}>
        {menu}
        <button className="icon-btn" title="新しい会話" disabled={disabled} onClick={onNew}><LuPlus /></button>
        <span className="spacer" />
        <button className={"icon-btn " + settingsCls} title="設定" onClick={onToggleSettings}><LuSettings /></button>
      </nav>
    );
  }

  return (
    <nav className="conversations" style={{ width }}>
      <div className="nav-top" onDoubleClick={toggleMaximiseOnDoubleClick}>
        {menu}
      </div>
      <button className="new" title="新しい会話" disabled={disabled} onClick={onNew}><LuPlus /> 新規</button>
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
                {generating && (c.id === waitingId
                  ? <span className="dot waiting" title="ツールの実行の確認待ち" />
                  : <span className="dot" title="返事を生成中" />)}
                {title}
              </button>
              <button
                className={"conv-settings" + (c.id === convSettingsId ? " active" : "")}
                title="会話の設定"
                disabled={disabled}
                onClick={() => onConvSettings(c.id)}
              ><LuSettings /></button>
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
        <AppName />
        <button className={"icon-btn " + settingsCls} title="設定" onClick={onToggleSettings}><LuSettings /></button>
      </div>
    </nav>
  );
}

// AppName はアプリの名前とバージョン(Go の _cmd/aibow/version)。
function AppName() {
  const [version, setVersion] = useState("");
  useEffect(() => {
    ChatService.Version().then(setVersion, () => setVersion(""));
  }, []);
  return <span className="app-name">AIBow{version && <span className="app-version">{version}</span>}</span>;
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
