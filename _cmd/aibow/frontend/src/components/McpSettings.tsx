// 設定の画面の「MCP のツール」: ツールを使う / 使わない(この PC の既定 = tools.json)。
//
// ツールの扱いは「会話ごとの上書き → この PC の既定 → 決まりの既定(確認する)」の順に決まる(Go の Conversations.toolPolicy)。
// ここで変えるのは、この PC の既定のうち「使わない」だけ。「使わない」にしたツールはモデルに見せない(説明もプロンプトに入らない)ので、
// 会話の上限の節約になる。「確認しない」は後日、画面から選べるようにする(今は tools.json を手で書いたときだけ効く)。
//
// ツールの一覧は、サーバーを最後に起動したときに Go が tools.json に覚えたもの。
// 一覧がまだ無いサーバーは、この欄を開いたときに Go がサーバーを一時的に起動して受け取る(ChatService.LoadToolList)。
// 会話で使う前に「このツールは使わない」を決められるようにするため。読み直すボタンでも受け取り直せる。
import { useCallback, useEffect, useRef, useState } from "react";
import { LuLoaderCircle, LuRefreshCw } from "react-icons/lu";
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

  const [loading, setLoading] = useState<Record<string, boolean>>({});   // ツールの一覧を受け取っている途中のサーバー
  const [listErrors, setListErrors] = useState<Record<string, string>>({}); // ツールの一覧を受け取れなかった理由
  const tried = useRef(new Set<string>()); // 一覧が無いので自動で受け取りにいったサーバー(失敗しても繰り返さない)

  const reload = useCallback(() => {
    ChatService.ToolPrefs().then(setPrefs, err => setError(errorMessage(err)));
  }, []);
  useEffect(reload, [reload]);

  // サーバーを一時的に起動して、ツールの一覧を受け取る(npx の初回などは数十秒かかる)
  const loadList = useCallback((name: string) => {
    setLoading(l => ({ ...l, [name]: true }));
    setListErrors(e => ({ ...e, [name]: "" }));
    ChatService.LoadToolList(name)
      .then(reload, err => setListErrors(e => ({ ...e, [name]: errorMessage(err) })))
      .finally(() => setLoading(l => ({ ...l, [name]: false })));
  }, [reload]);

  // 一覧がまだ無いサーバーは、開いたときに自動で受け取る
  useEffect(() => {
    if (!prefs) return;
    for (const s of servers) {
      if ((prefs.known?.[s.name]?.length ?? 0) === 0 && !tried.current.has(s.name)) {
        tried.current.add(s.name);
        loadList(s.name);
      }
    }
  }, [prefs, servers, loadList]);

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
              <button
                type="button"
                className="mcp-reload"
                disabled={loading[s.name]}
                onClick={() => loadList(s.name)}
                title="ツールの一覧を読み直す(サーバーを一時的に起動します)"
              >
                {loading[s.name] ? <LuLoaderCircle className="spin" /> : <LuRefreshCw />}
              </button>
              {known.length > 0 && <span className="mcp-count">{using} / {known.length}</span>}
              {known.length > 0 && (
                <span className="mcp-bulk">
                  <button type="button" onClick={() => set(known.map(t => ({ name: t.name, policy: useTool(s.name) })))}>すべて使う</button>
                  <button type="button" onClick={() => set(known.map(t => ({ name: t.name, policy: ToolPolicy.ToolOff })))}>すべて使わない</button>
                </span>
              )}
            </div>
            {listErrors[s.name] && <div className="msg error">{listErrors[s.name]}</div>}
            {known.length === 0 ? (
              <p className="tool-none">{loading[s.name] ? "(ツールの一覧を読み込み中…)" : "(ツールの一覧はまだありません)"}</p>
            ) : (
              <div className="mcp-tools">
                {known.map(t => {
                  const p = policyOf(s.name, t.name);
                  // モデルに見せる名前は「サーバーの名前__ツールの名前」。サーバーの名前の部分は見出しにあるので外す
                  const prefix = s.name.replace(/[^A-Za-z0-9_-]/gu, "_") + "__";
                  const short = t.name.startsWith(prefix) ? t.name.slice(prefix.length) : t.name;
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
