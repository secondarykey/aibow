// 設定の画面の「会話」の「プロファイル」: プロファイル(名前の付いた新しい会話の既定。0014)の一覧と編集。
//
// 一覧で選んだプロファイルの名前・値・決まりを、その下で変える。変えた値は下書きで、画面の下の「保存」で確定する(0053)。
//   値:会話の設定と同じ入力欄(ConversationSettingsForm)と、使うツールのグループ。新しい会話はこの写しから始まる。今ある会話には効かない
//   決まり:項目ごとの固定(名前の横の錠)と、使えないツール(グループごと・ツールごと)。保存すると今ある会話にもすぐ効く
//   ツールの「確認しない」(この PC の tools.json)も下書きに入れ、同じ「保存」でまとめて書く(Go の SaveProfile。0053 の決定事項 7)
// 保存していない変更があるときに、一覧で別のプロファイルを選ぶ・作成 / 複製 / アーカイブ / 削除 / 配布をすると、先に知らせる(unsaved.tsx)。
// 扱いを決めるのは Go。保存できなかったら(名前が空など)理由を出して、下書きはそのまま残す(打ったものが消えないように。決定事項 13)。
//
// 画面は一旦作ってから使い方を考える(0014 の 12)。ここは最初の形。
//
// 2つの形で使う(mode。0032 の段3):
//   own         … 設定の画面の「会話」。自分の配っていないプロファイルを変える。配られたもの(管理者が自分で配ったものも)は「配布されたプロファイル」の欄に読むだけで出す。
//                 既定のプロファイル(新しい会話の画面で最初に選ばれるもの)は、一覧の行の「既定」の札で見せ、選んだプロファイルの操作の行の「既定にする」で変える
//                 (0056。0050 の決定事項 4 では一覧の上の選択肢にしていたのを、ボタンに戻した。一覧の行は <button role="option"> なので、ボタンは行に置かず編集の欄に置く)
//   distributed … 管理の画面の「配るプロファイル」(ブラウザの管理者だけ)。配っているプロファイルの値・決まり・配る相手・サーバーのワークスペースを変える。
//                 「新規」で配布プロファイルを最初から作る(0059。以前の「複製して配布」はなくした)。配布プロファイルは個人プロファイルと行き来させない。
//                 アーカイブは「停止」「再開」と呼ぶ(列は同じ archived。止めると受け取った人の新しい会話で選べない)。削除できるかは全員の会話で決める(uses_all)
//
// 配る(0018 の 11-1・決定事項 22):
//   - 配る相手は人を選ぶ部品で選ぶ(0031)。一覧に「全員に配っている」「N 人に配っている」の印
//   - 受け取った人には「配布されたプロファイル」の欄を分けて出す。値は読むだけ(fieldset ごと無効にする)で、「複製」で自分のプロファイルにする
//   - 配られたものも既定にできる(配られなくなったら、Go が自分のプロファイルに戻す)
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import type { ToolPolicyChange } from "../../bindings/github.com/secondarykey/aibow/ui";
import { SaveBar, sameValue, useUnsaved, useUnsavedEntry } from "../unsaved";
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

interface Props {
  models: ModelEntry[];
  servers: ServerInfo[]; // settings.json の MCP のサーバー
  disabled: boolean;     // 生成中・読み込み中(上限を変えた保存は、作業領域を作り直すのでできない)
  onTryLimits(history: number, reply: number): Promise<void>; // 上限を変えて保存する前に、作業領域を作り直してみる(入らなければ reject)
  onError(text: string): void;
  onChanged(): void; // プロファイルを変えた(新しい会話の画面の写し・新しい会話の既定を読み直す)
  onToolsChanged(): void; // この PC のツールの扱い(確認しない)を変えた(メーターを読み直す)
  mode?: "own" | "distributed"; // 設定の画面(自分のもの)か、管理の画面(配るもの)か。既定は own
}

// inMode は、その形の一覧に出すか(own は自分のものと配られたもの全部、distributed は自分が変えられる配っているもの)
function inMode(i: ProfileInfo, distributed: boolean): boolean {
  return distributed ? !!i.profile.share && !i.profile.read_only : true;
}

export function ProfileSettings({ models, servers, disabled, onTryLimits, onError, onChanged, onToolsChanged, mode = "own" }: Props) {
  const distributed = mode === "distributed";
  const [dir] = useDirectory(); // 配っている印の人数(グループをメンバーに広げて数える)
  const [creating, setCreating] = useState(false); // 配布プロファイルを作る欄を出しているか(distributed の「新規」。0059)
  const [infos, setInfos] = useState<ProfileInfo[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [edit, setEditState] = useState<Profile | null>(null); // 選んだプロファイルの下書き(画面で変えている途中の値)
  const editRef = useRef<Profile | null>(null);
  const setEdit = (p: Profile | null) => {
    editRef.current = p;
    setEditState(p);
  };
  // この PC のツールの「確認しない」の下書き(まだ tools.json に書いていない変更)。保存したら pcVersion を進めて読み直させる
  const [pc, setPcState] = useState<ToolPolicyChange[]>([]);
  const pcRef = useRef<ToolPolicyChange[]>([]);
  const setPc = (c: ToolPolicyChange[]) => {
    pcRef.current = c;
    setPcState(c);
  };
  const [pcVersion, setPcVersion] = useState(0);
  const [saving, setSaving] = useState(false);
  const { guard } = useUnsaved();

  const reload = useCallback(async (select?: string) => {
    const list = (await ChatService.Profiles()) ?? [];
    setInfos(list);
    const pool = list.filter(i => inMode(i, distributed));
    const keep = (id: string | null | undefined) => (id && pool.some(i => i.profile.id === id) ? id : null);
    const id = keep(select) ?? keep(selected) ?? pool.find(i => i.default)?.profile.id ?? pool[0]?.profile.id ?? null;
    setSelected(id);
    setEdit(list.find(i => i.profile.id === id)?.profile ?? null);
    setPc([]);
  }, [selected, distributed]);
  useEffect(() => {
    reload().catch(err => onError(errorMessage(err)));
    // 最初の1回だけ(選んでいるものを変えるたびに読み直さない)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 変えた値は下書きに入れるだけ(保存は「保存」)
  const change = (f: (p: Profile) => Profile) => {
    const cur = editRef.current;
    if (!cur) return;
    setEdit(f(cur));
  };

  const info = infos.find(i => i.profile.id === selected);
  const saved = info?.profile ?? null; // 保存した値(Go が返したもの)
  const editable = !!edit && !edit.read_only && (distributed || !edit.share);
  const dirty = editable && !!saved && (!sameValue(edit, saved) || pc.length > 0);
  const limitsChanged = !!edit && !!saved &&
    (edit.values.history_tokens !== saved.values.history_tokens || edit.values.reply_tokens !== saved.values.reply_tokens);
  const blocked = !edit ? undefined
    : edit.name.trim() === "" ? "名前を入れてください"
      : limitsChanged && disabled ? "生成中・読み込み中は、会話の上限と返事の上限を変えて保存できません"
        : undefined;

  // 保存:上限を変えていれば作業領域を作り直してみてから、プロファイルとこの PC の確認しないをまとめて書く。
  // できなければ理由を出して false(下書きは残す。決定事項 13)。保存できても「保存しました」は出さない(決定事項 14)
  const save = async (): Promise<boolean> => {
    const p = editRef.current;
    if (!p || !saved) return true;
    if (blocked) {
      onError(`プロファイルを保存できません: ${blocked}`);
      return false;
    }
    const tools = pcRef.current;
    setSaving(true);
    try {
      if (limitsChanged) await onTryLimits(p.values.history_tokens, p.values.reply_tokens);
      await ChatService.SaveProfile({ ...p, name: p.name.trim() }, tools);
    } catch (err) {
      onError(`プロファイルを保存できません: ${errorMessage(err)}`);
      if (tools.length > 0) setPcVersion(v => v + 1); // 途中まで書けたかもしれないので、この PC の値を読み直す
      setSaving(false);
      return false;
    }
    setSaving(false);
    if (tools.length > 0) {
      setPcVersion(v => v + 1);
      onToolsChanged();
    }
    onChanged();
    await reload(p.id).catch(err => onError(errorMessage(err)));
    return true;
  };
  // キャンセル:確かめずに保存した値に戻す(消えるのは保存していない下書きだけ。決定事項 12)
  const cancel = () => {
    setEdit(saved);
    setPc([]);
  };
  useUnsavedEntry(dirty, save, cancel);

  // 一覧の操作(作成・複製・アーカイブ・削除・配布)は、保存していない変更があれば先に知らせる(決定事項 18)。
  // f は選び直すプロファイルの ID を返してもよい(作成・複製で、作ったものを選ぶ)
  const act = (f: () => Promise<string | void>, select?: string) => {
    guard(() => {
      f()
        .then(id => {
          onChanged();
          return reload(id || select);
        })
        .catch(err => onError(errorMessage(err)));
    });
  };

  const create = () => act(async () => {
    const base = saved ?? infos.find(i => i.default)?.profile;
    if (!base) return;
    return (await ChatService.CreateProfile(freeName(own), base.values, {})).id;
  });
  // 既定にするは、下書きに関わらない人ごとの設定なので、知らせずにそのまま変える(一覧の「既定」の印だけ読み直し、下書きは残す)
  const setDefault = (id: string) => {
    ChatService.SetDefaultProfile(id)
      .then(() => {
        onChanged();
        return ChatService.Profiles().then(list => setInfos(list ?? []));
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
      onClick={() => {
        if (i.profile.id !== selected) guard(() => { reload(i.profile.id).catch(err => onError(errorMessage(err))); });
      }}
    >
      <span className="profile-name">{i.profile.name}</span>
      {i.default && <span className="tool-tag" title="既定のプロファイル(新しい会話の画面で最初に選ばれる)">既定</span>}
      {i.profile.archived && <ArchivedTag profile={i.profile} distributed={distributed} />}
      {!i.profile.read_only && i.profile.share && <ShareTag profile={i.profile} dir={dir} />}
      {i.profile.owner_name && i.profile.owner_name !== me.name && (
        <span className="profile-owner" title="配布した人">{i.profile.owner_name}</span>
      )}
      {distributed
        ? <span className="profile-uses" title="このプロファイルから作った会話の数(受け取った人の会話も数える)">会話 {i.uses_all}</span>
        : <span className="profile-uses" title="このプロファイルから作った会話の数">会話 {i.uses}</span>}
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
      onChange={change}
      pc={pc}
      onPc={setPc}
      pcVersion={pcVersion}
      onArchive={archived => act(() => ChatService.SetProfileArchived(edit.id, archived), edit.id)}
      onDelete={() => act(() => ChatService.DeleteProfile(edit.id), infos.find(i => i.default)?.profile.id)}
      onCopy={() => act(async () => (await ChatService.CopyProfile(edit.id)).id)}
      onShare={(share, users, groups) => act(() => ChatService.SetProfileShare(edit.id, share, users, groups), edit.id)}
      onSetDefault={distributed ? undefined : () => setDefault(edit.id)}
      onError={onError}
      saveBar={<SaveBar dirty={dirty} saving={saving} blocked={dirty ? blocked : undefined} scope={distributed ? "配布した相手全員に効きます" : undefined} onSave={() => { save().catch(() => {}); }} onCancel={cancel} />}
    />
  );

  return (
    <>
      <div className="profile-list" role="listbox">
        {mine.map(item)}
        {distributed ? (
          <button type="button" className="profile-item profile-add" onClick={() => guard(() => setCreating(c => !c))} aria-expanded={creating} title="配布プロファイルを作成して、そのまま配布する">
            <LuPlus /> 新規
          </button>
        ) : (
          <button type="button" className="profile-item profile-add" onClick={create} title="選んでいるプロファイルの値を複製して、新しいプロファイルを作成する">
            <LuPlus /> 作成
          </button>
        )}
      </div>
      {distributed && mine.length === 0 && !creating && <p className="hint">配布しているプロファイルはありません。「新規」で作ります</p>}
      {creating && (
        <ShareNewForm
          name={freeName(mine)}
          models={models.filter(m => !m.disabled)}
          onDone={id => {
            setCreating(false);
            onChanged();
            reload(id).catch(err => onError(errorMessage(err)));
          }}
          onCancel={() => setCreating(false)}
          onError={onError}
        />
      )}
      {editable && !creating && editor}
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

function ProfileEditor({ profile, info, distributed, models, servers, onChange, pc, onPc, pcVersion, onArchive, onDelete, onCopy, onShare, onSetDefault, onError, saveBar }: {
  profile: Profile;                // 下書き
  info: ProfileInfo;               // 保存した値(一覧の印と、アーカイブ・削除できるか)
  distributed: boolean; // 管理の画面(配るもの)の編集か
  models: ModelEntry[];
  servers: ServerInfo[];
  onChange(f: (p: Profile) => Profile): void;
  pc: ToolPolicyChange[];          // この PC の確認しないの下書き
  onPc(changes: ToolPolicyChange[]): void;
  pcVersion: number;               // この PC の値を読み直す合図(保存したら進む)
  onArchive(archived: boolean): void;
  onDelete(): void;
  onCopy(): void;                                  // 複製して自分のプロファイルにする(配られたもの)
  onShare(share: string, users: string[], groups: string[]): void; // 配る範囲と相手(人とグループ)を変える(管理者)
  onSetDefault?(): void;                           // 既定にする(設定の画面だけ。既定は人ごとの設定なので、管理の画面には出さない)
  onError(text: string): void;
  saveBar: ReactNode;                              // 「保存」「キャンセル」(編集の欄のいちばん下に貼り付ける)
}) {
  // 配られたもの・(設定の画面では)自分で配ったものは読むだけ。配ったものを変えるのは管理の画面(0032 の段3)
  if (profile.read_only || (!distributed && profile.share)) return <ReceivedProfile profile={profile} info={info} models={models} onCopy={onCopy} onSetDefault={onSetDefault} />;
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
  const archived = info.profile.archived; // アーカイブは保存した値で決める(下書きでは変わらない)
  // 配布プロファイル(管理の画面)は、管理者自身の既定でも停止・削除できる(0059 の決定事項 14)。消せるかは全員の会話の数で決める(決定事項 15)
  const isDefault = !distributed && info.default;
  const uses = distributed ? info.uses_all : info.uses;

  // 名前も下書き(この画面の「保存」に入る。0053 の決定事項 9b)
  return (
    <div className="profile-editor">
      <label htmlFor="profile-name">名前</label>
      <input
        id="profile-name"
        type="text"
        value={profile.name}
        onChange={e => {
          const v = e.target.value;
          onChange(p => ({ ...p, name: v }));
        }}
      />
      <div className="profile-actions">
        {onSetDefault && <SetDefaultButton info={info} onClick={onSetDefault} />}
        {distributed ? (
          <button
            type="button"
            title={archived ? "受け取った人の新しい会話で、また選べるようにする" : "受け取った人の新しい会話で選べなくする(今ある会話は続けられ、決まりも効き続ける)"}
            onClick={() => onArchive(!archived)}
          >
            {archived ? "再開" : "停止"}
          </button>
        ) : (
          <button
            type="button"
            disabled={isDefault && !archived}
            title={isDefault ? "既定のプロファイルはアーカイブできない" : archived ? "新しい会話で選べるようにする" : "新しい会話では選べなくする(決まりは、このプロファイルから作った会話に効き続ける)"}
            onClick={() => onArchive(!archived)}
          >
            {archived ? "アーカイブ解除" : "アーカイブ"}
          </button>
        )}
        <DeleteButton
          question={distributed
            ? `配布プロファイル「${info.profile.name}」を削除しますか? 受け取った人の一覧からも消えます。元に戻せません`
            : `プロファイル「${info.profile.name}」を削除しますか?`}
          disabled={isDefault || uses > 0}
          title={isDefault ? "既定のプロファイルは削除できない"
            : uses > 0 ? (distributed
              ? `このプロファイルから作った会話が全員で ${uses} 個あるので削除できない(停止はできる)`
              : `このプロファイルから作った会話が ${uses} 個あるので削除できない(アーカイブはできる)`)
              : "このプロファイルを削除する"}
          onConfirm={onDelete}
        />
      </div>
      {distributed && <ShareForm profile={info.profile} onShare={onShare} />}
      {distributed && <WorkspaceField profile={profile} onChange={onChange} onError={onError} />}

      <ConversationSettingsForm settings={settings} models={models} lock={lock} onChange={setValues} />
      <LimitsForm history={settings.history_tokens} reply={settings.reply_tokens} lock={lock} onChange={setValues} />

      <h2 className="section" title={isBrowser
        ? "このプロファイルでのツールの扱い(最初から使う・選べば使える・使えない)。確認するかは aibow-server の既定(管理の画面の「ツール」)"
        : "このプロファイルでのツールの扱い(最初から使う・選べば使える・使えない)と、確認しない(この PC の設定)"}>ツール</h2>
      <ProfileTools profile={profile} servers={servers} lock={lock} onChange={onChange} pc={pc} onPc={onPc} pcVersion={pcVersion} />
      {saveBar}
    </div>
  );
}

// WorkspaceField は、プロファイルのサーバーのワークスペース(ブラウザだけ。0065 の決定事項 18)。選べるのは管理者だけで、ほかの人には名前だけを出す。
// 配っているプロファイルで、配られた人の会話のファイルのツールが扱うフォルダの組になる(配っていなければ効かない)。aibow-server にワークスペースが無ければ出さない
function WorkspaceField({ profile, onChange, onError }: { profile: Profile; onChange(f: (p: Profile) => Profile): void; onError(text: string): void }) {
  const [names, setNames] = useState<string[]>([]);
  useEffect(() => {
    ChatService.ServerWorkspaces().then(l => setNames((l ?? []).map(w => w.name)), err => onError(errorMessage(err)));
  }, [onError]);
  const workspace = profile.rules.workspace ?? "";
  if (names.length === 0 && !workspace) return null;
  const title = "このプロファイルの会話で読み書きする aibow-server のフォルダの組。配布しているプロファイルで、配布された人だけが使える";
  if (!me.admin || profile.read_only) {
    return (
      <div className="profile-share">
        <label title={title}>サーバーのワークスペース</label>
        <span>{workspace || "なし"}</span>
      </div>
    );
  }
  return (
    <div className="profile-share">
      <label htmlFor="profile-workspace" title={title}>サーバーのワークスペース</label>
      <select id="profile-workspace" value={workspace} onChange={e => {
        const v = e.target.value;
        onChange(p => ({ ...p, rules: { ...p.rules, workspace: v } }));
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
      noneAllowed={false}
      onCommit={onShare}
    />
  );
}

// ShareNewForm は、配布プロファイルを最初から作る欄(管理の画面の「新規」。0059 の決定事項 7)。名前・モデル・配布を決め、
// 配布の確定のボタンで、作ると配るを一度に行う(Go の ShareNew)。相手を決めずには作らない(管理者の自分のプロファイルと見分けがつかなくなるため)。
// モデル以外の値は、管理者の今の既定のプロファイルの値を Go が写す。決まりとサーバーのワークスペースは空から始め、作ったあと編集の欄で変える
function ShareNewForm({ name: initialName, models, onDone, onCancel, onError }: {
  name: string;          // 名前の初期値(配布プロファイル同士で重ならないもの)
  models: ModelEntry[];  // 使えるモデル(管理者が使えないようにしたものは除いてある)
  onDone(id: string): void;
  onCancel(): void;
  onError(text: string): void;
}) {
  const [name, setName] = useState(initialName);
  const [model, setModel] = useState(models[0]?.id ?? "");
  const [dir] = useDirectory();
  return (
    <div className="profile-editor share-new">
      <label htmlFor="new-profile-name">名前</label>
      <input id="new-profile-name" type="text" value={name} onChange={e => setName(e.target.value)} />
      <label htmlFor="new-profile-model" title="配布プロファイルで使うモデル。使えるモデルから選ぶ">モデル</label>
      <select id="new-profile-model" value={model} onChange={e => setModel(e.target.value)}>
        {models.length === 0 && <option value="">使えるモデルがありません</option>}
        {models.map(m => <option key={m.id} value={m.id}>{`${m.name}(${m.size_gb.toFixed(1)} GB)`}</option>)}
      </select>
      <SharePicker
        id="new-profile-share"
        label="配布"
        labelTitle="配布したプロファイルは、受け取った人の新しい会話で選べる"
        words={PROFILE_WORDS}
        share=""
        users={[]}
        groups={[]}
        dir={dir}
        self={me.name}
        noneAllowed={false}
        onCommit={(share, users, groups) => {
          if (name.trim() === "") {
            onError("配布プロファイルを作成できません: 名前を入れてください");
            return;
          }
          ChatService.ShareNew(name.trim(), model, share, users, groups).then(p => onDone(p.id), err => onError(`配布プロファイルを作成できません: ${errorMessage(err)}`));
        }}
      />
      <div className="profile-actions">
        <button type="button" onClick={onCancel}>キャンセル</button>
      </div>
    </div>
  );
}

// ArchivedTag は、一覧の行のアーカイブの札。配布プロファイル(管理の画面・受け取った人の欄)は「停止中」と呼ぶ(0059 の決定事項 12・13)
function ArchivedTag({ profile, distributed }: { profile: Profile; distributed: boolean }) {
  if (distributed) return <span className="tool-tag" title="受け取った人の新しい会話では選べない。今ある会話は続けられ、決まりも効き続ける">停止中</span>;
  if (profile.read_only || profile.share) {
    return <span className="tool-tag" title="管理者が配布を停止した。新しい会話では選べない。今ある会話は続けられる">停止中</span>;
  }
  return <span className="tool-tag" title="新しい会話では選べない。決まりは、このプロファイルから作った会話に効く">アーカイブ済み</span>;
}

// ReceivedProfile は、配られたプロファイル(読むだけ)。値と決まりは見せるが変えられない(fieldset ごと無効)。複製すれば自分のものになる
function ReceivedProfile({ profile, info, models, onCopy, onSetDefault }: {
  profile: Profile;
  info: ProfileInfo;
  models: ModelEntry[];
  onCopy(): void;
  onSetDefault?(): void;
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
        {onSetDefault && <SetDefaultButton info={info} received onClick={onSetDefault} />}
        <button type="button" className="with-icon" onClick={onCopy} title="値と決まりを複製して、自分のプロファイルを作成する(複製したものは変えられる)">
          <LuCopy /> 複製
        </button>
      </div>
      <fieldset disabled className="profile-readonly">
        <ConversationSettingsForm settings={settings} models={models} lock={lock} onChange={() => {}} />
        <LimitsForm history={settings.history_tokens} reply={settings.reply_tokens} lock={lock} onChange={() => {}} />
      </fieldset>
    </div>
  );
}

// SetDefaultButton は、選んだプロファイルを既定(新しい会話の画面で最初に選ばれるもの)にするボタン(0056)。
// 押すとその場で Go に送る(会話の値の保存とは別の操作)。既定のもの・アーカイブ済みのものは押せない(新しい会話で選べないものは既定にしない)。
// 配られたものも既定にできる。配られなくなったら、Go が自分のプロファイルに戻す
function SetDefaultButton({ info, received, onClick }: { info: ProfileInfo; received?: boolean; onClick(): void }) {
  const title = info.default
    ? "既定のプロファイル(新しい会話の画面で最初に選ばれる)"
    : info.profile.archived
      ? (received ? "停止中は既定にできない" : "アーカイブ済みは既定にできない")
      : "新しい会話の画面で、このプロファイルを最初に選ぶ" + (received ? "(配布されなくなったら、自分のプロファイルに戻る)" : "");
  return (
    <button type="button" disabled={info.default || info.profile.archived} title={title} onClick={onClick}>
      既定にする
    </button>
  );
}

// freeName は、新しいプロファイルの名前。同じまとまりのプロファイルと重ならないよう、重なれば番号を付ける(Go は重なる名前を断る)。
// infos は比べる相手(自分の画面は自分のプロファイル、管理の画面は配布プロファイル。名前は同じまとまりの中で重ならなければよい)
function freeName(infos: ProfileInfo[]): string {
  const base = "新しいプロファイル";
  const used = new Set(infos.map(i => i.profile.name));
  if (!used.has(base)) return base;
  for (let n = 2; ; n++) {
    if (!used.has(`${base} ${n}`)) return `${base} ${n}`;
  }
}
