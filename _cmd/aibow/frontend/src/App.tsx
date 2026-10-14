// App.tsx: 画面全体。左に会話の一覧(ウィンドウの上から下まで。上のハンバーガーで開閉)、
// 右に「タイトルバー(Frameless のため自前。開いている会話の名前)+ チャット(下に入力欄とモデルの選択)」。
// 会話を開いていないとき(起動時・「+ 新規」)は、チャットの代わりに新しい会話の画面(入力欄が真ん中)を出す。
// 設定は別の画面として扱い、一覧の下の「設定」で開くと、チャットの代わりに会話の場所に出す(SettingsView)。
// 「会話に戻る」・一覧で会話を選ぶ・「+ 新規」で閉じる。
// 一覧と会話の間の仕切り(Splitter)をドラッグすると、一覧の幅が変わる。
// ウィンドウが狭い(640px 以下)ときは、一覧を開くと会話を押しのけず、会話の上に重ねて出す。
// 狭くなったときに一覧が開いていたら自動で閉じる(会話の場所が狭くなりすぎないように)。
//
//	useTranscript    … 画面に並べる表示(吹き出し・エラー)
//	useToasts        … 右上に出して数秒で消えるお知らせ(モデルの読み込みなど)
//	useModels        … モデルの一覧と切り替え(Go の ChatService.Models / SwitchModel)
//	useConversations … 会話の一覧・開いている会話・会話ごとの設定
//	useChat          … 送信・今すぐ要約・停止(Go の ChatService.Send / Compact と chat イベント)
//	useAppSettings   … アプリ全体の設定(会話の上限・返事の上限・GPU の層数・新しい会話の既定)
//
// 会話の履歴と設定は Go が SQLite に保存している。画面は表示と入力に専念する。
// Go のメソッドは、wails3 generate bindings が作った frontend/bindings の関数として呼ぶ。
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranscript } from "./transcript";
import { Toasts, useToasts } from "./toast";
import { useModels } from "./useModels";
import type { Busy } from "./useModels";
import { useConversations } from "./useConversations";
import { useChat } from "./useChat";
import { useAppSettings } from "./useAppSettings";
import { ModelBar } from "./components/ModelBar";
import { Meter } from "./components/Meter";
import { TitleBar } from "./components/TitleBar";
import { ConversationList } from "./components/ConversationList";
import { Splitter, useListWidth } from "./components/Splitter";
import { ChatView } from "./components/ChatView";
import { NewChat } from "./components/NewChat";
import { SettingsView } from "./components/SettingsView";
import { Composer } from "./components/Composer";
import { WorkDirBar } from "./components/WorkDirBar";
import { ToolBar } from "./components/ToolBar";
import { ChatService } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { ServerInfo } from "../bindings/github.com/secondarykey/aibow/mcp/client";
import { ImeFocusRelay } from "./imeFocus";
import { errorMessage } from "./errors";
import { NARROW_QUERY, useMediaQuery } from "./mediaQuery";

export default function App() {
  // 生成とモデルの読み込みは同時にできない(Go 側のコンテキストが1つしかないため)
  const [busy, setBusy] = useState<Busy>("idle");
  // 設定の画面を開いているか。開いている間は、チャット(か新しい会話の画面)の代わりに設定を出す
  const [settingsOpen, setSettingsOpen] = useState(false);
  // 設定の画面を開いたときの、表示の最後の id。これより後に出たエラー(設定の適用の失敗など)は設定の画面にも出す
  const [settingsFrom, setSettingsFrom] = useState(0);
  // 入力欄に打っている文。設定の画面を開くと入力欄は消えるので、戻ったときに続きを打てるようにここで持つ
  const [text, setText] = useState("");
  const narrow = useMediaQuery(NARROW_QUERY);
  const [listOpen, setListOpen] = useState(() => !window.matchMedia(NARROW_QUERY).matches);
  // 狭くなったら一覧を閉じる。狭いまま開いたときは、閉じずに会話の上に重ねて出す(overlay)
  useEffect(() => {
    if (narrow) setListOpen(false);
  }, [narrow]);
  const overlay = narrow && listOpen;
  // 重ねて出している一覧は、会話を選ぶ・「+ 新規」・「設定」を押したら閉じる(選んだものが見えるように)
  const closeOverlay = () => {
    if (overlay) setListOpen(false);
  };
  useEffect(() => {
    if (!overlay) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setListOpen(false);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [overlay]);
  const listWidth = useListWidth(); // 会話の一覧の幅(一覧の右の仕切りで変える)
  // 入力欄にフォーカスを移す合図。会話を開いた・「+ 新規」を押したあとに進める(Composer の focusKey)
  const [focusKey, setFocusKey] = useState(0);
  const focusInput = () => setFocusKey(n => n + 1);

  const transcript = useTranscript();
  const toasts = useToasts();
  const { addError } = transcript;
  const models = useModels(toasts, transcript, setBusy);
  const convs = useConversations(addError);
  const chat = useChat(transcript, setBusy, convs, toasts);
  const app = useAppSettings(toasts, transcript, setBusy, models.reload);

  // 起動時: 新しい会話の画面に移ってから、モデルの一覧を取る。
  // 画面を移ると表示が作り直されるので、「読み込み中…」のメモが消えないよう、この順にする。
  // StrictMode では effect が2回呼ばれるので、1回だけにする(2回だと会話が2つ作られることがある)
  const started = useRef(false);
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    (async () => {
      const initErr = await convs.init().then(() => null, (err: unknown) => err);
      const ok = await models.reload().catch(err => {
        addError(`モデル一覧を取得できません: ${errorMessage(err)}`);
        return false;
      });
      if (!ok) return; // 起動に失敗している。理由は useModels が出している
      if (initErr) addError(`会話を読み込めません: ${errorMessage(initErr)}`);
      await app.reload().catch(err => addError(`設定を読み込めません: ${errorMessage(err)}`));
    })();
  }, [convs.init, models.reload, app.reload, addError]);

  // 会話で選べる MCP のサーバー(settings.json に書いたもの全部)。起動したときと、設定の画面の「ツール」を変えたときに読む
  const [mcpServers, setMcpServers] = useState<ServerInfo[]>([]);
  const reloadMcp = useCallback(() => {
    ChatService.MCPServers().then(
      s => setMcpServers(s ?? []),
      err => addError(`MCP のサーバーの一覧を取得できません: ${errorMessage(err)}`),
    );
  }, [addError]);
  useEffect(reloadMcp, [reloadMcp]);

  // モデルや上限が変わると、同じ会話でもトークン数(メーター)が変わるので数え直す
  useEffect(() => {
    convs.refreshUsage().catch(() => {});
  }, [models.current?.id, models.current?.n_ctx, app.settings, convs.refreshUsage]);

  // ウィンドウに戻ってきたら一覧を作り直す(aibow を起動したまま ollama pull したときのため)
  useEffect(() => {
    const onFocus = () => {
      if (busy === "idle") models.reload().catch(() => {});
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [busy, models.reload]);

  const currentId = convs.current?.conversation.id ?? null;
  const unavailable = busy === "unavailable";

  // 「+ 新規」: 新しい会話の画面に移る。会話そのものは、そこで最初に送信したときに作る
  const home = convs.current === null;
  const onNew = () => {
    closeOverlay();
    setSettingsOpen(false); // 設定の画面を開いていたら閉じて、新しい会話の画面に戻る
    if (home) {
      focusInput(); // すでに新しい会話の画面なら、入力欄に戻すだけ
      return;
    }
    convs.goHome().then(focusInput, err => addError(errorMessage(err)));
  };

  // 送信できない理由。別の会話で生成中なら、会話を読むことはできるが送信はできない
  const blockedReason =
    busy === "generating" && chat.generatingId !== currentId ? "別の会話で返事を生成中です"
      : busy === "generating" ? "返事を生成中です"
        : busy === "loading" ? "モデルを読み込み中です"
          : undefined;

  // 入力欄とモデルの選択。チャットの画面では下に、新しい会話の画面では真ん中に出す
  const canSend = busy === "idle" && (convs.current !== null || convs.draft !== null);
  // 開いている会話の設定(新しい会話の画面では、これから作る会話の設定 = 既定の写し)
  const settings = convs.current?.conversation.settings ?? convs.draft;
  const input = (
    <>
      <Composer
        text={text}
        onTextChange={setText}
        canSend={canSend}
        blockedReason={blockedReason}
        home={home}
        focusKey={focusKey}
        onSend={chat.send}
        top={settings && (
          // 入力欄の上: 作業フォルダと、この会話のツール(どちらも会話ごとの設定)
          <div className="composer-top">
            <WorkDirBar
              workDir={settings.work_dir}
              onChange={workDir => {
                convs.updateSettings({ work_dir: workDir });
                focusInput();
              }}
              onError={addError}
            />
            <ToolBar
              builtinOn={!settings.builtin_off}
              servers={mcpServers}
              selected={settings.mcp_servers ?? []}
              workDir={settings.work_dir}
              onBuiltinChange={on => convs.updateSettings({ builtin_off: !on })}
              onServersChange={mcp_servers => convs.updateSettings({ mcp_servers })}
            />
          </div>
        )}
      />
      <ModelBar models={models} disabled={busy !== "idle"}>
        {convs.current && (
          // summary の会話では、メーターのトークン数を押すと今すぐ要約する(最近の発言は会話の上限の半分まで残す)
          <Meter
            usage={convs.current.usage}
            onCompact={convs.current.conversation.settings.compact === "summary" ? chat.compact : undefined}
            compactDisabled={busy !== "idle"}
          />
        )}
      </ModelBar>
    </>
  );

  // タイトルバーに出す名前は一覧から取る(名前の変更や最初の送信で付いたタイトルが、すぐ反映されるように)
  const convTitle = convs.list.find(c => c.id === currentId)?.title || "新しい会話";
  const title = settingsOpen ? "設定" : convTitle;

  const toggleSettings = () => {
    closeOverlay();
    if (!settingsOpen) setSettingsFrom(transcript.items.reduce((m, it) => Math.max(m, it.id), 0));
    setSettingsOpen(o => !o);
  };
  // 一覧で会話を選んだら、設定の画面を閉じてその会話を出す
  const onOpen = (id: string) => {
    closeOverlay();
    setSettingsOpen(false);
    convs.open(id).then(focusInput, err => addError(errorMessage(err)));
  };
  const settingsErrors = transcript.items.filter(it => it.kind === "error" && it.id > settingsFrom);

  // 会話の場所に出すもの: 設定の画面 / 新しい会話の画面 / チャット
  let content;
  if (settingsOpen) {
    content = (
      <SettingsView
        settings={settings}
        conversationTitle={home ? null : convTitle}
        errors={settingsErrors.map(it => ({ id: it.id, text: it.kind === "error" ? it.text : "" }))}
        onClose={() => setSettingsOpen(false)}
        onChange={convs.updateSettings}
        onMakeDefault={() => settings && app.setDefaults(settings)}
        app={app.settings}
        appDisabled={busy !== "idle"}
        onApplyApp={app.apply}
        lastPrompt={chat.lastPrompt}
        onToolsChanged={() => {
          reloadMcp();
          convs.refreshUsage().catch(err => addError(errorMessage(err)));
          // 新しい会話の画面なら、これから作る会話のツールを「ツール」に合わせ直す(「ツール」のチェックが最初から使うもの)
          if (!convs.current && convs.draft) {
            ChatService.AppSettings().then(
              a => convs.updateSettings({ mcp_servers: a.defaults.mcp_servers, builtin_off: a.defaults.builtin_off }),
              err => addError(errorMessage(err)),
            );
          }
        }}
      />
    );
  } else if (home) {
    content = <NewChat items={transcript.items}>{input}</NewChat>;
  } else {
    content = (
      <section className="center">
        <ChatView
          items={transcript.items}
          firstId={convs.current?.usage?.first_id ?? null}
          conversationId={currentId}
          onStop={chat.stop}
          onAnswer={chat.answer}
        />
        {input}
      </section>
    );
  }

  const listProps = {
    list: convs.list,
    currentId,
    generatingId: chat.generatingId,
    waitingId: chat.confirmingId,
    disabled: unavailable,
    onOpen,
    onNew,
    onDelete: (id: string) => convs.remove(id).then(focusInput, err => addError(errorMessage(err))),
    onRename: (id: string, title: string) => convs.rename(id, title).catch(err => addError(errorMessage(err))),
    settingsOpen,
    onToggleSettings: toggleSettings,
    onToggleOpen: () => setListOpen(o => !o),
    width: listWidth.width,
  };

  return (
    <>
      {/* 狭いときは、ここ(会話の横)はいつも畳んだ細い列で、開いた一覧は下の overlay に重ねて出す */}
      <ConversationList {...listProps} open={listOpen && !narrow} />
      {listOpen && !narrow && (
        <Splitter width={listWidth.width} onResize={listWidth.set} onCommit={listWidth.commit} onReset={listWidth.reset} />
      )}
      <div className="workspace">
        <TitleBar title={title} />
        <main>{content}</main>
      </div>
      {overlay && (
        // 後ろの影を押したら閉じる(一覧の中を押したときは閉じない)
        <div className="list-overlay" onMouseDown={e => e.target === e.currentTarget && setListOpen(false)}>
          <ConversationList {...listProps} open />
        </div>
      )}
      <Toasts toasts={toasts.toasts} onDismiss={toasts.dismiss} />
      <ImeFocusRelay />
    </>
  );
}
