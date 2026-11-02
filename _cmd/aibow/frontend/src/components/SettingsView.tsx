// 設定の画面: タブで「基本設定」(画面)・「モデル」(モデルの一覧・置き場所・GPU の層数。0015 の 8-4)・「会話」(プロファイル・ツール)・
// 「サーバー」(aibow-server につなぐ・戻す。デスクトップだけ。0019)・「MCP」(MCP のサーバーを足す・変える・消す。デスクトップだけ。0034)に分ける。
// 会話に関わらないアプリの設定と、会話の既定として使われる値を、同じ画面に混ぜないため(2026-10-19、ユーザーの指示)。
// GPU の層数は、以前は基本設定で読み込み中のモデルの値だけを変えていたが、「モデル」のタブでモデルごとに変えるようにした。
// ツールは、プロファイルの中の1つの一覧で決める(最初から使う・選べば使える・使えないはプロファイル、確認しないはこの PC。0014)。
//
// 会話の一覧の下の「設定」で開き、チャットの代わりに会話の場所に出す(別の画面として扱う)。
// 「会話に戻る」か、一覧で会話を選ぶ・「+ 新規」を押すと閉じる。
// 開いている会話だけの設定は、入力欄の下(メーターの右)の歯車で開く会話の設定の画面(ConversationSettingsView)で変える。
// 新しい会話は、選んだプロファイル(名前の付いた新しい会話の既定)の写しから始まる(_docs/features/0009-conversation-settings.md・0014)。
import { useState } from "react";
import { LuArrowLeft } from "react-icons/lu";
import type { AppSettings, ConversationSettings, DisplaySettings } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { ProfileSettings } from "./ProfileSettings";
import { ModelSettings } from "./ModelSettings";
import { ServerSettings } from "./ServerSettings";
import { McpEditor, type McpEditorAPI } from "./McpEditor";
import { WorkspaceSettings } from "./WorkspaceSettings";
import type { WorkspaceList } from "../../bindings/github.com/secondarykey/aibow/ui";
import { ChatService, isBrowser, me } from "../backend";
import { AccountSettings, signedIn } from "./AccountSettings";
import { LeaderGroups } from "./GroupSettings";
import { useDirectory } from "../directory";
import type { Models } from "../useModels";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../../bindings/github.com/secondarykey/aibow/models/index";

interface Props {
  onClose(): void;                        // 会話に戻る
  errors: { id: number; text: string }[]; // 設定の画面を開いてから出たエラー(ふだんは会話の表示に出るもの)
  app: AppSettings | null;
  models: ModelEntry[];  // 選べるモデル(プロファイルのモデル)
  modelState: Models;    // モデルの一覧・読み込み・降ろす・層の数(「モデル」のタブ)
  servers: ServerInfo[]; // settings.json の MCP のサーバー(プロファイルの使うツールのグループ)
  onProfilesChanged(): void; // プロファイルを変えた(新しい会話の画面の写しを合わせ直す)
  // プロファイルの会話の上限・返事の上限(作業領域を作り直してみてから save で保存する)
  onApplyLimits(history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void): void;
  onError(text: string): void;
  appDisabled: boolean; // 生成中・読み込み中は変えられない(エンジンが1つのため)
  // 「サーバー」のタブだけは、起動に失敗していても操作できる(lib の無い PC から初めてつなぐため。0019)。生成中・読み込み中だけ止める
  connectDisabled: boolean;
  onDisplayChange(d: DisplaySettings): void; // 画面の設定を変える(生成に関わらないので、生成中でも変えられる)
  onToolsChanged(): void; // ツールを使う / 使わないを変えた(メーターと、会話で選べる MCP のサーバーを読み直す)
  onNotice(text: string): void; // お知らせ(トースト)を出す(MCP のサーバーを足した、など)
  initialTab?: Tab; // 開いたときのタブ(左下の名前のメニューの「アカウント」から開くときは account)
  workspaces: WorkspaceList; // その PC のワークスペース(「ワークスペース」のタブ。デスクトップだけ。0035)
  onWorkspacesChanged(): void; // ワークスペースを変えた(入力欄の上の一覧を読み直す)
}

export type Tab = "basic" | "model" | "conversation" | "workspace" | "mcp" | "server" | "groups" | "account";

const TABS: { id: Tab; label: string; title: string }[] = [
  { id: "basic", label: "基本設定", title: "会話によらず、この PC で1つだけのもの(画面)" },
  // ブラウザ(aibow-server)では一覧だけ。読み込む / 降ろす・GPU の層数・置き場所は管理の画面(0032 の決定事項 6)
  { id: "model", label: "モデル", title: isBrowser ? "使えるモデルの一覧" : "モデルの一覧(読み込む / 降ろす・GPU の層数)と置き場所" },
  { id: "conversation", label: "会話", title: "プロファイル(新しい会話の既定・ツールの扱い)" },
  // その PC のフォルダの組(アプリだけ。0035)。会話は入力欄の上でここから選ぶ
  { id: "workspace", label: "ワークスペース", title: "この PC のワークスペース(名前を付けたフォルダの組)を作る・変える・消す" },
  // MCP のサーバーそのものを足す・変える・消す(アプリだけ。0034 の決定事項 2・3)。使う・確認するを決めるのは「会話」のプロファイルの「ツール」
  { id: "mcp", label: "MCP", title: "MCP のサーバーを足す・変える・消す・試しにつなぐ" },
  // aibow-server につなぐ・戻すは、デスクトップのアプリだけ(0019。ブラウザは aibow-server そのものの画面)
  { id: "server", label: "サーバー", title: "aibow-server につなぐ・ログインする・アプリの中で動かす形に戻す" },
  // グループの長だけ(管理者でない人。管理者は「利用者」のタブで扱う。0031 の決定事項 32・37)
  { id: "groups", label: "グループ", title: "自分が長のグループ(メンバーを足す・外す)" },
  // ログインしているとき(ブラウザ・アプリが aibow-server にログインしてつないでいるとき)。自分の名前・表示名・メールアドレス・パスワード・ログアウト(0031 の決定事項 44)
  { id: "account", label: "アカウント", title: "自分の表示名・メールアドレス・パスワード・ログアウト" },
];

// appMcpAPI は、アプリの「MCP」のタブが呼ぶ口(mcp.json。0034)。aibow-server の分を作るときは、ここを替えた口を McpEditor に渡す
const appMcpAPI: McpEditorAPI = {
  list: () => ChatService.MCPServerList(),
  add: servers => ChatService.AddMCPServers(servers),
  update: (name, config) => ChatService.UpdateMCPServer(name, config),
  remove: name => ChatService.DeleteMCPServer(name),
  try: (name, config, add) => ChatService.TryMCPServer(name, config, add),
  parse: text => ChatService.ParseMCPJSON(text),
};

export function SettingsView({ onClose, errors, app, models, modelState, servers, onProfilesChanged, onApplyLimits, onError, appDisabled, connectDisabled, onDisplayChange, onToolsChanged, onNotice, initialTab, workspaces, onWorkspacesChanged }: Props) {
  // 開くたびに「基本設定」から(開いている間だけ覚える)。左下の名前のメニューから開いたときは「アカウント」
  const [tab, setTab] = useState<Tab>(initialTab ?? "basic");
  // グループの長かは、共有の相手の一覧(グループの長も入っている)で決める。ブラウザでログインしているときだけ
  const [dir, reloadDir] = useDirectory();
  const leader = isBrowser && me.login && !me.admin && dir.groups.some(g => g.leaders.includes(me.name));
  return (
    <section className="settings-view">
      <div className="settings-inner">
        <div className="settings-header">
          <h1>設定</h1>
          <button className="back" onClick={onClose} title="設定を閉じて、会話に戻る"><LuArrowLeft /> 会話に戻る</button>
        </div>
        {errors.map(e => <div key={e.id} className="msg error">{e.text}</div>)}

        <div className="settings-tabs" role="tablist">
          {TABS.filter(t => (t.id !== "groups" || leader) && (t.id !== "account" || signedIn()) && (t.id !== "server" || !isBrowser) && (t.id !== "mcp" || !isBrowser) && (t.id !== "workspace" || !isBrowser)).map(t => (
            <button
              key={t.id}
              role="tab"
              aria-selected={tab === t.id}
              className={"settings-tab" + (tab === t.id ? " current" : "")}
              title={t.title}
              onClick={() => setTab(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>

        {tab === "basic" && (
          <>
            <h2 title="画面の見せ方。生成には関わらない">画面</h2>
            {app && <DisplaySettingsForm display={app.display} onChange={onDisplayChange} />}
          </>
        )}

        {/* ブラウザ(aibow-server)では一覧だけ。読み込む / 降ろす・層の数・置き場所・使えるは管理の画面(0032 の決定事項 6)。アプリの中で動かすときは今のまま */}
        {tab === "model" && <ModelSettings models={modelState} listOnly={isBrowser} disabled={appDisabled} onError={onError} />}

        {tab === "groups" && <LeaderGroups dir={dir} reload={reloadDir} onError={onError} />}

        {tab === "account" && <AccountSettings onError={onError} />}

        {tab === "server" && <ServerSettings disabled={connectDisabled} onError={onError} />}

        {tab === "workspace" && <WorkspaceSettings list={workspaces} reload={onWorkspacesChanged} />}

        {tab === "mcp" && <McpEditor api={appMcpAPI} onChanged={onToolsChanged} onNotice={onNotice} />}

        {tab === "conversation" && (
          <>
            <h2 title="名前の付いた新しい会話の既定。新しい会話は、選んだプロファイルの写しから始まる(今ある会話には効かない)。決まり(固定・使えない)は、そのプロファイルから作った会話にすぐ効く">プロファイル</h2>
            <ProfileSettings
              models={models}
              servers={servers}
              disabled={appDisabled}
              onApplyLimits={onApplyLimits}
              onError={onError}
              onChanged={onProfilesChanged}
              onToolsChanged={onToolsChanged}
            />

          </>
        )}
      </div>
    </section>
  );
}

// 画面の設定。会話によらずアプリ全体で1つ。選んだらすぐ効く(「適用」は要らない)
function DisplaySettingsForm({ display, onChange }: { display: DisplaySettings; onChange(d: DisplaySettings): void }) {
  return (
    <>
      <label htmlFor="theme" title="システムなら、OS のライト / ダークの設定に合わせる(OS で切り替えるとすぐ変わる)">明るさ</label>
      <select id="theme" value={display.theme || "system"} onChange={e => onChange({ ...display, theme: e.target.value })}>
        <option value="system">システム(OS の設定に合わせる)</option>
        <option value="light">ライト</option>
        <option value="dark">ダーク</option>
      </select>

      <label className="check">
        <input
          type="checkbox"
          checked={display.pin_question}
          onChange={e => onChange({ ...display, pin_question: e.target.checked })}
        />
        スクロールしたら、質問を右上に出しておく
      </label>
      <p className="hint">長い返事を読んでいて質問が上に隠れたら、その質問を右上に小さく出します。押すと質問まで戻ります。</p>
    </>
  );
}
