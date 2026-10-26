// 設定の画面の「ツール」(ブラウザだけ。0024 の決定事項 21): ブラウザの会話のツールの扱いの、aibow-server 全体の既定。
//
// 組み込み(時刻・計算)と、server.json の mcpServers に書いた MCP のサーバーごとに、グループとツールの扱いを
// 「使わない / 確認する / 確認しない」で決める(Go の server-tools.json)。ツールごとに決めたものはグループより優先する。
// 「使わない」は禁止で、利用者が会話で「使う」にしても使えない(決定事項 22)。
// 変えられるのは管理者とサーバーの PC の上から開いた画面だけ(models.manage と同じ人。Go も断る)。ほかの人には同じ表を、変えられない形で見せる。
//
// MCP のサーバーのツールの一覧は、サーバーを最後に起動したときに Go が覚えたもの。管理者は読み直せる(サーバーを一時的に起動する)。
import { useCallback, useEffect, useState } from "react";
import { LuChevronDown, LuChevronRight, LuLoaderCircle, LuRefreshCw } from "react-icons/lu";
import { ChatService } from "../backend";
import { ToolPolicy } from "../../bindings/github.com/secondarykey/aibow";
import type { KnownTool, ToolPrefsView, ToolState } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { errorMessage } from "../errors";
import { TOOL_LABELS } from "../toolLabels";
import { BUILTIN } from "./ToolBar";

const LABELS: Record<string, string> = {
  [ToolPolicy.ToolOff]: "使わない",
  [ToolPolicy.ToolAsk]: "確認する",
  [ToolPolicy.ToolAllow]: "確認しない",
};
const CHOICES = [ToolPolicy.ToolOff, ToolPolicy.ToolAsk, ToolPolicy.ToolAllow];

export function ServerTools({ servers, manage, onChanged }: {
  servers: ServerInfo[]; // ブラウザに出す MCP のサーバー(server.json の mcpServers。${workdir} を使うものは除いてある)
  manage: boolean;       // 変えてよい人か(管理者・サーバーの PC の上から開いた画面)
  onChanged(): void;     // 既定を変えた(会話のツールの欄とメーターを読み直す)
}) {
  const [builtin, setBuiltin] = useState<KnownTool[]>([]);
  const [prefs, setPrefs] = useState<ToolPrefsView | null>(null);
  const [states, setStates] = useState<Record<string, ToolState>>({}); // ツールの名前 → 今の扱い(グループと決まりの既定も当てたもの)
  const [error, setError] = useState("");
  const [opened, setOpened] = useState<Record<string, boolean>>({});
  const [loading, setLoading] = useState<Record<string, boolean>>({});

  const reload = useCallback(() => {
    ChatService.ToolPrefs().then(setPrefs, err => setError(errorMessage(err)));
    ChatService.ToolDefaults().then(
      groups => setStates(Object.fromEntries((groups ?? []).flatMap(g => g.tools ?? []).map(t => [t.name, t]))),
      err => setError(errorMessage(err)),
    );
  }, []);
  useEffect(() => {
    reload();
    ChatService.BuiltinTools().then(t => setBuiltin(t ?? []), err => setError(errorMessage(err)));
  }, [reload]);

  const change = (p: Promise<unknown>) => {
    setError("");
    p.catch(err => setError(errorMessage(err))).finally(() => {
      reload();
      onChanged();
    });
  };
  const loadList = (name: string) => {
    setLoading(l => ({ ...l, [name]: true }));
    setError("");
    ChatService.LoadToolList(name)
      .then(reload, err => setError(errorMessage(err)))
      .finally(() => setLoading(l => ({ ...l, [name]: false })));
  };

  const groups = [
    { group: "", name: BUILTIN, title: "aibow-server の中で動くツール(時刻・計算)", tools: builtin, prefix: "", policy: prefs?.builtin ?? "" },
    ...servers.map(s => ({
      group: s.name,
      name: s.name,
      title: s.http ? "URL でつなぐ MCP のサーバー" : "aibow-server が起動する MCP のサーバー",
      tools: prefs?.known?.[s.name] ?? [],
      // モデルに見せる名前は「サーバーの名前__ツールの名前」。サーバーの名前の部分は見出しにあるので外す
      prefix: s.name.replace(/[^A-Za-z0-9_-]/gu, "_") + "__",
      policy: prefs?.servers?.[s.name] ?? "",
    })),
  ];

  return (
    <div className="tool-settings server-tools">
      {(error || prefs?.error) && <div className="msg error">{error || prefs?.error}</div>}
      {groups.map(g => {
        const shown = !!opened[g.group];
        const groupOff = g.policy === ToolPolicy.ToolOff;
        return (
          <div key={g.group} className={"tool-group" + (groupOff ? "" : " on")}>
            <div className="tool-group-head">
              <button type="button" className="tool-open" onClick={() => setOpened(o => ({ ...o, [g.group]: !o[g.group] }))} title={shown ? "畳む" : "ツールの一覧を開く"}>
                {shown ? <LuChevronDown /> : <LuChevronRight />}
              </button>
              <span className="tool-group-name" title={g.title}>{g.name}</span>
              <PolicySelect
                value={g.policy}
                unset={g.group === "" ? "決まりの既定(読むだけは確認しない)" : "決まりの既定(確認する)"}
                disabled={!manage}
                title={`このグループのツールの扱い(ツールごとに決めたものはそちらが優先)。「使わない」は全員の会話で使えない${manage ? "" : "(変えられるのは管理者だけ)"}`}
                onChange={p => change(ChatService.SetGroupPolicy(g.group, p))}
              />
              {g.group !== "" && manage && (
                <button type="button" className="tool-reload" disabled={loading[g.group]} onClick={() => loadList(g.group)} title="ツールの一覧を読み直す(サーバーを一時的に起動します)">
                  {loading[g.group] ? <LuLoaderCircle className="spin" /> : <LuRefreshCw />}
                </button>
              )}
            </div>
            {shown && (
              <div className="server-tool-list">
                {g.tools.length === 0 ? (
                  <p className="tool-none">{loading[g.group] ? "(ツールの一覧を読み込み中…)" : "(ツールの一覧はまだありません)"}</p>
                ) : (
                  g.tools.map(t => {
                    const short = t.name.startsWith(g.prefix) ? t.name.slice(g.prefix.length) : t.name;
                    const own = prefs?.tools?.[t.name] ?? "";
                    const now = states[t.name]?.policy;
                    return (
                      <div key={t.name} className={"server-tool" + (now === ToolPolicy.ToolOff ? " unused" : "")} title={`${t.name}\n${t.description}`}>
                        <code>{short}</code>
                        <PolicySelect
                          value={own}
                          unset={groupOff ? "グループのとおり(使わない)" : `グループのとおり${now ? `(${LABELS[now]})` : ""}`}
                          disabled={!manage || groupOff}
                          title={groupOff ? "グループごと使わない" : "このツールの扱い"}
                          onChange={p => change(ChatService.SetToolPolicy(t.name, p))}
                        />
                        <span className="tool-desc">{TOOL_LABELS[t.name] ?? t.description}</span>
                      </div>
                    );
                  })
                )}
              </div>
            )}
          </div>
        );
      })}
      {prefs?.path && <p className="hint tool-path" title="ブラウザの会話のツールの扱いの保存先(aibow-server の PC)">{prefs.path}</p>}
    </div>
  );
}

function PolicySelect({ value, unset, disabled, title, onChange }: {
  value: string;
  unset: string; // 決めていないときの表示
  disabled: boolean;
  title: string;
  onChange(p: ToolPolicy): void;
}) {
  return (
    <select className="tool-policy" value={value} disabled={disabled} title={title} onChange={e => onChange(e.target.value as ToolPolicy)}>
      <option value={ToolPolicy.$zero}>{unset}</option>
      {CHOICES.map(p => <option key={p} value={p}>{LABELS[p]}</option>)}
    </select>
  );
}
