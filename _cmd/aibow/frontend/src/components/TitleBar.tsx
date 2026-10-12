// チャット側の一番上: Frameless ウィンドウのタイトルバー。開いている会話の名前を出す。
//
// Frameless(main.go)にすると OS のタイトルバーが無くなるので、ウィンドウを動かす場所と
// 最小化・最大化・閉じるのボタンを自分で用意する。
// ドラッグできる場所は CSS の --wails-draggable: drag で決める(style.css の .titlebar)。
// @wailsio/runtime がページ全体の mousedown を見ていて、drag の要素で押して動かすと
// Go に「ウィンドウを動かして」と頼む。ボタンには no-drag を付けて、押せるようにする。
// 会話の一覧の上(ハンバーガーの横)もドラッグできる(ConversationList.tsx)。
import { useEffect, useState } from "react";
import type { MouseEvent } from "react";
import { Window } from "@wailsio/runtime";
import { LuCopy, LuMinus, LuSquare, LuX } from "react-icons/lu";

// toggleMaximiseOnDoubleClick は、ドラッグできる場所のダブルクリックで最大化と元に戻すを切り替える
// (OS のタイトルバーと同じ動き)。ボタンの上のダブルクリックは無視する。
export function toggleMaximiseOnDoubleClick(e: MouseEvent) {
  if (e.target instanceof Element && e.target.closest("button, input")) return;
  Window.ToggleMaximise();
}

export function TitleBar({ title }: { title: string }) {
  // 最大化しているかで、ボタンのアイコンを変える(元に戻す = 重なった四角)
  const [maximised, setMaximised] = useState(false);
  useEffect(() => {
    const check = () => { Window.IsMaximised().then(setMaximised).catch(() => {}); };
    check();
    window.addEventListener("resize", check); // 最大化・元に戻すとウィンドウの大きさが変わる
    return () => window.removeEventListener("resize", check);
  }, []);

  return (
    <div className="titlebar" onDoubleClick={toggleMaximiseOnDoubleClick}>
      <span className="conversation-title" title={title}>{title}</span>
      <div className="window-controls">
        <button title="最小化" onClick={() => Window.Minimise()}><LuMinus /></button>
        <button title={maximised ? "元に戻す" : "最大化"} onClick={() => Window.ToggleMaximise()}>
          {maximised ? <LuCopy /> : <LuSquare />}
        </button>
        <button className="close" title="閉じる" onClick={() => Window.Close()}><LuX /></button>
      </div>
    </div>
  );
}
