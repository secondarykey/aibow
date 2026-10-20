// プロファイルの「ツール」: このプロファイルでのツールの扱いを、1つの一覧で決める(0014。2026-10-20、ユーザーと決めた形)。
//
// グループ(組み込み / MCP のサーバー)の見出しと、ツールの行は同じ形(2026-10-20、ユーザーの指示でそろえた。前はグループだけ3択の選択):
//   チェック       … 新しい会話で最初から使う(値。外すと「選べば使える」= 会話で選べば使う)。
//                    グループは新しい会話で最初から選ぶか、ツールは会話の上書き off として写すか
//   確認しない     … この PC の既定(tools.json)。どのプロファイルでも同じ(枠の吹き出しにそう書く)
//   錠             … 使えない(決まり。会話で変えさせない)。錠をかけるとチェックも外れる。グループに錠をかけると、中のツールも押せない
// チェックの向きは、グループもツールも「使う」にそろえる(チェック = 使う)。
//
// MCP のサーバーのツールの一覧は、サーバーを最後に起動したときに Go が tools.json に覚えたもの。
// 開いたグループで一覧がまだ無ければ、Go がサーバーを一時的に起動して受け取る(ChatService.LoadToolList)。読み直すボタンでも受け取り直せる。
import { useCallback, useEffect, useRef, useState, type MouseEvent } from "react";
import { LuChevronDown, LuChevronRight, LuLoaderCircle, LuLock, LuLockOpen, LuRefreshCw } from "react-icons/lu";
import { ChatService } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import { ToolPolicy } from "../../bindings/github.com/secondarykey/aibow";
import type { KnownTool, Profile, ToolPrefsView, ToolState } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { errorMessage } from "../errors";
import { FIX, type Lock } from "../profileRules";
import { TOOL_LABELS } from "../toolLabels";
import { ConfirmChip } from "./ConfirmChip";
import { FixMark } from "./ConversationSettingsForm";
import { BUILTIN } from "./ToolBar";

// 「確認しない」はこの PC の値なので、どのプロファイルでも同じことを吹き出しに添える
const PC_NOTE = "(この PC の設定。どのプロファイルでも同じ)";

export function ProfileTools({ profile, servers, lock, onChange, onPCChanged }: {
  profile: Profile;
  servers: ServerInfo[];
  lock: Lock; // 使うツールのグループの固定(錠のボタン)
  onChange(f: (p: Profile) => Profile): void;
  onPCChanged(): void; // この PC の値(確認しない)を変えた(メーターなどを読み直す)
}) {
  const [builtin, setBuiltin] = useState<KnownTool[]>([]);
  const [prefs, setPrefs] = useState<ToolPrefsView | null>(null);
  const [states, setStates] = useState<Record<string, ToolState>>({}); // ツールの名前 → この PC での扱い(確認するか)
  const [error, setError] = useState("");
  const [opened, setOpened] = useState<Record<string, boolean>>({});
  const [loading, setLoading] = useState<Record<string, boolean>>({});
  const [listErrors, setListErrors] = useState<Record<string, string>>({});
  const tried = useRef(new Set<string>()); // 一覧が無いので自動で受け取りにいったサーバー(失敗しても繰り返さない)

  const reloadPC = useCallback(() => {
    ChatService.ToolPrefs().then(setPrefs, err => setError(errorMessage(err)));
    ChatService.ToolDefaults().then(
      groups => setStates(Object.fromEntries((groups ?? []).flatMap(g => g.tools ?? []).map(t => [t.name, t]))),
      err => setError(errorMessage(err)),
    );
  }, []);
  useEffect(() => {
    reloadPC();
    ChatService.BuiltinTools().then(t => setBuiltin(t ?? []), err => setError(errorMessage(err)));
  }, [reloadPC]);

  const loadList = useCallback((name: string) => {
    setLoading(l => ({ ...l, [name]: true }));
    setListErrors(e => ({ ...e, [name]: "" }));
    ChatService.LoadToolList(name)
      .then(reloadPC, err => setListErrors(e => ({ ...e, [name]: errorMessage(err) })))
      .finally(() => setLoading(l => ({ ...l, [name]: false })));
  }, [reloadPC]);
  useEffect(() => {
    if (!prefs) return;
    for (const s of servers) {
      if (opened[s.name] && (prefs.known?.[s.name]?.length ?? 0) === 0 && !tried.current.has(s.name)) {
        tried.current.add(s.name);
        loadList(s.name);
      }
    }
  }, [prefs, servers, loadList, opened]);

  // この PC の値(確認しない)を変える
  const pc = (p: Promise<unknown>) => {
    setError("");
    p.catch(err => setError(errorMessage(err))).finally(() => {
      reloadPC();
      onPCChanged();
    });
  };

  const v = profile.values;
  const off = profile.rules.off ?? {};
  const toolsOff = v.tools_off ?? [];
  const offTools = off.tools ?? [];

  const groupLocked = (group: string) => !!(group === "" ? off.builtin : (off.servers ?? []).includes(group));
  const groupUsed = (group: string) => group === "" ? !v.builtin_off : (v.mcp_servers ?? []).includes(group);
  // グループのチェック: 新しい会話で最初から使うか(外すと「選べば使える」)
  const setGroupUse = (group: string, on: boolean) => onChange(p => {
    if (group === "") return { ...p, values: { ...p.values, builtin_off: !on } };
    const cur = (p.values.mcp_servers ?? []).filter(n => n !== group);
    return { ...p, values: { ...p.values, mcp_servers: on ? [...cur, group].sort() : cur } };
  });
  // グループの錠: 使えない。かけるとチェックも外す。外しても「選べば使える」のまま(ツールの錠と同じ)
  const setGroupLocked = (group: string, locked: boolean) => onChange(p => {
    const o = p.rules.off ?? {};
    const vals = { ...p.values };
    const nextOff = { ...o };
    if (group === "") {
      vals.builtin_off = true;
      nextOff.builtin = locked;
    } else {
      vals.mcp_servers = (vals.mcp_servers ?? []).filter(n => n !== group);
      const offs = (o.servers ?? []).filter(n => n !== group);
      nextOff.servers = locked ? [...offs, group].sort() : offs;
    }
    return { ...p, values: vals, rules: { ...p.rules, off: nextOff } };
  });
  // ツールのチェック: 新しい会話で最初から使うか(外すと「選べば使える」)
  const setDefaultUse = (name: string, on: boolean) => onChange(p => {
    const cur = (p.values.tools_off ?? []).filter(n => n !== name);
    return { ...p, values: { ...p.values, tools_off: on ? cur : [...cur, name].sort() } };
  });
  // ツールの錠: 使えない。かけるとチェックも外す(使えないツールを最初から使う、にはしない)。外しても「選べば使える」のまま
  const setLocked = (name: string, locked: boolean) => onChange(p => {
    const o = p.rules.off ?? {};
    const tools = (o.tools ?? []).filter(n => n !== name);
    const offList = (p.values.tools_off ?? []).filter(n => n !== name);
    return {
      ...p,
      values: { ...p.values, tools_off: [...offList, name].sort() },
      rules: { ...p.rules, off: { ...o, tools: locked ? [...tools, name].sort() : tools } },
    };
  });

  const groups = [
    { group: "", name: BUILTIN, title: "aibow の中で動くツール(時刻・計算・ファイル・コマンド)", tools: builtin, prefix: "", pcPolicy: prefs?.builtin },
    ...servers.map(s => ({
      group: s.name,
      name: s.name,
      title: s.http ? "URL でつなぐ MCP のサーバー" : "aibow が起動する MCP のサーバー",
      tools: prefs?.known?.[s.name] ?? [],
      // モデルに見せる名前は「サーバーの名前__ツールの名前」。サーバーの名前の部分は見出しにあるので外す
      prefix: s.name.replace(/[^A-Za-z0-9_-]/gu, "_") + "__",
      pcPolicy: prefs?.servers?.[s.name],
    })),
  ];

  return (
    <div className="tool-settings">
      {(error || prefs?.error) && <div className="msg error">{error || prefs?.error}</div>}
      <div className="field-label">
        <span className="profile-sub">グループ</span>
        <FixMark item={FIX.toolGroups} lock={lock} />
      </div>
      {groups.map(g => {
        const groupOff = groupLocked(g.group);
        const used = !groupOff && groupUsed(g.group);
        const shown = !!opened[g.group];
        const isServer = g.group !== "";
        const usable = g.tools.filter(t => !groupOff && !offTools.includes(t.name)).length;
        const groupAllow = g.pcPolicy === ToolPolicy.ToolAllow;
        return (
          <div key={g.group} className={"tool-group" + (used ? " on" : "")}>
            <div className="tool-group-head">
              <button type="button" className="tool-open" onClick={() => setOpened(o => ({ ...o, [g.group]: !o[g.group] }))} title={shown ? "畳む" : "ツールの一覧を開く"}>
                {shown ? <LuChevronDown /> : <LuChevronRight />}
              </button>
              <label className="check" title={groupOff ? "使えない(錠を外すと選べる)" : `${g.title}。チェックすると、新しい会話で最初から使う(外すと、会話で選べば使える)`}>
                <input type="checkbox" checked={used} disabled={groupOff} onChange={e => setGroupUse(g.group, e.target.checked)} />
                <span className="tool-group-name">{g.name}</span>
              </label>
              {!groupOff && (
                <ConfirmChip
                  allow={groupAllow}
                  title={(groupAllow
                    ? "このグループのツールはまとめて確認しない(押すと戻す。ツールごとに決めたものはそちらが優先)"
                    : "このグループのツールをまとめて確認しないにする(ツールごとに決めたものはそちらが優先)") + PC_NOTE}
                  onToggle={a => pc(ChatService.SetGroupPolicy(g.group, a ? ToolPolicy.ToolAllow : ToolPolicy.$zero))}
                />
              )}
              <LockChip
                on={groupOff}
                disabled={false}
                title={groupOff ? "使えない(このプロファイルの会話では、このグループのツールを使えない)。押すと外す" : "使えないにする(このプロファイルの会話では、このグループのツールを使えなくする)"}
                onToggle={l => setGroupLocked(g.group, l)}
              />
              {g.tools.length > 0 && <span className="tool-count">{usable} / {g.tools.length}</span>}
              {isServer && shown && (
                <button type="button" className="tool-reload" disabled={loading[g.group]} onClick={() => loadList(g.group)} title="ツールの一覧を読み直す(サーバーを一時的に起動します)">
                  {loading[g.group] ? <LuLoaderCircle className="spin" /> : <LuRefreshCw />}
                </button>
              )}
            </div>
            {shown && (
              <div className="tool-group-body">
                {listErrors[g.group] && <div className="msg error">{listErrors[g.group]}</div>}
                {g.tools.length === 0 ? (
                  <p className="tool-none">{loading[g.group] ? "(ツールの一覧を読み込み中…)" : "(ツールの一覧はまだありません)"}</p>
                ) : (
                  g.tools.map(t => {
                    const short = t.name.startsWith(g.prefix) ? t.name.slice(g.prefix.length) : t.name;
                    const locked = groupOff || offTools.includes(t.name);
                    const checked = !locked && !toolsOff.includes(t.name);
                    const allow = states[t.name]?.default === ToolPolicy.ToolAllow;
                    return (
                      <label key={t.name} className={"check tool-item" + (locked ? " unused" : "")} title={`${t.name}\n${t.description}`}>
                        <input
                          type="checkbox"
                          checked={checked}
                          disabled={locked}
                          onChange={e => setDefaultUse(t.name, e.target.checked)}
                        />
                        <code>{short}</code>
                        {locked ? <span /> : (
                          <ConfirmChip
                            allow={allow}
                            title={(allow ? "このツールは確認せずに実行する(押すと確認する)" : "このツールを確認せずに実行するようにする") + PC_NOTE}
                            onToggle={a => pc(ChatService.SetToolPolicy(t.name, a ? ToolPolicy.ToolAllow : ToolPolicy.ToolAsk))}
                          />
                        )}
                        <LockChip
                          on={locked}
                          disabled={groupOff}
                          title={groupOff ? "グループごと使えない" : locked ? "使えない(このプロファイルの会話では使えない)。押すと外す" : "使えないにする(このプロファイルの会話では使えなくする)"}
                          onToggle={l => setLocked(t.name, l)}
                        />
                        <span className="tool-desc">{TOOL_LABELS[t.name] ?? t.description}</span>
                      </label>
                    );
                  })
                )}
              </div>
            )}
          </div>
        );
      })}
      {prefs?.path && <p className="hint tool-path" title="この PC での、確認の扱いの保存先">{prefs.path}</p>}
    </div>
  );
}

// LockChip は、ツールの「使えない」(錠)を切り替える小さな枠。かけているときは出したまま、そうでないときは行にマウスを乗せたときだけ出す。
// ツールの行は label なので、押したときに行のチェックまで切り替わらないよう、既定の動きを止める(ConfirmChip と同じ)。
function LockChip({ on, disabled, title, onToggle }: { on: boolean; disabled: boolean; title: string; onToggle(on: boolean): void }) {
  const click = (e: MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (!disabled) onToggle(!on);
  };
  return (
    <button type="button" className={"tool-lock" + (on ? " on" : "")} disabled={disabled} title={title} onClick={click}>
      {on ? <LuLock /> : <LuLockOpen />}{on ? " 使えない" : ""}
    </button>
  );
}
