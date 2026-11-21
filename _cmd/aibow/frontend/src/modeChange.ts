// modeChange.ts: モード(アプリモード / サーバーモード・つなぎ先)を変えたことを、画面全体の入れ物(RestartGate)に知らせる(0051 の決定事項 3)。
//
// 変える操作は設定の画面の「サーバー」のタブ(ServerSettings)にあり、ふだんの設定の画面とログインの画面の設定の2か所から開く。
// どちらで変えても、その場で「起動し直してください」の画面に移したいので、部品の親子を通さずに、ここで1か所に知らせる。
import type { ServerConnection } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";

type Listener = (conn: ServerConnection) => void;
const listeners = new Set<Listener>();

// modeChanged は、つなぐ・戻すの操作のあとの今のモード(Go の ServerConnection)を知らせる。
// pending が空でなければ、起動し直すまで前のモードのまま動いている
export function modeChanged(conn: ServerConnection): void {
  listeners.forEach(f => f(conn));
}

export function onModeChanged(f: Listener): () => void {
  listeners.add(f);
  return () => {
    listeners.delete(f);
  };
}
