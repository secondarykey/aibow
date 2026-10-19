// 会話の設定の画面の「ツール」: この会話で使うツール。
//
// グループ(組み込み / MCP のサーバー)のチェックは、この会話で使うか(入力欄の上の「この会話のツール」と同じ設定。builtin_off / mcp_servers)。
// ツールごとのチェックは、この会話で使うか(会話の上書き tool_policies の on / off)。
// PC の既定(設定の画面の「ツール」= tools.json)の「使わない」は「既定では使わない」の意味で、禁止ではない。
// なので、既定で使わないツールも、この会話では使うにできる。既定と同じにしたら上書きを消す(既定を変えたときに付いていくように)。
// 「確認しない」の枠で、この会話だけ確認せずに実行するにできる(会話の上書きの allow / ask。0010)。既定と同じにしたら上書きを消す。
// 扱いは Go の toolPolicy と同じ決め方で Go に決めてもらう(ChatService.ToolStates)。画面で同じ判断を書き直さない。
// 既定と違うツールと、既定で使わないツールには、どこで決まったか(この会話 / 既定)を添える。
//
// グループは、使っていてもいなくても畳んで出し、▶ で開ける(設定の画面の「ツール」と同じ)。
import { useEffect, useState } from "react";
import { LuChevronDown, LuChevronRight } from "react-icons/lu";
import { ChatService } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import { ToolPolicy } from "../../bindings/github.com/secondarykey/aibow";
import type { ConversationSettings, ToolGroupState, ToolState } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { errorMessage } from "../errors";
import { TOOL_LABELS } from "../toolLabels";
import { BUILTIN } from "./ToolBar";
import { ConfirmChip } from "./ConfirmChip";

interface Props {
  id: string; // 会話の ID(プロファイルの決まりを Go が引くため)
  settings: ConversationSettings;
  servers: ServerInfo[]; // settings.json の MCP のサーバー
  onChange(patch: Partial<ConversationSettings>): void;
}

export function ConversationTools({ id, settings, servers, onChange }: Props) {
  const [groups, setGroups] = useState<ToolGroupState[]>([]);
  const [error, setError] = useState("");
  const [opened, setOpened] = useState<Record<string, boolean>>({}); // ▶ で開いたグループ(最初は全部畳む)

  // ツールの扱いが変わるのは、会話の上書き(tool_policies)が変わったときだけ(PC の既定は設定の画面で変える)
  const policies = JSON.stringify(settings.tool_policies ?? {});
  useEffect(() => {
    ChatService.ToolStates(id, settings).then(
      g => {
        setGroups(g ?? []);
        setError("");
      },
      err => setError(errorMessage(err)),
    );
    // settings の中で扱いに効くのは tool_policies だけなので、それが変わったときに読み直す
  }, [id, policies]);

  // ツールを使う / 使わない。既定(会話の上書きが無いときの扱い)と同じなら上書きを消し、違えば on / off で上書きする
  const toggleTool = (t: ToolState, on: boolean) => {
    const next = { ...(settings.tool_policies ?? {}) };
    if (on === (t.default !== ToolPolicy.ToolOff)) delete next[t.name];
    else next[t.name] = on ? ToolPolicy.ToolOn : ToolPolicy.ToolOff;
    onChange({ tool_policies: next });
  };

  // 確認する / 確認しない(使うツールだけ)。既定(上書きが無いとき)と同じなら上書きを消し、違えば ask / allow で上書きする
  const toggleConfirm = (t: ToolState, allow: boolean) => {
    const next = { ...(settings.tool_policies ?? {}) };
    const want = allow ? ToolPolicy.ToolAllow : ToolPolicy.ToolAsk;
    if (want === t.default) delete next[t.name];
    else next[t.name] = want;
    onChange({ tool_policies: next });
  };

  const selected = settings.mcp_servers ?? [];
  const toggleServer = (name: string, on: boolean) =>
    onChange({ mcp_servers: on ? [...selected, name] : selected.filter(n => n !== name) });

  return (
    <div className="tool-settings">
      {error && <div className="msg error">{error}</div>}
      {groups.map(g => {
        if (g.server === "") {
          return (
            <Group
              key=""
              name={BUILTIN}
              title="aibow の中で動くツール"
              on={!settings.builtin_off}
              onToggle={on => onChange({ builtin_off: !on })}
              open={opened[""]}
              onToggleOpen={() => setOpened(o => ({ ...o, "": !o[""] }))}
              tools={g.tools ?? []}
              workDir={settings.work_dir}
              label={t => TOOL_LABELS[t.name] ?? t.description}
              onToggleTool={toggleTool}
              onToggleConfirm={toggleConfirm}
            />
          );
        }
        // settings.json から消したサーバーは出さない(Go は settings.json のサーバーだけを返すので、ふつうは見つかる)
        const info = servers.find(s => s.name === g.server);
        if (!info) return null;
        return (
          <Group
            key={g.server}
            name={g.server}
            title={info.http ? "URL でつなぐ MCP のサーバー" : "aibow が起動する MCP のサーバー"}
            on={selected.includes(g.server)}
            onToggle={on => toggleServer(g.server, on)}
            open={opened[g.server]}
            onToggleOpen={() => setOpened(o => ({ ...o, [g.server]: !o[g.server] }))}
            tools={g.tools ?? []}
            workDir={settings.work_dir}
            needsWorkDir={info.uses_work_dir}
            // モデルに見せる名前は「サーバーの名前__ツールの名前」。サーバーの名前の部分は見出しにあるので外す
            prefix={g.server.replace(/[^A-Za-z0-9_-]/gu, "_") + "__"}
            label={t => t.description}
            onToggleTool={toggleTool}
            onToggleConfirm={toggleConfirm}
          />
        );
      })}
    </div>
  );
}

function Group({ name, title, on, onToggle, open, onToggleOpen, tools, workDir, needsWorkDir, prefix = "", label, onToggleTool, onToggleConfirm }: {
  name: string;
  title: string;
  on: boolean;            // この会話で使う
  onToggle(on: boolean): void;
  open?: boolean;
  onToggleOpen(): void;
  tools: ToolState[];
  workDir: string;
  needsWorkDir?: boolean; // ${workdir} を使うサーバー(作業フォルダを決めた会話でしか起動しない)
  prefix?: string;
  label(t: ToolState): string;
  onToggleTool(t: ToolState, on: boolean): void;
  onToggleConfirm(t: ToolState, allow: boolean): void;
}) {
  const usable = (t: ToolState) => t.policy !== ToolPolicy.ToolOff && !(t.needs_work_dir && !workDir);
  const using = tools.filter(usable).length;
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
        {needsWorkDir && !workDir && (
          <span className="tool-tag" title="このサーバーは作業フォルダで起動します">作業フォルダが要る</span>
        )}
      </div>
      {shown && (
        <div className="tool-group-body">
          {tools.length === 0 ? (
            <p className="tool-none">(ツールの一覧はまだありません)</p>
          ) : (
            tools.map(t => {
              const short = t.name.startsWith(prefix) ? t.name.slice(prefix.length) : t.name;
              return (
                <label key={t.name} className={"check tool-item" + (on ? "" : " unused")} title={`${t.name}\n${t.description}`}>
                  <input
                    type="checkbox"
                    checked={t.policy !== ToolPolicy.ToolOff}
                    disabled={!on} // この会話で使っていないグループのツールは、選んでも使われないので押せなくする
                    onChange={e => onToggleTool(t, e.target.checked)}
                  />
                  <code>{short}</code>
                  {t.policy !== ToolPolicy.ToolOff && on && (
                    <ConfirmChip
                      allow={t.policy === ToolPolicy.ToolAllow}
                      title={t.policy === ToolPolicy.ToolAllow ? "この会話では確認せずに実行する(押すと確認する)" : "この会話では確認せずに実行するようにする"}
                      onToggle={a => onToggleConfirm(t, a)}
                    />
                  )}
                  <StateTag tool={t} workDir={workDir} />
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

// どこで決まったか・使えない理由。既定どおりに使うツールには何も付けない
// (確認する / 確認しないの表示は、確認を画面で扱うときに足す)
function StateTag({ tool, workDir }: { tool: ToolState; workDir: string }) {
  const off = tool.policy === ToolPolicy.ToolOff;
  if (tool.from === "profile") {
    return <span className="tool-tag" title="この会話のプロファイルで使えないにしてある(この会話でも使えない)">プロファイルで使えない</span>;
  }
  if (tool.from === "conversation") {
    if (off) return <span className="tool-tag" title="この会話だけ使わない(既定では使う)">この会話で使わない</span>;
    if (tool.default === ToolPolicy.ToolOff) return <span className="tool-tag" title="この会話だけ使う(既定では使わない)">この会話で使う</span>;
    // 確認の扱いだけを、この会話で変えている
    return <span className="tool-tag" title="確認するかを、この会話だけ変えています">この会話</span>;
  }
  if (off) {
    return <span className="tool-tag" title="設定の画面の「ツール」(tools.json)で、既定では使わないにしてある">既定で使わない</span>;
  }
  if (tool.needs_work_dir && !workDir) {
    return <span className="tool-tag" title="作業フォルダを決めた会話でだけ使えます">作業フォルダが要る</span>;
  }
  return null;
}
