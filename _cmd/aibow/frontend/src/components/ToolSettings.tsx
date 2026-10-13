// 設定の画面の「ツール」: ツールを使う / 使わない(この PC の既定 = tools.json)。
//
// ツールはグループに分けて出す: ローカル(aibow の中で動くツール)と、MCP のサーバーごと。
// グループの「使う」を外すと、そのグループのツールは全部使わない(一覧も畳む)。使うにすると、一覧を開いてツールごとに選べる。
//
// ツールの扱いは「会話ごとの上書き → この PC の既定 → 決まりの既定」の順に決まる(Go の Conversations.toolPolicy)。
// ここで変えるのは、この PC の既定のうち「使わない」だけ。使わないツールはモデルに見せない(説明もプロンプトに入らない)ので、
// 会話の上限の節約になる。「確認しない」は後日、画面から選べるようにする(今は tools.json を手で書いたときだけ効く)。
//
// MCP のサーバーのツールの一覧は、サーバーを最後に起動したときに Go が tools.json に覚えたもの。
// 使うグループで一覧がまだ無ければ、Go がサーバーを一時的に起動して受け取る(ChatService.LoadToolList)。読み直すボタンでも受け取り直せる。
import { useCallback, useEffect, useRef, useState } from "react";
import { LuLoaderCircle, LuRefreshCw } from "react-icons/lu";
import { ChatService } from "../../bindings/aibow/cmd/aibow";
import { ToolPolicy } from "../../bindings/aibow";
import type { KnownTool, ToolPrefsView } from "../../bindings/aibow";
import type { ServerInfo } from "../../bindings/aibow/mcpclient";
import { errorMessage } from "../errors";
import { TOOL_LABELS } from "../toolLabels";

interface Props {
  onChanged(): void; // プロンプトに入るツール・使えるサーバーが変わったので、メーターと MCP の一覧を読み直す
}

export function ToolSettings({ onChanged }: Props) {
  const [prefs, setPrefs] = useState<ToolPrefsView | null>(null);
  const [local, setLocal] = useState<KnownTool[]>([]);
  const [servers, setServers] = useState<ServerInfo[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState<Record<string, boolean>>({});   // ツールの一覧を受け取っている途中のサーバー
  const [listErrors, setListErrors] = useState<Record<string, string>>({}); // ツールの一覧を受け取れなかった理由
  const tried = useRef(new Set<string>()); // 一覧が無いので自動で受け取りにいったサーバー(失敗しても繰り返さない)

  const reload = useCallback(() => {
    ChatService.ToolPrefs().then(setPrefs, err => setError(errorMessage(err)));
  }, []);
  useEffect(() => {
    reload();
    ChatService.LocalTools().then(t => setLocal(t ?? []), err => setError(errorMessage(err)));
    ChatService.AllMCPServers().then(s => setServers(s ?? []), err => setError(errorMessage(err)));
  }, [reload]);

  // サーバーを一時的に起動して、ツールの一覧を受け取る(npx の初回などは数十秒かかる)
  const loadList = useCallback((name: string) => {
    setLoading(l => ({ ...l, [name]: true }));
    setListErrors(e => ({ ...e, [name]: "" }));
    ChatService.LoadToolList(name)
      .then(reload, err => setListErrors(e => ({ ...e, [name]: errorMessage(err) })))
      .finally(() => setLoading(l => ({ ...l, [name]: false })));
  }, [reload]);

  // 使うサーバーで一覧がまだ無ければ、自動で受け取る(使わないサーバーは、使うにしたときに受け取る)
  useEffect(() => {
    if (!prefs) return;
    for (const s of servers) {
      const on = prefs.servers?.[s.name] !== ToolPolicy.ToolOff;
      if (on && (prefs.known?.[s.name]?.length ?? 0) === 0 && !tried.current.has(s.name)) {
        tried.current.add(s.name);
        loadList(s.name);
      }
    }
  }, [prefs, servers, loadList]);

  if (!prefs) return error ? <div className="msg error">{error}</div> : null;

  // 変えたら tools.json を読み直し、メーターと MCP の一覧も読み直してもらう
  const after = (p: Promise<unknown>) => {
    setError("");
    p.catch(err => setError(errorMessage(err))).finally(() => {
      reload();
      onChanged();
    });
  };
  const setTools = (changes: { name: string; policy: ToolPolicy }[]) =>
    after(Promise.all(changes.map(c => ChatService.SetToolPolicy(c.name, c.policy))));
  // グループの「使う」: 使わないなら off、使うなら決めていない状態に戻す
  const setGroup = (group: string, on: boolean) =>
    after(ChatService.SetGroupPolicy(group, on ? ToolPolicy.$zero : ToolPolicy.ToolOff));

  return (
    <div className="tool-settings">
      {(error || prefs.error) && <div className="msg error">{error || prefs.error}</div>}
      <ToolGroup
        name="ローカル"
        title="aibow の中で動くツール"
        on={prefs.local !== ToolPolicy.ToolOff}
        onToggle={on => setGroup("", on)}
        tools={local}
        prefs={prefs}
        groupPolicy={prefs.local}
        label={t => TOOL_LABELS[t.name] ?? t.description}
        onSetTools={setTools}
      />
      {servers.map(s => (
        <ToolGroup
          key={s.name}
          name={s.name}
          title={s.http ? "URL でつなぐ MCP のサーバー" : "aibow が起動する MCP のサーバー"}
          on={prefs.servers?.[s.name] !== ToolPolicy.ToolOff}
          onToggle={on => {
            setGroup(s.name, on);
            if (on && (prefs.known?.[s.name]?.length ?? 0) === 0) loadList(s.name);
          }}
          tools={prefs.known?.[s.name] ?? []}
          prefs={prefs}
          groupPolicy={prefs.servers?.[s.name] ?? ToolPolicy.$zero}
          // モデルに見せる名前は「サーバーの名前__ツールの名前」。サーバーの名前の部分は見出しにあるので外す
          prefix={s.name.replace(/[^A-Za-z0-9_-]/gu, "_") + "__"}
          label={t => t.description}
          onSetTools={setTools}
          loading={loading[s.name]}
          listError={listErrors[s.name]}
          onReload={() => loadList(s.name)}
        />
      ))}
      {prefs.path && <p className="hint tool-path" title="この PC での、ツールの扱いの既定の保存先">{prefs.path}</p>}
    </div>
  );
}

// ToolGroup はツールのグループ1つ(ローカル / MCP のサーバー1つ)。使うときだけ、ツールの一覧を開く
function ToolGroup({ name, title, on, onToggle, tools, prefs, groupPolicy, prefix = "", label, onSetTools, loading, listError, onReload }: {
  name: string;
  title: string;
  on: boolean;
  onToggle(on: boolean): void;
  tools: KnownTool[];
  prefs: ToolPrefsView;
  groupPolicy: ToolPolicy;
  prefix?: string;
  label(t: KnownTool): string;
  onSetTools(changes: { name: string; policy: ToolPolicy }[]): void;
  loading?: boolean;
  listError?: string;
  onReload?(): void;
}) {
  const using = tools.filter(t => prefs.tools?.[t.name] !== ToolPolicy.ToolOff).length;
  return (
    <div className={"tool-group" + (on ? " on" : "")}>
      <div className="tool-group-head">
        <label className="check" title={title}>
          <input type="checkbox" checked={on} onChange={e => onToggle(e.target.checked)} />
          <span className="tool-group-name">{name}</span>
        </label>
        {on && tools.length > 0 && <span className="tool-count">{using} / {tools.length}</span>}
        {onReload && on && (
          <button type="button" className="tool-reload" disabled={loading} onClick={onReload} title="ツールの一覧を読み直す(サーバーを一時的に起動します)">
            {loading ? <LuLoaderCircle className="spin" /> : <LuRefreshCw />}
          </button>
        )}
        {on && tools.length > 0 && (
          <span className="tool-bulk">
            <button type="button" onClick={() => onSetTools(tools.map(t => ({ name: t.name, policy: ToolPolicy.$zero })))}>すべて使う</button>
            <button type="button" onClick={() => onSetTools(tools.map(t => ({ name: t.name, policy: ToolPolicy.ToolOff })))}>すべて使わない</button>
          </span>
        )}
      </div>
      {on && (
        <div className="tool-group-body">
          {listError && <div className="msg error">{listError}</div>}
          {tools.length === 0 ? (
            <p className="tool-none">{loading ? "(ツールの一覧を読み込み中…)" : "(ツールの一覧はまだありません)"}</p>
          ) : (
            tools.map(t => {
              const p = prefs.tools?.[t.name];
              const allow = p === ToolPolicy.ToolAllow || (p === undefined && groupPolicy === ToolPolicy.ToolAllow);
              const short = t.name.startsWith(prefix) ? t.name.slice(prefix.length) : t.name;
              return (
                <label key={t.name} className="check tool-item" title={`${t.name}\n${t.description}`}>
                  <input
                    type="checkbox"
                    checked={p !== ToolPolicy.ToolOff}
                    onChange={e => onSetTools([{ name: t.name, policy: e.target.checked ? ToolPolicy.$zero : ToolPolicy.ToolOff }])}
                  />
                  <code>{short}</code>
                  {allow && <span className="tool-allow" title="tools.json で「確認しない」にしてあります">確認しない</span>}
                  <span className="tool-desc">{label(t)}</span>
                </label>
              );
            })
          )}
        </div>
      )}
    </div>
  );
}
