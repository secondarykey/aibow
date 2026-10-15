// 左側: 会話の一覧(更新の新しい順)。新規作成・切り替え・名前の変更・削除。
// 名前の変更はダブルクリックか F2(F2 は App.tsx が受けて renamingId を決める)。
// 会話の設定の歯車は、一覧を畳んでいても開けるよう、入力欄の下(メーターの右)に置く(App.tsx)。
// 下にアプリの名前とバージョン、右に設定の画面の開閉ボタン(アイコンだけ)。
// ウィンドウの上から下までの列。上のハンバーガーで一覧を畳める。畳んだときもアイコンだけの細い列を残し、
// ハンバーガー・新規・設定はそこに集める。上の空いているところはタイトルバーと同じくドラッグでウィンドウを動かせる。
// 「+ 新規」は一覧の一番上の行に置く(スクロールしても隠れないよう、一覧の外に固定する)。
// 開いているときも畳んだときも同じアイコンを使い、開いているときだけ文字を添える
//
// 会話の名前の左の印(丸):
//   生成中   … 青の点滅
//   確認待ち … 橙(点滅しない。ツールを実行する前の確認に答えるまで、送信が止まっている)
//   エラー   … 赤(点滅しない。ほかの会話を見ている間にエラーで終わった。その会話を開くと消え、エラーが表示に出る)
//   終わった … 青(点滅しない。ほかの会話を見ている間に返事が終わった。その会話を開くと消える)
// 一覧を畳んでいるときは、いちばん気にしてほしい印(確認待ち → エラー → 生成中 → 終わった)をハンバーガーに出す
import { useEffect, useRef, useState } from "react";
import { ChatService } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { KeyboardEvent } from "react";
import { LuMenu, LuPlus, LuSettings } from "react-icons/lu";
import { toggleMaximiseOnDoubleClick } from "./TitleBar";
import type { Conversation } from "../../bindings/github.com/secondarykey/aibow";
import type { Ended } from "../useChat";

interface Props {
  list: Conversation[];
  currentId: string | null;
  generatingId: string | null;
  waitingId: string | null; // ツールの実行前の確認を待っている会話(生成中の印を「確認待ち」にする)
  ended: Record<string, Ended>; // ほかの会話を見ている間に送信が終わった会話と終わり方(開くと消える。useChat が持つ)
  disabled: boolean;
  onOpen(id: string): void;
  onNew(): void;
  onDelete(id: string): void;
  onRename(id: string, title: string): void;
  renamingId: string | null; // 名前を変えている会話(入力欄にする)
  onStartRename(id: string): void;
  onEndRename(): void;
  settingsOpen: boolean;
  onToggleSettings(): void;
  open: boolean; // 一覧を開いているか
  onToggleOpen(): void;
  width: number; // 開いているときの幅(px)。一覧の右の仕切り(Splitter)で変えられる
}

export function ConversationList({
  list, currentId, generatingId, waitingId, ended, disabled, onOpen, onNew, onDelete, onRename, renamingId, onStartRename, onEndRename,
  settingsOpen, onToggleSettings, open, onToggleOpen, width,
}: Props) {
  // 削除の確認を出している会話。消すと戻せないので、もう一度押してもらう
  const [confirming, setConfirming] = useState<string | null>(null);

  const markOf = (id: string): Mark | null =>
    id === generatingId ? (id === waitingId ? "waiting" : "generating") : ended[id] ?? null;
  // 畳んでいるときにハンバーガーに出す印。消した会話が ended に残っていても出さないよう、一覧にある会話だけを見る
  const marks = list.map(c => markOf(c.id));
  const menuMark = open ? null : MARK_ORDER.find(m => marks.includes(m)) ?? null;

  const menu = (
    <button
      className="icon-btn menu-btn"
      title={(open ? "会話の一覧を閉じる" : "会話の一覧を開く") + (menuMark ? `(${MARK_BADGE_LABEL[menuMark]})` : "")}
      onClick={onToggleOpen}
    >
      <LuMenu />
      {menuMark && <span className={"dot badge " + menuMark} />}
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
          const mark = markOf(c.id);
          const cls = ["conversation", c.id === currentId && "current", generating && "generating"].filter(Boolean).join(" ");
          if (renamingId === c.id) {
            return (
              <li key={c.id} className={cls + " editing"}>
                <TitleInput
                  initial={c.title}
                  onDone={title => {
                    onEndRename();
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
                title={`${title}(ダブルクリックか F2 で名前を変更)`}
                disabled={disabled}
                onClick={() => onOpen(c.id)}
                onDoubleClick={() => onStartRename(c.id)}
              >
                {mark && <span className={"dot " + mark} title={MARK_LABEL[mark]} />}
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
        <AppName />
        <button className={"icon-btn " + settingsCls} title="設定" onClick={onToggleSettings}><LuSettings /></button>
      </div>
    </nav>
  );
}

// Mark は会話の名前の左に出す印の種類(CSS の .dot.<種類>)。
type Mark = "waiting" | "generating" | Ended;

// MARK_ORDER は、畳んだ一覧のハンバーガーに1つだけ出すときに、どれを先に出すか(気にしてほしい順)
const MARK_ORDER: Mark[] = ["waiting", "error", "generating", "done"];

const MARK_LABEL: Record<Mark, string> = {
  waiting: "ツールの実行の確認待ち",
  error: "エラーで終わった(開くとエラーが出ます)",
  generating: "返事を生成中",
  done: "返事が終わった",
};

// MARK_BADGE_LABEL は、ハンバーガーの印にマウスを乗せたときの説明(どの会話かは一覧を開くと分かる)
const MARK_BADGE_LABEL: Record<Mark, string> = {
  waiting: "ツールの実行の確認を待っている会話があります",
  error: "エラーで終わった会話があります",
  generating: "返事を生成中の会話があります",
  done: "返事が終わった会話があります",
};

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
    if (e.key === "Escape") {
      // 取り消すだけにする。重ねて出した一覧を Esc で閉じる仕組み(App.tsx)まで届けない
      e.stopPropagation();
      finish(null);
    }
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
