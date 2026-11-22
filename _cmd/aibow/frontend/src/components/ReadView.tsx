// ほかの人の会話を読むだけで開く画面(0018 の 11-2)。共有された会話と、管理者が「利用者」のタブから開いた会話。
//
// 吹き出しはいつもの会話と同じ(ChatView)だが、操作(直す・作り直す・モデルに渡さない)を出さない(readOnly)。
// 入力欄の代わりに「写して続ける」(写して自分の会話にする。決定事項 2・24)。
// 上の帯で、誰の会話か・どう共有されたか(自分あて・グループ・全員。0055 の決定事項 25)・管理者として読んでいるか(記録が残る。決定事項 25)を出す。
// 帯の文は、一覧の行の説明と同じ言い方にする(directory.ts の sharedTo)。
import { useMemo } from "react";
import { LuCopy, LuEye, LuShare2 } from "react-icons/lu";
import type { SharedRead } from "../../bindings/github.com/secondarykey/aibow";
import { messageItems, type Item } from "../transcript";
import { sharedBanner, showName } from "../directory";
import { ChatView } from "./ChatView";

const noop = () => {};

export function ReadView({ read, pinQuestion, onCopy }: { read: SharedRead; pinQuestion: boolean; onCopy(): void }) {
  const items: Item[] = useMemo(
    () => messageItems(read.messages ?? [], read.compactions ?? []).map((it, i) => ({ ...it, id: i }) as Item),
    [read],
  );
  const owner = showName(read.owner_name, read.owner_display);
  return (
    <section className="center">
      <div className={"read-banner" + (read.by_admin ? " admin" : "")}>
        {read.by_admin
          ? <><LuEye /> 管理者として {owner} の会話を読んでいます(記録が残ります)</>
          : <><LuShare2 className="received" /> {sharedBanner(read)}</>}
      </div>
      <ChatView
        items={items}
        firstId={null}
        conversationId={read.conversation.id}
        pinQuestion={pinQuestion}
        readOnly
        onStop={noop}
        onAnswer={noop}
        retry={{ disabled: true, onRegenerate: noop, onResend: noop }}
        exclude={{ disabled: true, onChange: noop }}
      />
      <footer className="read-footer">
        <button type="button" className="primary" onClick={onCopy} title="この会話(発言と要約)を複製して、自分の会話として続ける">
          <LuCopy /> 複製して続ける
        </button>
      </footer>
    </section>
  );
}
