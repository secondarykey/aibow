// stored.ts: 画面の見た目の好み(一覧の幅・一覧を開いているか・最後に開いていた会話)を、次の起動まで覚えておく。
//
// 覚える先は WebView2 の localStorage。画面の見た目だけの好みなので、Go の設定(DB)には入れない。
// ウィンドウの位置と大きさは、ウィンドウを作る前に Go が知る必要があるので、こちらではなく Go が覚える(windowstate.go)。
//
// localStorage は使えないこともある(壊れている・無効にされている)。読めなければ null、書けなければ何もしない
// (覚えておけなくても、今の起動の間は効いているので困らない)。

export const STORED_LIST_WIDTH = "aibow.listWidth";
export const STORED_LIST_OPEN = "aibow.listOpen";
export const STORED_LAST_CONVERSATION = "aibow.lastConversation";

export function loadStored(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

// value が null なら消す
export function saveStored(key: string, value: string | null) {
  try {
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
  } catch {
    // 覚えておけなくても困らない
  }
}
