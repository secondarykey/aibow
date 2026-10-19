// 設定の画面の「会話」の「プロファイル」: プロファイル(名前の付いた新しい会話の既定。0014)の一覧と編集。
//
// 一覧で選んだプロファイルの名前・値・決まりを、その下で変える。変えたらすぐ保存する(会話の設定と同じく、まとめて送る)。
//   値:会話の設定と同じ入力欄(ConversationSettingsForm)と、使うツールのグループ。新しい会話はこの写しから始まる。今ある会話には効かない
//   決まり:項目ごとの固定(名前の横の錠)と、使えないツール(グループごと・ツールごと)。今ある会話にもすぐ効く
// 扱いを決めるのは Go。保存できなかったら(名前が空など)理由を出して、Go の値に戻す。
//
// 画面は一旦作ってから使い方を考える(0014 の 12)。ここは最初の形。
import { useCallback, useEffect, useRef, useState } from "react";
import { LuChevronDown, LuChevronRight, LuPlus } from "react-icons/lu";
import { ChatService } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { ConversationSettings, KnownTool, Profile, ProfileInfo } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../../bindings/github.com/secondarykey/aibow/models/index";
import { errorMessage } from "../errors";
import { asSettings, FIX, valuesPatch, type Lock } from "../profileRules";
import { TOOL_LABELS } from "../toolLabels";
import { ConversationSettingsForm, FixMark, LimitsForm } from "./ConversationSettingsForm";
import { BUILTIN } from "./ToolBar";

// 変えてから Go に保存するまで待つ時間(1文字ごとに送らない。useConversations と同じ)
const SAVE_DELAY_MS = 400;

interface Props {
  models: ModelEntry[];
  servers: ServerInfo[]; // settings.json の MCP のサーバー
  disabled: boolean;     // 生成中・読み込み中(上限の「適用」で作業領域を作り直すため)
  onApplyLimits(history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void): void;
  onError(text: string): void;
  onChanged(): void; // プロファイルを変えた(新しい会話の画面の写し・新しい会話の既定を読み直す)
}

export function ProfileSettings({ models, servers, disabled, onApplyLimits, onError, onChanged }: Props) {
  const [infos, setInfos] = useState<ProfileInfo[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [edit, setEditState] = useState<Profile | null>(null); // 選んだプロファイル(画面で変えている途中の値)
  const editRef = useRef<Profile | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const setEdit = (p: Profile | null) => {
    editRef.current = p;
    setEditState(p);
  };

  const reload = useCallback(async (select?: string) => {
    const list = (await ChatService.Profiles()) ?? [];
    setInfos(list);
    const id = select ?? selected ?? list.find(i => i.default)?.profile.id ?? list[0]?.profile.id ?? null;
    setSelected(id);
    setEdit(list.find(i => i.profile.id === id)?.profile ?? null);
  }, [selected]);
  useEffect(() => {
    reload().catch(err => onError(errorMessage(err)));
    // 最初の1回だけ(選んでいるものを変えるたびに読み直さない)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 変えた値は少し待ってからまとめて保存する。保存できなければ理由を出して、Go の値に戻す
  const save = useCallback((p: Profile) => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      ChatService.UpdateProfile(p).then(
        () => {
          onChanged();
          return ChatService.Profiles().then(list => setInfos(list ?? []));
        },
        err => {
          onError(`プロファイルを保存できません: ${errorMessage(err)}`);
          reload(p.id).catch(() => {});
        },
      );
    }, SAVE_DELAY_MS);
  }, [onChanged, onError, reload]);
  const change = (f: (p: Profile) => Profile) => {
    const cur = editRef.current;
    if (!cur) return;
    const next = f(cur);
    setEdit(next);
    save(next);
  };

  // 一覧の操作は、保存していない変更を先に送ってから行う
  const flush = async () => {
    window.clearTimeout(timer.current);
    if (editRef.current) await ChatService.UpdateProfile(editRef.current);
  };
  const act = (f: () => Promise<unknown>, select?: string) => {
    flush()
      .then(f)
      .then(() => {
        onChanged();
        return reload(select);
      })
      .catch(err => onError(errorMessage(err)));
  };

  const info = infos.find(i => i.profile.id === selected);
  const create = () => {
    const base = editRef.current ?? infos.find(i => i.default)?.profile;
    if (!base) return;
    flush()
      .then(() => ChatService.CreateProfile("新しいプロファイル", base.values, {}))
      .then(p => {
        onChanged();
        return reload(p.id);
      })
      .catch(err => onError(errorMessage(err)));
  };

  return (
    <>
      <div className="profile-list" role="listbox">
        {infos.map(i => (
          <button
            key={i.profile.id}
            type="button"
            role="option"
            aria-selected={i.profile.id === selected}
            className={"profile-item" + (i.profile.id === selected ? " current" : "") + (i.profile.archived ? " archived" : "")}
            onClick={() => flush().catch(() => {}).then(() => reload(i.profile.id))}
          >
            <span className="profile-name">{i.profile.name}</span>
            {i.default && <span className="tool-tag" title="新しい会話の画面で最初に選ばれる">最初に選ぶ</span>}
            {i.profile.archived && <span className="tool-tag" title="新しい会話では選べない。決まりは、このプロファイルから作った会話に効く">しまってある</span>}
            <span className="profile-uses" title="このプロファイルから作った会話の数">会話 {i.uses}</span>
          </button>
        ))}
        <button type="button" className="profile-item profile-add" onClick={create} title="選んでいるプロファイルの値を写して、新しいプロファイルを作る">
          <LuPlus /> 新しいプロファイル
        </button>
      </div>

      {edit && info && (
        <ProfileEditor
          key={edit.id}
          profile={edit}
          info={info}
          models={models}
          servers={servers}
          disabled={disabled}
          onChange={change}
          onApplyLimits={onApplyLimits}
          onSetDefault={() => act(() => ChatService.SetDefaultProfile(edit.id), edit.id)}
          onArchive={archived => act(() => ChatService.SetProfileArchived(edit.id, archived), edit.id)}
          onDelete={() => act(() => ChatService.DeleteProfile(edit.id), infos.find(i => i.default)?.profile.id)}
        />
      )}
    </>
  );
}

function ProfileEditor({ profile, info, models, servers, disabled, onChange, onApplyLimits, onSetDefault, onArchive, onDelete }: {
  profile: Profile;
  info: ProfileInfo;
  models: ModelEntry[];
  servers: ServerInfo[];
  disabled: boolean;
  onChange(f: (p: Profile) => Profile): void;
  onApplyLimits(history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void): void;
  onSetDefault(): void;
  onArchive(archived: boolean): void;
  onDelete(): void;
}) {
  const [name, setName] = useState(profile.name);
  useEffect(() => setName(profile.name), [profile.name]);
  const fixed = profile.rules.fixed ?? [];
  const lock: Lock = {
    fixed,
    profile: profile.name,
    onToggle: item => onChange(p => ({
      ...p,
      rules: { ...p.rules, fixed: fixed.includes(item) ? fixed.filter(f => f !== item) : [...fixed, item] },
    })),
  };
  const setValues = (patch: Partial<ConversationSettings>) =>
    onChange(p => ({ ...p, values: { ...p.values, ...valuesPatch(patch) } }));
  const settings = asSettings(profile.values);

  const commitName = () => {
    const t = name.trim();
    if (t === "" || t === profile.name) {
      setName(profile.name);
      return;
    }
    onChange(p => ({ ...p, name: t }));
  };

  return (
    <div className="profile-editor">
      <label htmlFor="profile-name">名前</label>
      <input
        id="profile-name"
        type="text"
        value={name}
        onChange={e => setName(e.target.value)}
        onBlur={commitName}
        onKeyDown={e => {
          if (e.key === "Enter") e.currentTarget.blur();
          if (e.key === "Escape") {
            setName(profile.name);
            e.currentTarget.blur();
          }
        }}
      />
      <div className="profile-actions">
        <button
          type="button"
          disabled={info.default || profile.archived}
          title={info.default ? "新しい会話の画面で最初に選ばれている" : profile.archived ? "しまってあるプロファイルは選べない" : "新しい会話の画面で最初に選ばれるものにする"}
          onClick={onSetDefault}
        >
          最初に選ぶ
        </button>
        <button
          type="button"
          disabled={info.default && !profile.archived}
          title={info.default ? "最初に選ばれるプロファイルはしまえない" : profile.archived ? "新しい会話で選べるように戻す" : "新しい会話では選べなくする(決まりは、このプロファイルから作った会話に効き続ける)"}
          onClick={() => onArchive(!profile.archived)}
        >
          {profile.archived ? "戻す" : "しまう"}
        </button>
        <button
          type="button"
          disabled={info.default || info.uses > 0}
          title={info.default ? "最初に選ばれるプロファイルは消せない" : info.uses > 0 ? `このプロファイルから作った会話が ${info.uses} 個あるので消せない(しまうことはできる)` : "このプロファイルを消す"}
          onClick={onDelete}
        >
          消す
        </button>
      </div>

      <ConversationSettingsForm settings={settings} models={models} lock={lock} onChange={setValues} />
      <LimitsForm
        history={settings.history_tokens}
        reply={settings.reply_tokens}
        disabled={disabled}
        lock={lock}
        onApply={(h, r) => onApplyLimits(h, r, setValues)}
      />

      <h2 className="section" title="新しい会話で最初から使うツールのグループと、このプロファイルの会話で使えないツール">ツール</h2>
      <ProfileTools profile={profile} servers={servers} lock={lock} onChange={onChange} />
    </div>
  );
}

// ProfileTools は、使うツールのグループ(値)と、使えないツール(決まり)。
// グループの「使う」は新しい会話で最初から使うもの。「使えない」はこのプロファイルの会話では使えない(会話で選んでも使えない)。
// ツールの一覧は、組み込みのツールと、MCP のサーバーを最後に起動したときに見えたもの(tools.json の known)。
function ProfileTools({ profile, servers, lock, onChange }: {
  profile: Profile;
  servers: ServerInfo[];
  lock: Lock;
  onChange(f: (p: Profile) => Profile): void;
}) {
  const [builtin, setBuiltin] = useState<KnownTool[]>([]);
  const [known, setKnown] = useState<Record<string, KnownTool[]>>({});
  const [opened, setOpened] = useState<Record<string, boolean>>({});
  useEffect(() => {
    ChatService.BuiltinTools().then(t => setBuiltin(t ?? []), () => {});
    ChatService.ToolPrefs().then(v => setKnown((v.known ?? {}) as Record<string, KnownTool[]>), () => {});
  }, []);

  const v = profile.values;
  const off = profile.rules.off ?? {};
  const offServers = off.servers ?? [];
  const offTools = off.tools ?? [];
  const using = v.mcp_servers ?? [];

  const setUse = (group: string, on: boolean) => onChange(p => {
    if (group === "") return { ...p, values: { ...p.values, builtin_off: !on } };
    const cur = p.values.mcp_servers ?? [];
    return { ...p, values: { ...p.values, mcp_servers: on ? [...cur, group].sort() : cur.filter(n => n !== group) } };
  });
  const setOffGroup = (group: string, isOff: boolean) => onChange(p => {
    const o = p.rules.off ?? {};
    if (group === "") return { ...p, rules: { ...p.rules, off: { ...o, builtin: isOff } } };
    const cur = o.servers ?? [];
    return { ...p, rules: { ...p.rules, off: { ...o, servers: isOff ? [...cur, group].sort() : cur.filter(n => n !== group) } } };
  });
  const setOffTool = (name: string, isOff: boolean) => onChange(p => {
    const o = p.rules.off ?? {};
    const cur = o.tools ?? [];
    return { ...p, rules: { ...p.rules, off: { ...o, tools: isOff ? [...cur, name].sort() : cur.filter(n => n !== name) } } };
  });

  const groups = [
    { group: "", name: BUILTIN, on: !v.builtin_off, isOff: !!off.builtin, tools: builtin, prefix: "" },
    ...servers.map(s => ({
      group: s.name,
      name: s.name,
      on: using.includes(s.name),
      isOff: offServers.includes(s.name),
      tools: known[s.name] ?? [],
      prefix: s.name.replace(/[^A-Za-z0-9_-]/gu, "_") + "__",
    })),
  ];

  return (
    <div className="tool-settings">
      <div className="field-label">
        <span className="profile-sub">使うツールのグループ</span>
        <FixMark item={FIX.toolGroups} lock={lock} />
      </div>
      {groups.map(g => (
        <div key={g.group} className={"tool-group" + (g.on && !g.isOff ? " on" : "")}>
          <div className="tool-group-head">
            <button type="button" className="tool-open" onClick={() => setOpened(o => ({ ...o, [g.group]: !o[g.group] }))} title={opened[g.group] ? "畳む" : "ツールの一覧を開く"}>
              {opened[g.group] ? <LuChevronDown /> : <LuChevronRight />}
            </button>
            <label className="check" title="新しい会話で最初から使う">
              <input type="checkbox" checked={g.on} disabled={g.isOff} onChange={e => setUse(g.group, e.target.checked)} />
              <span className="tool-group-name">{g.name}</span>
            </label>
            <label className="check profile-off" title="このプロファイルの会話では使えない(会話で選んでも使えない)">
              <input type="checkbox" checked={g.isOff} onChange={e => setOffGroup(g.group, e.target.checked)} />
              使えない
            </label>
          </div>
          {opened[g.group] && (
            <div className="tool-group-body">
              {g.tools.length === 0 ? (
                <p className="tool-none">(ツールの一覧はまだありません。設定の画面の「ツール」で読み込めます)</p>
              ) : (
                g.tools.map(t => {
                  const short = t.name.startsWith(g.prefix) ? t.name.slice(g.prefix.length) : t.name;
                  const isOff = g.isOff || offTools.includes(t.name);
                  return (
                    <label key={t.name} className={"check tool-item" + (isOff ? " unused" : "")} title={`${t.name}\n${t.description}`}>
                      <input
                        type="checkbox"
                        checked={isOff}
                        disabled={g.isOff} // グループごと使えないなら、ツールごとに選んでも同じ
                        onChange={e => setOffTool(t.name, e.target.checked)}
                      />
                      <code>{short}</code>
                      <span />
                      <span className="tool-tag">{isOff ? "使えない" : "使える"}</span>
                      <span className="tool-desc">{TOOL_LABELS[t.name] ?? t.description}</span>
                    </label>
                  );
                })
              )}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
