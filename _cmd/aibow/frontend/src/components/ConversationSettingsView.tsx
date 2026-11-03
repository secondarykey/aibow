// 会話の設定の画面: 会話の名前と ID / 開いている会話だけの設定 / この会話のツール / モデルに渡した文字列
//
// 入力欄の下(メーターの右)の歯車で開き、チャットの代わりに会話の場所に出す(設定の画面と同じく別の画面として扱う)。
// 会話は「新しい会話の既定」(設定の画面)の写しから始まり、ここで変えた設定はこの会話にだけ効く。
// 「会話に戻る」・一覧で会話を選ぶ・「+ 新規」・一覧の下の歯車で閉じる。
import { useEffect, useRef, useState } from "react";
import { LuArrowLeft } from "react-icons/lu";
import type { ConversationSettings, Profile, WorktreeState } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../../bindings/github.com/secondarykey/aibow/models/index";
import { ConversationSettingsForm, LimitsForm } from "./ConversationSettingsForm";
import { lockOf } from "../profileRules";
import { ConversationTools } from "./ConversationTools";
import { isBrowser, me } from "../backend";
import { useDirectory } from "../directory";
import { CONVERSATION_WORDS, SharePicker } from "./SharePicker";
import { CopyButton } from "./CopyButton";

interface Props {
  id: string;                             // 会話の ID(UUIDv7。ログや DB を調べるときの手がかり)
  title: string;                          // 会話の名前
  onRename(title: string): void;          // 名前を変える(一覧のダブルクリック・F2 と同じ)
  settings: ConversationSettings;
  models: ModelEntry[]; // 選べるモデル
  errors: { id: number; text: string }[]; // この画面を開いてから出たエラー(ふだんは会話の表示に出るもの)
  onClose(): void;                        // 会話に戻る
  onChange(patch: Partial<ConversationSettings>): void;
  profile: Profile | null;              // この会話のプロファイル(どのプロファイルから作ったか・決まり。0014)
  folder: string;                       // ブラウザの会話の資料のフォルダの名前(無ければ空。0024 の 12-2)
  workDir: string;                      // ツールが使うフォルダ(アプリはワークスペースのメインのフォルダ、ブラウザは資料のフォルダの名前。無ければ空)
  onSaveToProfile(): void;              // この会話の設定で、プロファイルの値を上書きする
  onSaveAsProfile(name: string): void;  // この会話の設定で、新しいプロファイルを作る
  lastPrompt: string;
  lastPromptFormat: string; // その文字列を組み立てた書式の名前(0026 の決定事項 17)
  servers: ServerInfo[]; // settings.json の MCP のサーバー
  limitsDisabled: boolean; // 生成中・読み込み中は上限を変えられない(作業領域を作り直すため)
  onApplyLimits(history: number, reply: number): void;
  share: string;            // 共有の範囲(0018 の 11-2。"" 非公開 / all / users)
  shareWith: string[];      // 共有の相手(名前)
  shareGroups: string[];    // 共有の相手のグループ(名前。0031)
  onShare(share: string, users: string[], groups: string[]): void;
  worktree: WorktreeState | null;           // worktree の状態(0039。アプリだけ。名前の欄に使う)
  onRenameWorktree(name: string): Promise<void>; // worktree の名前を変える(作る前だけ)。断られたら reject
}

// TitleField は会話の名前の入力欄。打っている間は保存せず、Enter か欄を離れたときに保存する(一覧の名前の変更と同じ)。
// Esc で元に戻す。空にしたときは保存せず元に戻す(Go は空の名前を受け付けない)
function TitleField({ title, onRename }: { title: string; onRename(title: string): void }) {
  const [value, setValue] = useState(title);
  useEffect(() => setValue(title), [title]);
  // Esc で離れるときは保存しない(blur のときの value は、元に戻す前の打った文のままなので、印で止める)
  const cancelled = useRef(false);
  const commit = () => {
    if (cancelled.current) {
      cancelled.current = false;
      return;
    }
    const t = value.trim();
    if (t && t !== title) onRename(t);
    else setValue(title);
  };
  return (
    <>
      <label htmlFor="conv-title">名前</label>
      <input
        id="conv-title"
        type="text"
        value={value}
        onChange={e => setValue(e.target.value)}
        onBlur={commit}
        onKeyDown={e => {
          if (e.nativeEvent.isComposing) return; // 変換の確定の Enter では保存しない
          if (e.key === "Enter") e.currentTarget.blur();
          if (e.key === "Escape") {
            cancelled.current = true;
            setValue(title);
            e.currentTarget.blur();
          }
        }}
      />
    </>
  );
}

// WorktreeNameField は、worktree の名前(ブランチとフォルダの名前に使う英語の短い名前。0039)。
// 最初の送信でモデルが書いたものが入る。作る前なら直せる(Enter か欄を離れたときに保存。Esc で戻す)。作ったあとは実際のブランチ名を出すだけ
function WorktreeNameField({ state, onRename }: { state: WorktreeState; onRename(name: string): Promise<void> }) {
  const name = state.name ?? "";
  const [value, setValue] = useState(name);
  useEffect(() => setValue(name), [name]);
  const cancelled = useRef(false);
  const label = (
    <label htmlFor="conv-worktree-name" title="worktree のブランチ(aibow/<名前>-<会話の ID の末尾>)とフォルダの名前。英小文字・数字を - でつなぐ">
      worktree の名前
    </label>
  );
  if (state.active) {
    return (
      <>
        {label}
        <div className="conv-profile"><code>{state.branch}</code></div>
      </>
    );
  }
  const commit = () => {
    if (cancelled.current) {
      cancelled.current = false;
      return;
    }
    const v = value.trim();
    if (v !== name) onRename(v).catch(() => setValue(name)); // 断られたら(使えない文字など)、保存してある名前に戻す。理由はエラーに出る
  };
  return (
    <>
      {label}
      <input
        id="conv-worktree-name"
        type="text"
        value={value}
        spellCheck={false}
        onChange={e => setValue(e.target.value)}
        onBlur={commit}
        onKeyDown={e => {
          if (e.nativeEvent.isComposing) return;
          if (e.key === "Enter") e.currentTarget.blur();
          if (e.key === "Escape") {
            cancelled.current = true;
            setValue(name);
            e.currentTarget.blur();
          }
        }}
      />
    </>
  );
}

export function ConversationSettingsView({ id, title, onRename, settings, models, errors, onClose, onChange, profile, folder, workDir, onSaveToProfile, onSaveAsProfile, lastPrompt, lastPromptFormat, servers, limitsDisabled, onApplyLimits,
  share, shareWith, shareGroups, onShare, worktree, onRenameWorktree,
}: Props) {
  const lock = lockOf(profile);
  return (
    <section className="settings-view">
      <div className="settings-inner">
        <div className="settings-header">
          <h1>
            会話の設定
            <span className="settings-target">{title}</span>
          </h1>
          <button className="back" onClick={onClose} title="会話の設定を閉じて、会話に戻る"><LuArrowLeft /> 会話に戻る</button>
        </div>
        {errors.map(e => <div key={e.id} className="msg error">{e.text}</div>)}

        <TitleField title={title} onRename={onRename} />
        <label title="会話の ID(UUIDv7)。ログの「会話 …」や DB(conversations の id)と同じ値で、調べるときの手がかりになる">ID</label>
        <div className="conv-id">
          <code>{id}</code>
          <CopyButton text={id} />
        </div>

        <label title="この会話を作ったプロファイル。固定した項目と使えないツールは、このプロファイルの決まりで決まる">プロファイル</label>
        <div className="conv-profile">{profile ? profile.name : "(プロファイルを持たない会話)"}</div>
        {isBrowser && (
          <>
            <label title="この会話のツールが読み書きする aibow-server の資料のフォルダ。管理者が配ったプロファイルで決まる">資料のフォルダ</label>
            <div className="conv-profile">{folder || "(なし)"}</div>
          </>
        )}
        {!isBrowser && worktree && <WorktreeNameField state={worktree} onRename={onRenameWorktree} />}
        {me.login && <ShareField share={share} shareWith={shareWith} shareGroups={shareGroups} onShare={onShare} />}

        <ConversationSettingsForm settings={settings} models={models} lock={lock} onChange={onChange} />
        <LimitsForm history={settings.history_tokens} reply={settings.reply_tokens} disabled={limitsDisabled} lock={lock} onApply={onApplyLimits} />
        <SaveToProfile profile={profile} onSave={onSaveToProfile} onSaveAs={onSaveAsProfile} />

        <h2 className="section" title="この会話で使うツール(入力欄の上の「この会話のツール」と同じ)">ツール</h2>
        <ConversationTools id={id} settings={settings} workDir={workDir} servers={servers} profile={profile} onChange={onChange} />


        <h2 className="section">モデルに渡した文字列{lastPromptFormat && <span className="prompt-format">(書式: {lastPromptFormat})</span>}</h2>
        <p className="hint">
          モデルは会話を覚えていません。送信のたびに、これまでの会話をモデルの書式の1本の文字列にして読ませています
          (会話の上限を超えたら、compact で収めたもの。要約があれば system の後ろに入ります)。
          最後の「返事の書き出し」の続きを書かせることで返事になります。
        </p>
        <pre className="last-prompt">{lastPrompt || "(まだ送信していません)"}</pre>
      </div>
    </section>
  );
}

// ShareField は会話の共有(0018 の決定事項 24)。範囲と相手は人を選ぶ部品で選び、確定してから保存する(0031 の決定事項 6〜13)。
// 非公開から共有にするときは、ツールの結果も見えることを確かめの文に出す(0018 の決定事項 12)。
function ShareField({ share, shareWith, shareGroups, onShare }: { share: string; shareWith: string[]; shareGroups: string[]; onShare(share: string, users: string[], groups: string[]): void }) {
  const [dir] = useDirectory();
  return (
    <SharePicker
      id="conv-share"
      label="共有"
      labelTitle="共有した会話は、相手の一覧の「共有された会話」に出て、読むだけで開ける。相手は写して続けられる"
      words={CONVERSATION_WORDS}
      share={share}
      users={shareWith}
      groups={shareGroups}
      dir={dir}
      self={me.name}
      notes={next => (share === "" && next.share !== "" ? ["共有すると、この会話のツールの結果(ファイルの中身・コマンドの出力)も相手に見えます。"] : [])}
      onCommit={onShare}
    />
  );
}

// SaveToProfile は「プロファイルに保存」: この会話の設定で、プロファイルの値を上書きする / 新しいプロファイルとして保存する。
// 上書きすると、そのプロファイルから新しく作る会話が変わる(今ある会話には効かない)。決まりは変えない
function SaveToProfile({ profile, onSave, onSaveAs }: { profile: Profile | null; onSave(): void; onSaveAs(name: string): void }) {
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState("");
  const submit = () => {
    const t = name.trim();
    if (t === "") return;
    onSaveAs(t);
    setNaming(false);
    setName("");
  };
  return (
    <div className="save-profile">
      {profile && (
        <button type="button" onClick={onSave} title={`この会話の設定(モデル・使うツールのグループも)で、プロファイル「${profile.name}」の値を上書きする`}>
          プロファイル「{profile.name}」に保存
        </button>
      )}
      {naming ? (
        <span className="save-profile-name">
          <input
            type="text"
            autoFocus
            value={name}
            placeholder="新しいプロファイルの名前"
            onChange={e => setName(e.target.value)}
            onKeyDown={e => {
              if (e.key === "Enter") submit();
              if (e.key === "Escape") setNaming(false);
            }}
          />
          <button type="button" disabled={name.trim() === ""} onClick={submit}>保存</button>
          <button type="button" onClick={() => setNaming(false)}>やめる</button>
        </span>
      ) : (
        <button type="button" onClick={() => setNaming(true)} title="この会話の設定で、新しいプロファイルを作る(決まりは無し)">
          新しいプロファイルとして保存
        </button>
      )}
    </div>
  );
}
