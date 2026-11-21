// 「起動し直してください」の画面(0051 の決定事項 3〜5。デスクトップのアプリだけ)。
//
// モード(アプリモード / サーバーモード・つなぎ先)を変えても、aibow は起動し直すまで前のモードのまま動く(0019 の決定事項 1。自分では起動し直さない)。
// 以前はそのまま前の画面を使えたので、どちらで動いているのか分かりにくく、消したトークンで aibow-server を呼び続けて断られていた
// (aibow-server のログの「…断りました」の正体。0051 の 4)。そこで、変えたらその場でアプリ全体をこの画面で覆い、
// 会話の一覧・入力欄を出さない(App を描かないので、aibow-server には何も送らない)。
//
// ここから開ける設定は「サーバー」のタブだけ。選び直して今動いているモードに戻した(pending が空になった)ら、画面を読み直して元に戻す。
// サーバーモードで「アプリモードに戻す」を押したときはトークンをもう消しているので、読み直すとログインの画面になる(Go の Account が NeedLogin を返す)。
import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { LuSettings } from "react-icons/lu";
import type { ServerConnection } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import { isBrowser } from "../backend";
import { onModeChanged } from "../modeChange";
import { WithTitleBar } from "./Login";
import { LoginSettings } from "./LoginSettings";
import { pendingText } from "./ServerSettings";

export function RestartGate({ children }: { children: ReactNode }) {
  const [conn, setConn] = useState<ServerConnection | null>(null);
  const [settings, setSettings] = useState(false);
  const open = useRef(false); // この画面を出しているか(知らせの中で読むので ref でも持つ)
  useEffect(() => onModeChanged(c => {
    if (c.pending) {
      open.current = true;
      setConn(c);
      setSettings(false);
    } else if (open.current) {
      // 今動いているモードに戻した。描き直すのではなく読み直す(ログインの様子を起動のときと同じ流れで決め直すため)
      location.reload();
    }
  }), []);

  if (isBrowser || !conn) return <>{children}</>;
  if (settings) {
    return <WithTitleBar><LoginSettings serverOnly onClose={() => setSettings(false)} /></WithTitleBar>;
  }
  return (
    <WithTitleBar>
      <div className="login">
        <div className="login-box restart-box">
          <h1 className="login-title">AIBow</h1>
          <div className="login-note restart-note" role="status">{pendingText(conn)}</div>
          <button type="button" className="with-icon" title="つなぎ先を選び直す(「サーバー」のタブ)" onClick={() => setSettings(true)}>
            <LuSettings /> 設定
          </button>
        </div>
      </div>
    </WithTitleBar>
  );
}
