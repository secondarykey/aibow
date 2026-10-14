// 会話の設定の画面: 開いている会話だけの設定 / この会話のツール / モデルに渡した文字列
//
// 会話の一覧の各行の歯車で開き、チャットの代わりに会話の場所に出す(設定の画面と同じく別の画面として扱う)。
// 会話は「新しい会話の既定」(設定の画面)の写しから始まり、ここで変えた設定はこの会話にだけ効く。
// 「会話に戻る」・同じ歯車をもう一度・一覧で会話を選ぶ・「+ 新規」・一覧の下の歯車で閉じる。
import { LuArrowLeft } from "react-icons/lu";
import type { ConversationSettings } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { ConversationSettingsForm } from "./ConversationSettingsForm";
import { ConversationTools } from "./ConversationTools";

interface Props {
  title: string;                          // 会話の名前
  settings: ConversationSettings;
  errors: { id: number; text: string }[]; // この画面を開いてから出たエラー(ふだんは会話の表示に出るもの)
  onClose(): void;                        // 会話に戻る
  onChange(patch: Partial<ConversationSettings>): void;
  onMakeDefault(): void;
  lastPrompt: string;
  servers: ServerInfo[]; // settings.json の MCP のサーバー
}

export function ConversationSettingsView({ title, settings, errors, onClose, onChange, onMakeDefault, lastPrompt, servers }: Props) {
  return (
    <section className="settings-view">
      <div className="settings-inner">
        <div className="settings-header">
          <h1>
            会話の設定
            <span className="settings-target">{title}</span>
          </h1>
          <button className="back" onClick={onClose} title="会話の設定を閉じて、会話に戻る"><LuArrowLeft /> 会話に戻る</button>
        </div>
        {errors.map(e => <div key={e.id} className="msg error">{e.text}</div>)}

        <ConversationSettingsForm settings={settings} onChange={onChange} />
        <button className="make-default" onClick={onMakeDefault}>この設定を新しい会話の既定にする</button>

        <h2 className="section" title="この会話で使うツール(入力欄の上の「この会話のツール」と同じ)">ツール</h2>
        <ConversationTools settings={settings} servers={servers} onChange={onChange} />

        <h2 className="section">モデルに渡した文字列</h2>
        <p className="hint">
          モデルは会話を覚えていません。送信のたびに、これまでの会話をこの書式の1本の文字列にして読ませています
          (会話の上限を超えたら、compact で収めたもの。要約があれば system の後ろに入ります)。
          最後の <code>&lt;|im_start|&gt;assistant</code> の続きを書かせることで返事になります。
        </p>
        <pre className="last-prompt">{lastPrompt || "(まだ送信していません)"}</pre>
      </div>
    </section>
  );
}
