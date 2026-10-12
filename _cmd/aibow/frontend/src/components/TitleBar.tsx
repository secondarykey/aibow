// 画面の一番上: Frameless ウィンドウのタイトルバー。
//
// Frameless(main.go)にすると OS のタイトルバーが無くなるので、ウィンドウを動かす場所と
// 最小化・最大化・閉じるのボタンを自分で用意する。
// ドラッグできる場所は CSS の --wails-draggable: drag で決める(style.css の .titlebar)。
// @wailsio/runtime がページ全体の mousedown を見ていて、drag の要素で押して動かすと
// Go に「ウィンドウを動かして」と頼む。ボタンには no-drag を付けて、押せるようにする。
import { useEffect, useState } from "react";
import { Window } from "@wailsio/runtime";
import { LuCopy, LuMenu, LuMinus, LuSquare, LuX } from "react-icons/lu";

interface Props {
  listOpen: boolean;
  onToggleList(): void;
}

export function TitleBar({ listOpen, onToggleList }: Props) {
  // 最大化しているかで、ボタンのアイコンを変える(元に戻す = 重なった四角)
  const [maximised, setMaximised] = useState(false);
  useEffect(() => {
    const check = () => { Window.IsMaximised().then(setMaximised).catch(() => {}); };
    check();
    window.addEventListener("resize", check); // 最大化・元に戻すとウィンドウの大きさが変わる
    return () => window.removeEventListener("resize", check);
  }, []);

  return (
    <div className="titlebar" onDoubleClick={() => Window.ToggleMaximise()}>
      <button
        className="icon-btn"
        title={listOpen ? "会話の一覧を閉じる" : "会話の一覧を開く"}
        onClick={onToggleList}
        onDoubleClick={e => e.stopPropagation()}
      >
        <LuMenu />
      </button>
      <div className="window-controls" onDoubleClick={e => e.stopPropagation()}>
        <button title="最小化" onClick={() => Window.Minimise()}><LuMinus /></button>
        <button title={maximised ? "元に戻す" : "最大化"} onClick={() => Window.ToggleMaximise()}>
          {maximised ? <LuCopy /> : <LuSquare />}
        </button>
        <button className="close" title="閉じる" onClick={() => Window.Close()}><LuX /></button>
      </div>
    </div>
  );
}
