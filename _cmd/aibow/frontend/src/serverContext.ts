// serverContext.ts: aibow-server につないでいるか(0015)。
//
// つないでいるときは、作業領域はサーバーが起動のときに決めた大きさで固定になる(0015 の 3)。
// 会話の上限・返事の上限の欄(LimitsForm)など、奥の部品で説明を変えるために使う(props で何段も渡さないため)。
import { createContext } from "react";

export interface ServerInfo {
  url: string;  // つなぎ先
  nCtx: number; // サーバーの作業領域の大きさ(まだ分からなければ 0)
}

// アプリの中で llama.cpp を動かしているときは null
export const ServerContext = createContext<ServerInfo | null>(null);
