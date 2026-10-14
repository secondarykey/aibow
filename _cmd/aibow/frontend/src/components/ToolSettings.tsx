// 設定の画面の「ツール」: ツールを使う / 使わない(この PC の既定 = tools.json)。
//
// ツールはグループに分けて出す: 組み込み(aibow の中で動くツール)と、MCP のサーバーごと。
// グループのチェックは「新しい会話で最初から使う」の意味(使えなくするのではない。会話の「この会話のツール」からはいつでも選べる)。
// グループは、チェックしていてもいなくても畳んで出し、▶ で開いてツールごとに選べる(ツールの多いサーバーで画面が長くならないように。0009)。
//
// ツールの扱いは「会話ごとの上書き → この PC の既定 → 決まりの既定」の順に決まる(Go の Conversations.toolPolicy)。
// ここで変えるのは、この PC の既定のうち「使わない」だけ(既定では使わない、の意味。会話の設定の画面で、会話ごとに使うにできる)。
// 使わないツールはモデルに見せない(説明もプロンプトに入らない)ので、
// 会話の上限の節約になる。「確認しない」は後日、画面から選べるようにする(今は tools.json を手で書いたときだけ効く)。
//
// MCP のサーバーのツールの一覧は、サーバーを最後に起動したときに Go が tools.json に覚えたもの。
// 開いたグループで一覧がまだ無ければ、Go がサーバーを一時的に起動して受け取る(ChatService.LoadToolList)。読み直すボタンでも受け取り直せる。
import { useCallback, useEffect, useRef, useState } from "react";
import { LuChevronDown, LuChevronRight, LuLoaderCircle, LuRefreshCw } from "react-icons/lu";
import { ChatService } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import { ToolPolicy } from "../../bindings/github.com/secondarykey/aibow";
import type { KnownTool, ToolPrefsView } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { errorMessage } from "../errors";
import { TOOL_LABELS } from "../toolLabels";
import { BUILTIN } from "./ToolBar";

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
  const [opened, setOpened] = useState<Record<string, boolean>>({}); // ▶ で開いたグループ(最初は全部畳む)

  const reload = useCallback(() => {
    ChatService.ToolPrefs().then(setPrefs, err => setError(errorMessage(err)));
  }, []);
  useEffect(() => {
    reload();
    ChatService.BuiltinTools().then(t => setLocal(t ?? []), err => setError(errorMessage(err)));
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

  // 開いているグループで一覧がまだ無ければ、自動で受け取る
  useEffect(() => {
    if (!prefs) return;
    for (const s of servers) {
      if (opened[s.name] && (prefs.known?.[s.name]?.length ?? 0) === 0 && !tried.current.has(s.name)) {
        tried.current.add(s.name);
        loadList(s.name);
      }
    }
  }, [prefs, servers, loadList, opened]);

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
  // グループのチェック: 新しい会話で最初から使うか
  const setGroup = (group: string, on: boolean) => after(ChatService.SetGroupDefault(group, on));
  const toggleOpen = (group: string) => setOpened(o => ({ ...o, [group]: !o[group] }));

  return (
    <div className="tool-settings">
      {(error || prefs.error) && <div className="msg error">{error || prefs.error}</div>}
      <ToolGroup
        name={BUILTIN}
        title="aibow の中で動くツール。チェックすると、新しい会話で最初から使う"
        on={!prefs.notDefault.builtin}
        open={opened[""]}
        onToggleOpen={() => toggleOpen("")}
        onToggle={on => setGroup("", on)}
        tools={local}
        prefs={prefs}
        groupPolicy={prefs.builtin}
        label={t => TOOL_LABELS[t.name] ?? t.description}
        onSetTools={setTools}
      />
      {servers.map(s => (
        <ToolGroup
          key={s.name}
          name={s.name}
          title={(s.http ? "URL でつなぐ MCP のサーバー" : "aibow が起動する MCP のサーバー") + "。チェックすると、新しい会話で最初から使う"}
          on={!prefs.notDefault.servers?.includes(s.name)}
          open={opened[s.name]}
          onToggleOpen={() => toggleOpen(s.name)}
          onToggle={on => setGroup(s.name, on)}
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

// ToolGroup はツールのグループ1つ(組み込み / MCP のサーバー1つ)。
// チェック(新しい会話で最初から使う)にかかわらず畳んで出し、▶ で開ける
function ToolGroup({ name, title, on, open, onToggleOpen, onToggle, tools, prefs, groupPolicy, prefix = "", label, onSetTools, loading, listError, onReload }: {
  name: string;
  title: string;
  on: boolean;
  open?: boolean;
  onToggleOpen(): void;
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
  const shown = !!open;
  return (
    <div className={"tool-group" + (on ? " on" : "")}>
      <div className="tool-group-head">
        <button type="button" className="tool-open" onClick={onToggleOpen} title={open ? "畳む" : "ツールの一覧を開く"}>
          {open ? <LuChevronDown /> : <LuChevronRight />}
        </button>
        <label className="check" title={title}>
          <input type="checkbox" checked={on} onChange={e => onToggle(e.target.checked)} />
          <span className="tool-group-name">{name}</span>
        </label>
        {tools.length > 0 && <span className="tool-count">{using} / {tools.length}</span>}
        {onReload && shown && (
          <button type="button" className="tool-reload" disabled={loading} onClick={onReload} title="ツールの一覧を読み直す(サーバーを一時的に起動します)">
            {loading ? <LuLoaderCircle className="spin" /> : <LuRefreshCw />}
          </button>
        )}
        {shown && tools.length > 0 && (
          <span className="tool-bulk">
            <button type="button" onClick={() => onSetTools(tools.map(t => ({ name: t.name, policy: ToolPolicy.$zero })))}>すべて使う</button>
            <button type="button" onClick={() => onSetTools(tools.map(t => ({ name: t.name, policy: ToolPolicy.ToolOff })))}>すべて使わない</button>
          </span>
        )}
      </div>
      {shown && (
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
