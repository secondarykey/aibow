// 設定の画面の「MCP のツール」: ツールを使う / 使わない(この PC の既定 = tools.json)。
//
// ツールの扱いは「会話ごとの上書き → この PC の既定 → 決まりの既定(確認する)」の順に決まる(Go の Conversations.toolPolicy)。
// ここで変えるのは、この PC の既定のうち「使わない」だけ。「使わない」にしたツールはモデルに見せない(説明もプロンプトに入らない)ので、
// 会話の上限の節約になる。「確認しない」は後日、画面から選べるようにする(今は tools.json を手で書いたときだけ効く)。
//
// ツールの一覧は、サーバーを最後に起動したときに Go が tools.json に覚えたもの。一度も起動していないサーバーは、一覧がまだ無い。
import { useCallback, useEffect, useState } from "react";
import { ChatService } from "../../bindings/aibow/cmd/aibow";
import { ToolPolicy } from "../../bindings/aibow";
import type { KnownTool, ToolPrefsView } from "../../bindings/aibow";
import type { ServerInfo } from "../../bindings/aibow/mcpclient";
import { errorMessage } from "../errors";

interface Props {
  servers: ServerInfo[];
  onChanged(): void; // プロンプトに入るツールが変わったので、メーターを数え直す
}

export function McpSettings({ servers, onChanged }: Props) {
  const [prefs, setPrefs] = useState<ToolPrefsView | null>(null);
  const [error, setError] = useState("");

  const reload = useCallback(() => {
    ChatService.ToolPrefs().then(setPrefs, err => setError(errorMessage(err)));
  }, []);
  useEffect(reload, [reload]);

  if (servers.length === 0 || !prefs) return null;

  // 実際の扱い: ツールごと → サーバーごと(どちらも決めていなければ「確認する」)
  const policyOf = (server: string, tool: string) => prefs.tools?.[tool] ?? prefs.servers?.[server] ?? ToolPolicy.ToolAsk;
  const set = (changes: { name: string; policy: ToolPolicy }[]) => {
    setError("");
    Promise.all(changes.map(c => ChatService.SetToolPolicy(c.name, c.policy)))
      .catch(err => setError(errorMessage(err)))
      .finally(() => {
        reload();
        onChanged();
      });
  };
  // 使う: 決めていない状態に戻す(サーバーごとに「使わない」にしてあるときだけ、ツールに「確認する」を書く)
  const useTool = (server: string): ToolPolicy =>
    prefs.servers?.[server] === ToolPolicy.ToolOff ? ToolPolicy.ToolAsk : ToolPolicy.$zero;

  return (
    <div className="mcp-settings">
      {(error || prefs.error) && <div className="msg error">{error || prefs.error}</div>}
      {servers.map(s => {
        const known: KnownTool[] = prefs.known?.[s.name] ?? [];
        const using = known.filter(t => policyOf(s.name, t.name) !== ToolPolicy.ToolOff).length;
        return (
          <div key={s.name} className="mcp-server">
            <div className="mcp-server-head">
              <span className="mcp-server-name">{s.name}</span>
              {known.length > 0 && <span className="mcp-count">{using} / {known.length}</span>}
              {known.length > 0 && (
                <span className="mcp-bulk">
                  <button type="button" onClick={() => set(known.map(t => ({ name: t.name, policy: useTool(s.name) })))}>すべて使う</button>
                  <button type="button" onClick={() => set(known.map(t => ({ name: t.name, policy: ToolPolicy.ToolOff })))}>すべて使わない</button>
                </span>
              )}
            </div>
            {known.length === 0 ? (
              <p className="tool-none" title="会話でこのサーバーを選んで一度送信すると、ツールの一覧が出ます">(ツールの一覧はまだありません)</p>
            ) : (
              <div className="mcp-tools">
                {known.map(t => {
                  const p = policyOf(s.name, t.name);
                  const short = t.name.startsWith(s.name.replace(/[^A-Za-z0-9_-]/gu, "_") + "__") ? t.name.slice(t.name.indexOf("__") + 2) : t.name;
                  return (
                    <label key={t.name} className="check mcp-tool" title={`${t.name}\n${t.description}`}>
                      <input
                        type="checkbox"
                        checked={p !== ToolPolicy.ToolOff}
                        onChange={e => set([{ name: t.name, policy: e.target.checked ? useTool(s.name) : ToolPolicy.ToolOff }])}
                      />
                      <code>{short}</code>
                      {p === ToolPolicy.ToolAllow && <span className="mcp-allow" title="tools.json で「確認しない」にしてあります">確認しない</span>}
                      <span className="mcp-desc">{t.description}</span>
                    </label>
                  );
                })}
              </div>
            )}
          </div>
        );
      })}
      {prefs.path && <p className="hint mcp-path" title="この PC での、ツールの扱いの既定の保存先">{prefs.path}</p>}
    </div>
  );
}
