// 「起動し直してください」の画面(0051 の決定事項 3〜5。デスクトップのアプリだけ)。
//
// モード(アプリモード / サーバーモード・つなぎ先)を変えても、aibow は起動し直すまで前のモードのまま動く(0019 の決定事項 1。自分では起動し直さない)。
// 以前はそのまま前の画面を使えたので、どちらで動いているのか分かりにくく、消したトークンで aibow-server を呼び続けて断られていた
// (aibow-server のログの「…断りました」の正体。0051 の 4)。そこで、変えたらその場でアプリ全体をこの画面で覆い、
// 会話の一覧・入力欄を出さない(App を描かないので、aibow-server には何も送らない)。
//
// ボタンは置かず、警告のお知らせだけにする(2026-11-21、ユーザーと決め直した)。
// 初めは設定を開くボタンを置いたが、ボタンが1つだけあると人は考えずに押しやすく、「起動し直す」が薄れる。
// 間違えて変えたときは、起動し直してから設定の画面の「サーバー」で戻せる(起動し直しが1回増えるだけ)。閉じるのはタイトルバーの ✕
import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { LuTriangleAlert } from "react-icons/lu";
import type { ServerConnection } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import { isBrowser } from "../backend";
import { onModeChanged } from "../modeChange";
import { WithTitleBar } from "./Login";

export function RestartGate({ children }: { children: ReactNode }) {
  const [conn, setConn] = useState<ServerConnection | null>(null);
  // 一度覆ったら、起動し直すまでこのまま(この画面からはモードを変えられない)
  useEffect(() => onModeChanged(c => {
    if (c.pending) setConn(c);
  }), []);

  if (isBrowser || !conn) return <>{children}</>;
  return (
    <WithTitleBar>
      <div className="login">
        <div className="login-box restart-box">
          <h1 className="login-title">AIBow</h1>
          <div className="restart-note" role="alert" title="設定はもう保存してあります。戻すときは、起動し直してから設定の画面の「サーバー」で変えてください">
            <LuTriangleAlert className="icon" />
            <span>{restartText(conn)}</span>
          </div>
        </div>
      </div>
    </WithTitleBar>
  );
}

// restartText は、起動し直してほしいことのお知らせ。することを先に書き、起動し直したらどうなるかを後ろに(0048)
function restartText(conn: ServerConnection): string {
  if (conn.pending === "direct") return "aibow を起動し直してください。起動し直すと、アプリモードに戻ります";
  const base = `aibow を起動し直してください。起動し直すと、aibow-server(${conn.pendingUrl})につなぎます`;
  return conn.pendingLogin ? base + "(つないだら、ログインの画面でログインします)" : base;
}
