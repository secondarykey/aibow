// 設定の画面の「会話」の「プロファイル」: プロファイル(名前の付いた新しい会話の既定。0014)の一覧と編集。
//
// 一覧で選んだプロファイルの名前・値・決まりを、その下で変える。変えたらすぐ保存する(会話の設定と同じく、まとめて送る)。
//   値:会話の設定と同じ入力欄(ConversationSettingsForm)と、使うツールのグループ。新しい会話はこの写しから始まる。今ある会話には効かない
//   決まり:項目ごとの固定(名前の横の錠)と、使えないツール(グループごと・ツールごと)。今ある会話にもすぐ効く
// 扱いを決めるのは Go。保存できなかったら(名前が空など)理由を出して、Go の値に戻す。
//
// 画面は一旦作ってから使い方を考える(0014 の 12)。ここは最初の形。
//
// 2つの形で使う(mode。0032 の段3):
//   own         … 設定の画面の「会話」。自分の配っていないプロファイルを変える。配られたもの(管理者が自分で配ったものも)は「配布されたプロファイル」の欄に読むだけで出す。
//                 一覧の上に「既定のプロファイル」(新しい会話の画面で最初に選ばれるもの)の選択肢を置く(0050 の決定事項 4。以前は各プロファイルの「最初に選ぶ」ボタン)
//   distributed … 管理の画面の「配るプロファイル」(ブラウザの管理者だけ)。配っているプロファイルの値・決まり・配る相手・資料のフォルダを変える。「複製して配布」で、自分のプロファイルを写してそのまま配る
//
// 配る(0018 の 11-1・決定事項 22):
//   - 配る相手は人を選ぶ部品で選ぶ(0031)。一覧に「全員に配っている」「N 人に配っている」の印
//   - 受け取った人には「配布されたプロファイル」の欄を分けて出す。値は読むだけ(fieldset ごと無効にする)で、「複製」で自分のプロファイルにする
//   - 配られたものも既定にできる(配られなくなったら、Go が自分のプロファイルに戻す)
import { useCallback, useEffect, useRef, useState } from "react";
import { LuCopy, LuPlus } from "react-icons/lu";
import { DeleteButton } from "./DeleteButton";
import { ChatService, isBrowser, me } from "../backend";
import { countPeople, targetLabel, useDirectory, type Directory, type Target } from "../directory";
import { PROFILE_WORDS, SharePicker } from "./SharePicker";
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
  mode?: "own" | "distributed"; // 設定の画面(自分のもの)か、管理の画面(配るもの)か。既定は own
}

// inMode は、その形の一覧に出すか(own は自分のものと配られたもの全部、distributed は自分が変えられる配っているもの)
function inMode(i: ProfileInfo, distributed: boolean): boolean {
  return distributed ? !!i.profile.share && !i.profile.read_only : true;
}

export function ProfileSettings({ models, servers, disabled, onApplyLimits, onError, onChanged, onToolsChanged, mode = "own" }: Props) {
  const distributed = mode === "distributed";
  const [dir] = useDirectory(); // 配っている印の人数(グループをメンバーに広げて数える)
  const [copying, setCopying] = useState(false); // 写して配るの欄を出しているか(distributed)
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
    const pool = list.filter(i => inMode(i, distributed));
    const keep = (id: string | null | undefined) => (id && pool.some(i => i.profile.id === id) ? id : null);
    const id = keep(select) ?? keep(selected) ?? pool.find(i => i.default)?.profile.id ?? pool[0]?.profile.id ?? null;
    setSelected(id);
    setEdit(list.find(i => i.profile.id === id)?.profile ?? null);
  }, [selected, distributed]);
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

  // own:自分の配っていないもの(変えられる)と、配られたもの(読むだけ。管理者が自分で配ったものも、変えるのは管理の画面)に分ける。
  // distributed:配っているもの(管理者が変えられる)だけ
  const own = infos.filter(i => !i.profile.read_only && !i.profile.share);
  const mine = distributed ? infos.filter(i => inMode(i, true)) : own;
  const received = distributed ? [] : infos.filter(i => i.profile.read_only || !!i.profile.share);
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
      {i.default && <span className="tool-tag" title="既定のプロファイル(新しい会話の画面で最初に選ばれる)">既定</span>}
      {i.profile.archived && <span className="tool-tag" title="新しい会話では選べない。決まりは、このプロファイルから作った会話に効く">アーカイブ済み</span>}
      {!i.profile.read_only && i.profile.share && <ShareTag profile={i.profile} dir={dir} />}
      {i.profile.owner_name && i.profile.owner_name !== me.name && (
        <span className="profile-owner" title="配布した人">{i.profile.owner_name}</span>
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
      distributed={distributed}
      models={models}
      servers={servers}
      disabled={disabled}
      onChange={change}
      onApplyLimits={onApplyLimits}
      onToolsChanged={onToolsChanged}
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
      onShare={(share, users, groups) => act(() => ChatService.SetProfileShare(edit.id, share, users, groups), edit.id)}
      onError={onError}
    />
  );

  const editable = !!edit && !edit.read_only && (distributed || !edit.share);
  return (
    <>
      {!distributed && (
        <DefaultProfile infos={infos} onChange={id => act(() => ChatService.SetDefaultProfile(id), selected ?? undefined)} />
      )}
      <div className="profile-list" role="listbox">
        {mine.map(item)}
        {distributed ? (
          <button type="button" className="profile-item profile-add" onClick={() => setCopying(c => !c)} aria-expanded={copying} title="自分のプロファイルを複製して、そのまま配布する(元は自分のプロファイルとして残る)">
            <LuPlus /> 複製して配布
          </button>
        ) : (
          <button type="button" className="profile-item profile-add" onClick={create} title="選んでいるプロファイルの値を複製して、新しいプロファイルを作成する">
            <LuPlus /> 作成
          </button>
        )}
      </div>
      {distributed && mine.length === 0 && !copying && <p className="hint">配布しているプロファイルはありません</p>}
      {copying && (
        <ShareCopyForm
          own={own}
          onDone={id => {
            setCopying(false);
            onChanged();
            reload(id).catch(err => onError(errorMessage(err)));
          }}
          onCancel={() => setCopying(false)}
          onError={onError}
        />
      )}
      {editable && !copying && editor}
      {received.length > 0 && (
        <>
          <h2 className="section" title="管理者が配布したプロファイル。新しい会話で選べるが、値と決まりは変えられない(変えたいときは複製する)">配布されたプロファイル</h2>
          <div className="profile-list" role="listbox">{received.map(item)}</div>
        </>
      )}
      {!distributed && edit && !editable && editor}

    </>
  );
}

function ProfileEditor({ profile, info, distributed, models, servers, disabled, onChange, onApplyLimits, onToolsChanged, onArchive, onDelete, onCopy, onShare, onError }: {
  profile: Profile;
  info: ProfileInfo;
  distributed: boolean; // 管理の画面(配るもの)の編集か
  models: ModelEntry[];
  servers: ServerInfo[];
  disabled: boolean;
  onChange(f: (p: Profile) => Profile): void;
  onApplyLimits(history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void): void;
  onToolsChanged(): void;
  onArchive(archived: boolean): void;
  onDelete(): void;
  onCopy(): void;                                  // 複製して自分のプロファイルにする(配られたもの)
  onShare(share: string, users: string[], groups: string[]): void; // 配る範囲と相手(人とグループ)を変える(管理者)
  onError(text: string): void;
}) {
  // 配られたもの・(設定の画面では)自分で配ったものは読むだけ。配ったものを変えるのは管理の画面(0032 の段3)
  if (profile.read_only || (!distributed && profile.share)) return <ReceivedProfile profile={profile} models={models} onCopy={onCopy} />;
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
          disabled={info.default && !profile.archived}
          title={info.default ? "既定のプロファイルはアーカイブできない" : profile.archived ? "新しい会話で選べるようにする" : "新しい会話では選べなくする(決まりは、このプロファイルから作った会話に効き続ける)"}
          onClick={() => onArchive(!profile.archived)}
        >
          {profile.archived ? "アーカイブ解除" : "アーカイブ"}
        </button>
        <DeleteButton
          question={`プロファイル「${profile.name}」を削除しますか?`}
          disabled={info.default || info.uses > 0}
          title={info.default ? "既定のプロファイルは削除できない" : info.uses > 0 ? `このプロファイルから作った会話が ${info.uses} 個あるので削除できない(アーカイブはできる)` : "このプロファイルを削除する"}
          onConfirm={onDelete}
        />
      </div>
      {distributed && <ShareForm profile={profile} onShare={onShare} />}
      {distributed && <FolderField profile={profile} onChange={onChange} onError={onError} />}

      <ConversationSettingsForm settings={settings} models={models} lock={lock} onChange={setValues} />
      <LimitsForm
        history={settings.history_tokens}
        reply={settings.reply_tokens}
        disabled={disabled}
        lock={lock}
        onApply={(h, r) => onApplyLimits(h, r, setValues)}
      />

      <h2 className="section" title={isBrowser
        ? "このプロファイルでのツールの扱い(最初から使う・選べば使える・使えない)。確認するかは aibow-server の既定(管理の画面の「ツール」)"
        : "このプロファイルでのツールの扱い(最初から使う・選べば使える・使えない)と、確認しない(この PC の設定)"}>ツール</h2>
      <ProfileTools profile={profile} servers={servers} lock={lock} onChange={onChange} onPCChanged={onToolsChanged} />
    </div>
  );
}

// FolderField は、プロファイルの資料のフォルダ(ブラウザだけ。0024 の 12-2)。選べるのは管理者だけで、ほかの人には名前だけを出す。
// 配っているプロファイルで、配られた人の会話の作業フォルダになる(配っていなければ効かない)。aibow-server にフォルダが無ければ出さない
function FolderField({ profile, onChange, onError }: { profile: Profile; onChange(f: (p: Profile) => Profile): void; onError(text: string): void }) {
  const [names, setNames] = useState<string[]>([]);
  useEffect(() => {
    ChatService.Folders().then(n => setNames(n ?? []), err => onError(errorMessage(err)));
  }, [onError]);
  const folder = profile.rules.folder ?? "";
  if (names.length === 0 && !folder) return null;
  const title = "このプロファイルの会話で読み書きする aibow-server の資料のフォルダ。配布しているプロファイルで、配布された人だけが使える";
  if (!me.admin || profile.read_only) {
    return (
      <div className="profile-share">
        <label title={title}>資料のフォルダ</label>
        <span>{folder || "なし"}</span>
      </div>
    );
  }
  return (
    <div className="profile-share">
      <label htmlFor="profile-folder" title={title}>資料のフォルダ</label>
      <select id="profile-folder" value={folder} onChange={e => {
        const v = e.target.value;
        onChange(p => ({ ...p, rules: { ...p.rules, folder: v } }));
      }}>
        <option value="">なし</option>
        {names.map(n => <option key={n} value={n}>{n}</option>)}
      </select>
    </div>
  );
}

// ShareTag は、配っているプロファイルの印(一覧)。人数はグループを今のメンバーに広げて数える(人を選ぶ部品と同じ数え方。0031)
function ShareTag({ profile, dir }: { profile: Profile; dir: Directory }) {
  if (profile.share === "all") return <span className="tool-tag share-tag" title="全員の新しい会話で選べる">全員に配布</span>;
  const targets: Target[] = [
    ...(profile.share_groups ?? []).map(name => ({ kind: "group" as const, name })),
    ...(profile.share_with ?? []).map(name => ({ kind: "user" as const, name })),
  ];
  return (
    <span className="tool-tag share-tag" title={`配布している相手: ${targets.map(t => targetLabel(t, dir)).join("・")}`}>
      {countPeople(targets, dir, me.name)} 人に配布
    </span>
  );
}

// ShareForm は、管理者がプロファイルを配る範囲と相手を選ぶ欄(0018 の決定事項 22)。会話の共有と同じ人を選ぶ部品で選び、確定してから変える(0031 の決定事項 6〜13)。
function ShareForm({ profile, onShare }: { profile: Profile; onShare(share: string, users: string[], groups: string[]): void }) {
  const [dir] = useDirectory();
  return (
    <SharePicker
      id="profile-share"
      label="配布"
      labelTitle="配布したプロファイルは、受け取った人の新しい会話で選べる。値と決まりは受け取った人には変えられず、ほかの管理者は変えられる"
      words={PROFILE_WORDS}
      share={profile.share ?? ""}
      users={profile.share_with ?? []}
      groups={profile.share_groups ?? []}
      dir={dir}
      self={me.name}
      notes={next => (next.share === "" ? [`配布を停止すると、作った人(${profile.owner_name || me.name})の自分のプロファイルに戻り、管理の画面の一覧から外れます。`] : [])}
      onCommit={onShare}
    />
  );
}

// ShareCopyForm は「複製して配布」(0032 の段3)。元にする自分のプロファイルと配る相手を選び、確定したときに写してそのまま配る(Go の ShareCopy)。
// 名前は元と同じ(配ったもの同士で重なれば Go が断る)。資料のフォルダは写さない。写したあとは元と関係を持たない
function ShareCopyForm({ own, onDone, onCancel, onError }: { own: ProfileInfo[]; onDone(id: string): void; onCancel(): void; onError(text: string): void }) {
  const [source, setSource] = useState(own.find(i => i.default)?.profile.id ?? own[0]?.profile.id ?? "");
  const [dir] = useDirectory();
  if (own.length === 0) return <p className="hint">複製できる自分のプロファイルがありません</p>;
  return (
    <div className="profile-editor share-copy">
      <label htmlFor="copy-source" title="値と決まりを複製する(資料のフォルダは複製しない)。元は自分のプロファイルとして残り、あとで変えても配布したものは変わらない">元にするもの</label>
      <select id="copy-source" value={source} onChange={e => setSource(e.target.value)}>
        {own.map(i => <option key={i.profile.id} value={i.profile.id}>{i.profile.name}</option>)}
      </select>
      <SharePicker
        id="copy-share"
        label="配布"
        labelTitle="配布したプロファイルは、受け取った人の新しい会話で選べる"
        words={PROFILE_WORDS}
        share=""
        users={[]}
        groups={[]}
        dir={dir}
        self={me.name}
        onCommit={(share, users, groups) => {
          ChatService.ShareCopy(source, share, users, groups).then(p => onDone(p.id), err => onError(errorMessage(err)));
        }}
      />
      <div className="profile-actions">
        <button type="button" onClick={onCancel}>キャンセル</button>
      </div>
    </div>
  );
}

// ReceivedProfile は、配られたプロファイル(読むだけ)。値と決まりは見せるが変えられない(fieldset ごと無効)。複製すれば自分のものになる
function ReceivedProfile({ profile, models, onCopy }: {
  profile: Profile;
  models: ModelEntry[];
  onCopy(): void;
}) {
  const settings = asSettings(profile.values);
  const lock: Lock = { fixed: profile.rules.fixed ?? [], profile: profile.name, onToggle: () => {} };
  return (
    <div className="profile-editor">
      <div className="profile-received">
        <span className="profile-received-name">{profile.name}</span>
        {profile.owner_name && <span className="profile-owner" title="配布した人">{profile.owner_name}</span>}
      </div>
      <div className="profile-actions">
        <button type="button" className="with-icon" onClick={onCopy} title="値と決まりを複製して、自分のプロファイルを作成する(複製したものは変えられる)">
          <LuCopy /> 複製
        </button>
      </div>
      <fieldset disabled className="profile-readonly">
        <ConversationSettingsForm settings={settings} models={models} lock={lock} onChange={() => {}} />
        <LimitsForm history={settings.history_tokens} reply={settings.reply_tokens} disabled lock={lock} onApply={() => {}} />
      </fieldset>
    </div>
  );
}

// DefaultProfile は、既定のプロファイル(新しい会話の画面で最初に選ばれるもの)の選択肢(0050 の決定事項 4)。
// 選べるのは、自分のもの・配られたもの(アーカイブ済みは除く)。配られたものを選んで、配られなくなったら、Go が自分のプロファイルに戻す。
// 既定は人ごとの設定なので、管理の画面(配るプロファイル)には置かない
function DefaultProfile({ infos, onChange }: { infos: ProfileInfo[]; onChange(id: string): void }) {
  const current = infos.find(i => i.default)?.profile.id ?? "";
  const choices = infos.filter(i => !i.profile.archived || i.default);
  if (choices.length === 0) return null;
  return (
    <div className="profile-default">
      <label htmlFor="profile-default" title="新しい会話の画面で最初に選ばれるプロファイル。配布されたものも選べる(配布されなくなったら、自分のプロファイルに戻る)">
        既定のプロファイル
      </label>
      <select id="profile-default" value={current} onChange={e => onChange(e.target.value)}>
        {choices.map(i => (
          <option key={i.profile.id} value={i.profile.id}>
            {i.profile.name}{i.profile.read_only && i.profile.owner_name ? `(${i.profile.owner_name} が配布)` : ""}
          </option>
        ))}
      </select>
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
