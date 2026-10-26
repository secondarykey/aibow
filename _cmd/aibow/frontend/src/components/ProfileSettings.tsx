// 設定の画面の「会話」の「プロファイル」: プロファイル(名前の付いた新しい会話の既定。0014)の一覧と編集。
//
// 一覧で選んだプロファイルの名前・値・決まりを、その下で変える。変えたらすぐ保存する(会話の設定と同じく、まとめて送る)。
//   値:会話の設定と同じ入力欄(ConversationSettingsForm)と、使うツールのグループ。新しい会話はこの写しから始まる。今ある会話には効かない
//   決まり:項目ごとの固定(名前の横の錠)と、使えないツール(グループごと・ツールごと)。今ある会話にもすぐ効く
// 扱いを決めるのは Go。保存できなかったら(名前が空など)理由を出して、Go の値に戻す。
//
// 画面は一旦作ってから使い方を考える(0014 の 12)。ここは最初の形。
//
// 配る(0018 の 11-1・決定事項 22):
//   - 管理者(ブラウザ)は、編集の「配る」で、配らない / 全員 / 指定した人(名前のチェック)を選ぶ。一覧に「全員に配っている」「N 人に配っている」の印
//   - 受け取った人には「配られたプロファイル」の欄を分けて出す。値は読むだけ(fieldset ごと無効にする)で、「写す」で自分のプロファイルにする
//   - 配られたものも「最初に選ぶ」にできる(配られなくなったら、Go が自分のプロファイルに戻す)
import { useCallback, useEffect, useRef, useState } from "react";
import { LuCopy, LuPlus } from "react-icons/lu";
import { admin, ChatService, isBrowser, me } from "../backend";
import type { ConversationSettings, Profile, ProfileInfo } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../../bindings/github.com/secondarykey/aibow/models/index";
import { errorMessage } from "../errors";
import { asSettings, valuesPatch, type Lock } from "../profileRules";
import { ConversationSettingsForm, LimitsForm } from "./ConversationSettingsForm";
import { ProfileTools } from "./ProfileTools";

// 変えてから Go に保存するまで待つ時間(1文字ごとに送らない。useConversations と同じ)
const SAVE_DELAY_MS = 400;

interface Props {
  models: ModelEntry[];
  servers: ServerInfo[]; // settings.json の MCP のサーバー
  disabled: boolean;     // 生成中・読み込み中(上限の「適用」で作業領域を作り直すため)
  onApplyLimits(history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void): void;
  onError(text: string): void;
  onChanged(): void; // プロファイルを変えた(新しい会話の画面の写し・新しい会話の既定を読み直す)
  onToolsChanged(): void; // この PC のツールの扱い(確認しない)を変えた(メーターを読み直す)
}

export function ProfileSettings({ models, servers, disabled, onApplyLimits, onError, onChanged, onToolsChanged }: Props) {
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
      .then(() => ChatService.CreateProfile(freeName(infos), base.values, {}))
      .then(p => {
        onChanged();
        return reload(p.id);
      })
      .catch(err => onError(errorMessage(err)));
  };

  // 自分が変えられるもの(自分のもの・管理者なら配ったもの)と、配られたもの(読むだけ)に分ける
  const mine = infos.filter(i => !i.profile.read_only);
  const received = infos.filter(i => i.profile.read_only);
  const item = (i: ProfileInfo) => (
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
      {!i.profile.read_only && i.profile.share && <ShareTag profile={i.profile} />}
      {i.profile.owner_name && i.profile.owner_name !== me.name && (
        <span className="profile-owner" title="配った人">{i.profile.owner_name}</span>
      )}
      <span className="profile-uses" title="このプロファイルから作った会話の数">会話 {i.uses}</span>
    </button>
  );

  // 編集は、選んでいるプロファイルのある一覧(自分のもの / 配られたもの)のすぐ下に出す
  const editor = edit && info && (
    <ProfileEditor
      key={edit.id}
      profile={edit}
      info={info}
      models={models}
      servers={servers}
      disabled={disabled}
      onChange={change}
      onApplyLimits={onApplyLimits}
      onToolsChanged={onToolsChanged}
      onSetDefault={() => act(() => ChatService.SetDefaultProfile(edit.id), edit.id)}
      onArchive={archived => act(() => ChatService.SetProfileArchived(edit.id, archived), edit.id)}
      onDelete={() => act(() => ChatService.DeleteProfile(edit.id), infos.find(i => i.default)?.profile.id)}
      onCopy={() => {
        ChatService.CopyProfile(edit.id)
          .then(p => {
            onChanged();
            return reload(p.id);
          })
          .catch(err => onError(errorMessage(err)));
      }}
      onShare={(share, users) => act(() => ChatService.SetProfileShare(edit.id, share, users), edit.id)}
      onError={onError}
    />
  );

  return (
    <>
      <div className="profile-list" role="listbox">
        {mine.map(item)}
        <button type="button" className="profile-item profile-add" onClick={create} title="選んでいるプロファイルの値を写して、新しいプロファイルを作る">
          <LuPlus /> 新しいプロファイル
        </button>
      </div>
      {!edit?.read_only && editor}
      {received.length > 0 && (
        <>
          <h2 className="section" title="管理者が配ったプロファイル。新しい会話で選べるが、値と決まりは変えられない(変えたいときは写す)">配られたプロファイル</h2>
          <div className="profile-list" role="listbox">{received.map(item)}</div>
        </>
      )}
      {edit?.read_only && editor}

    </>
  );
}

function ProfileEditor({ profile, info, models, servers, disabled, onChange, onApplyLimits, onToolsChanged, onSetDefault, onArchive, onDelete, onCopy, onShare, onError }: {
  profile: Profile;
  info: ProfileInfo;
  models: ModelEntry[];
  servers: ServerInfo[];
  disabled: boolean;
  onChange(f: (p: Profile) => Profile): void;
  onApplyLimits(history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void): void;
  onToolsChanged(): void;
  onSetDefault(): void;
  onArchive(archived: boolean): void;
  onDelete(): void;
  onCopy(): void;                                  // 写して自分のプロファイルにする(配られたもの)
  onShare(share: string, users: string[]): void;   // 配る範囲と相手を変える(管理者)
  onError(text: string): void;
}) {
  if (profile.read_only) return <ReceivedProfile profile={profile} info={info} models={models} onSetDefault={onSetDefault} onCopy={onCopy} />;
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
      {isBrowser && me.admin && <ShareForm profile={profile} onShare={onShare} onError={onError} />}

      <ConversationSettingsForm settings={settings} models={models} lock={lock} onChange={setValues} />
      <LimitsForm
        history={settings.history_tokens}
        reply={settings.reply_tokens}
        disabled={disabled}
        lock={lock}
        onApply={(h, r) => onApplyLimits(h, r, setValues)}
      />

      {/* ブラウザでは出さない。ブラウザの会話のツールの扱いの既定は管理者が決めるので、プロファイルのツールと一緒に管理者の「ツール」の画面で扱う(0024 の 12-1b) */}
      {!isBrowser && (
        <>
          <h2 className="section" title="このプロファイルでのツールの扱い(最初から使う・選べば使える・使えない)と、確認しない(この PC の設定)">ツール</h2>
          <ProfileTools profile={profile} servers={servers} lock={lock} onChange={onChange} onPCChanged={onToolsChanged} />
        </>
      )}
    </div>
  );
}

// ShareTag は、配っているプロファイルの印(一覧)。
function ShareTag({ profile }: { profile: Profile }) {
  const text = profile.share === "all" ? "全員に配っている" : `${profile.share_with?.length ?? 0} 人に配っている`;
  const title = profile.share === "all" ? "全員の新しい会話で選べる" : `配っている相手: ${(profile.share_with ?? []).join("・")}`;
  return <span className="tool-tag share-tag" title={title}>{text}</span>;
}

// ShareForm は、管理者がプロファイルを配る範囲と相手を選ぶ欄(0018 の決定事項 22)。選んだらすぐ変える。
function ShareForm({ profile, onShare, onError }: { profile: Profile; onShare(share: string, users: string[]): void; onError(text: string): void }) {
  const [names, setNames] = useState<string[]>([]);
  useEffect(() => {
    admin.users()
      .then(list => setNames(list.filter(u => u.name !== me.name && !u.local).map(u => u.name)))
      .catch(err => onError(errorMessage(err)));
  }, [onError]);
  const share = profile.share ?? "";
  const chosen = profile.share_with ?? [];
  const toggle = (name: string) => {
    const next = chosen.includes(name) ? chosen.filter(n => n !== name) : [...chosen, name];
    if (next.length === 0) onShare("", []);
    else onShare("users", next);
  };
  return (
    <div className="profile-share">
      <label htmlFor="profile-share" title="配ったプロファイルは、受け取った人の新しい会話で選べる。値と決まりは受け取った人には変えられず、ほかの管理者は変えられる">配る</label>
      <select
        id="profile-share"
        value={share}
        onChange={e => {
          const v = e.target.value;
          if (v === "users") onShare("users", chosen.length ? chosen : names.slice(0, 1));
          else onShare(v, []);
        }}
      >
        <option value="">配らない</option>
        <option value="all">全員</option>
        <option value="users" disabled={names.length === 0}>指定した人</option>
      </select>
      {share === "users" && (
        <div className="share-users">
          {names.map(n => (
            <label key={n} className="check">
              <input type="checkbox" checked={chosen.includes(n)} onChange={() => toggle(n)} />
              {n}
            </label>
          ))}
        </div>
      )}
    </div>
  );
}

// ReceivedProfile は、配られたプロファイル(読むだけ)。値と決まりは見せるが変えられない(fieldset ごと無効)。写せば自分のものになる
function ReceivedProfile({ profile, info, models, onSetDefault, onCopy }: {
  profile: Profile;
  info: ProfileInfo;
  models: ModelEntry[];
  onSetDefault(): void;
  onCopy(): void;
}) {
  const settings = asSettings(profile.values);
  const lock: Lock = { fixed: profile.rules.fixed ?? [], profile: profile.name, onToggle: () => {} };
  return (
    <div className="profile-editor">
      <div className="profile-received">
        <span className="profile-received-name">{profile.name}</span>
        {profile.owner_name && <span className="profile-owner" title="配った人">{profile.owner_name}</span>}
      </div>
      <div className="profile-actions">
        <button
          type="button"
          disabled={info.default}
          title={info.default ? "新しい会話の画面で最初に選ばれている" : "新しい会話の画面で最初に選ばれるものにする(配られなくなったら、自分のプロファイルに戻る)"}
          onClick={onSetDefault}
        >
          最初に選ぶ
        </button>
        <button type="button" onClick={onCopy} title="値と決まりを写して、自分のプロファイルを作る(写したものは変えられる)">
          <LuCopy /> 写す
        </button>
      </div>
      <fieldset disabled className="profile-readonly">
        <ConversationSettingsForm settings={settings} models={models} lock={lock} onChange={() => {}} />
        <LimitsForm history={settings.history_tokens} reply={settings.reply_tokens} disabled lock={lock} onApply={() => {}} />
      </fieldset>
    </div>
  );
}

// freeName は、新しいプロファイルの名前。ほかのプロファイルと重ならないよう、重なれば番号を付ける(Go は重なる名前を断る)
function freeName(infos: ProfileInfo[]): string {
  const base = "新しいプロファイル";
  const used = new Set(infos.filter(i => !i.profile.share).map(i => i.profile.name)); // 名前は自分のもの同士で重ならなければよい
  if (!used.has(base)) return base;
  for (let n = 2; ; n++) {
    if (!used.has(`${base} ${n}`)) return `${base} ${n}`;
  }
}
