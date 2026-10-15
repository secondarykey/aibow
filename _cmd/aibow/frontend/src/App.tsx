// App.tsx: 画面全体。左に会話の一覧(ウィンドウの上から下まで。上のハンバーガーで開閉)、
// 右に「タイトルバー(Frameless のため自前。開いている会話の名前)+ チャット(下に入力欄とモデルの選択)」。
// 会話を開いていないとき(起動時・「+ 新規」)は、チャットの代わりに新しい会話の画面(入力欄が真ん中)を出す。
// 設定は別の画面として扱い、一覧の下の「設定」で開くと、チャットの代わりに会話の場所に出す(SettingsView。新しい会話の既定・アプリ全体)。
// 入力欄の下(メーターの右)の歯車で開く会話の設定(ConversationSettingsView。その会話だけの設定)も同じく会話の場所に出す。
// どちらも「会話に戻る」・一覧で会話を選ぶ・「+ 新規」で閉じる。
// 一覧と会話の間の仕切り(Splitter)をドラッグすると、一覧の幅が変わる。
// ウィンドウが狭い(720px 以下)ときは、一覧を開くと会話を押しのけず、会話の上に重ねて出す。
// 狭くなったときに一覧が開いていたら自動で閉じる(会話の場所が狭くなりすぎないように)。
//
//	useTranscript    … 画面に並べる表示(吹き出し・エラー)
//	useToasts        … 右上に出して数秒で消えるお知らせ(モデルの読み込みなど)
//	useModels        … モデルの一覧と切り替え(Go の ChatService.Models / SwitchModel)
//	useConversations … 会話の一覧・開いている会話・会話ごとの設定
//	useChat          … 送信・今すぐ要約・停止(Go の ChatService.Send / Compact と chat イベント)
//	useAppSettings   … アプリの設定(GPU の層数)と新しい会話の既定(会話の上限・返事の上限も入る)
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
import { ConversationSettingsView } from "./components/ConversationSettingsView";
import { Composer } from "./components/Composer";
import { WorkDirBar } from "./components/WorkDirBar";
import { ToolBar } from "./components/ToolBar";
import { LuSettings } from "react-icons/lu";
import { ChatService } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { ServerInfo } from "../bindings/github.com/secondarykey/aibow/mcp/client";
import type { ConversationSettings } from "../bindings/github.com/secondarykey/aibow";
import { ImeFocusRelay } from "./imeFocus";
import { errorMessage } from "./errors";
import { NARROW_QUERY, useMediaQuery } from "./mediaQuery";
import { STORED_LAST_CONVERSATION, STORED_LIST_OPEN, loadStored, saveStored } from "./stored";

export default function App() {
  // 生成とモデルの読み込みは同時にできない(Go 側のコンテキストが1つしかないため)
  const [busy, setBusy] = useState<Busy>("idle");
  // 設定の画面を開いているか。開いている間は、チャット(か新しい会話の画面)の代わりに設定を出す
  const [settingsOpen, setSettingsOpen] = useState(false);
  // 会話の設定の画面を開いているか。開いている会話の設定を出す
  const [convSettingsOpen, setConvSettingsOpen] = useState(false);
  // 設定の画面(か会話の設定の画面)を開いたときの、表示の最後の id。これより後に出たエラー(設定の適用の失敗など)はその画面にも出す
  const [settingsFrom, setSettingsFrom] = useState(0);
  // 入力欄に打っている文。設定の画面を開くと入力欄は消えるので、戻ったときに続きを打てるようにここで持つ
  const [text, setText] = useState("");
  const narrow = useMediaQuery(NARROW_QUERY);
  // 一覧を開いているか。広いときは前回の起動で自分で開け閉めした状態から始める(stored.ts)。狭いときは閉じて始める
  const [listOpen, setListOpen] = useState(
    () => !window.matchMedia(NARROW_QUERY).matches && loadStored(STORED_LIST_OPEN) !== "false",
  );
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

  // 起動時: 最後に開いていた会話(無ければ新しい会話の画面)に移ってから、モデルの一覧を取る。
  // 画面を移ると表示が作り直されるので、「読み込み中…」のメモが消えないよう、この順にする。
  // StrictMode では effect が2回呼ばれるので、1回だけにする(2回だと会話が2つ作られることがある)
  const started = useRef(false);
  const restored = useRef(false); // 起動時に最後の会話を開き終えたか(開いている会話を覚え始める合図)
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    (async () => {
      const initErr = await convs.init(loadStored(STORED_LAST_CONVERSATION)).then(() => null, (err: unknown) => err);
      restored.current = true;
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
  // (新しい会話の既定を変えても今の会話には効かないので、見るのは作業領域の設定だけ)
  useEffect(() => {
    convs.refreshUsage().catch(() => {});
  }, [models.current?.id, models.current?.n_ctx, app.settings?.workspace, convs.refreshUsage]);

  // 返事を書き終えたら、モデルの情報を取り直す(送信の前に作業領域を作り直していることがあるため。モデルの選択の吹き出しの「作業領域」)
  const prevBusy = useRef(busy);
  useEffect(() => {
    if (prevBusy.current === "generating" && busy === "idle") models.reload().catch(() => {});
    prevBusy.current = busy;
  }, [busy, models.reload]);

  // ウィンドウに戻ってきたら一覧を作り直す(aibow を起動したまま ollama pull したときのため)
  useEffect(() => {
    const onFocus = () => {
      if (busy === "idle") models.reload().catch(() => {});
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [busy, models.reload]);

  const currentId = convs.current?.conversation.id ?? null;
  // 開いている会話を覚えておき、次の起動で開く(新しい会話の画面なら消す)。
  // 起動して最後の会話を開くまで(restored)は、まだ何も開いていない(null)ので覚え直さない
  useEffect(() => {
    if (restored.current) saveStored(STORED_LAST_CONVERSATION, currentId);
  }, [currentId]);
  const unavailable = busy === "unavailable";
  // 開いている会話が無くなった(削除して新しい会話の画面に戻った)ら、会話の設定の画面も閉じる
  useEffect(() => {
    if (currentId === null) setConvSettingsOpen(false);
  }, [currentId]);

  // 名前を変えている会話。一覧のダブルクリックか F2 で入力欄になる。
  // 狭いときに重ねて出す一覧は、1回目のクリックで会話を開いて閉じてしまい、ダブルクリックが届かない。そこで F2 でも入れるようにし、
  // 一覧の外(入力欄など)で押しても効くよう、状態は一覧ではなくここで持つ(狭いときは一覧が2つ出ているため)
  const [renamingId, setRenamingId] = useState<string | null>(null);
  // F2 のために一覧を開いたか。開いたのなら、名前を決めたら閉じて元に戻す
  const openedForRename = useRef(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "F2" || e.isComposing || currentId === null || unavailable || renamingId !== null) return;
      e.preventDefault();
      if (!listOpen) {
        openedForRename.current = true;
        setListOpen(true);
      }
      setRenamingId(currentId);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [currentId, unavailable, renamingId, listOpen]);
  const endRename = () => {
    setRenamingId(null);
    if (openedForRename.current) {
      openedForRename.current = false;
      setListOpen(false);
    }
  };

  // 「+ 新規」: 新しい会話の画面に移る。会話そのものは、そこで最初に送信したときに作る
  const home = convs.current === null;
  const onNew = () => {
    closeOverlay();
    setSettingsOpen(false); // 設定の画面を開いていたら閉じて、新しい会話の画面に戻る
    setConvSettingsOpen(false);
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
      <ModelBar models={models} disabled={busy !== "idle"} size={settings ? settings.history_tokens + settings.reply_tokens : undefined}>
        {convs.current && (
          // summary の会話では、メーターのトークン数を押すと今すぐ要約する(最近の発言は会話の上限の半分まで残す)
          <>
            <Meter
              usage={convs.current.usage}
              onCompact={convs.current.conversation.settings.compact === "summary" ? chat.compact : undefined}
              compactDisabled={busy !== "idle"}
            />
            {/* 会話の設定。会話の一覧を畳んでいても開けるよう、一覧ではなくここに置く(2026-10-15、ユーザーの指示) */}
            <button className="icon-btn conv-settings" title="会話の設定" disabled={unavailable} onClick={() => openConvSettings()}>
              <LuSettings />
            </button>
          </>
        )}
      </ModelBar>
    </>
  );

  // タイトルバーに出す名前は一覧から取る(名前の変更や最初の送信で付いたタイトルが、すぐ反映されるように)
  const convTitle = convs.list.find(c => c.id === currentId)?.title || "新しい会話";
  const showConvSettings = convSettingsOpen && convs.current !== null;
  const title = settingsOpen ? "設定" : showConvSettings ? "会話の設定" : convTitle;

  const markSettingsFrom = () => setSettingsFrom(transcript.items.reduce((m, it) => Math.max(m, it.id), 0));
  const toggleSettings = () => {
    closeOverlay();
    if (!settingsOpen) markSettingsFrom();
    setConvSettingsOpen(false); // 会話の設定の画面からは、設定の画面に切り替える
    setSettingsOpen(o => !o);
  };
  // メーターの右の歯車: 開いている会話の設定の画面を開く(閉じるのは「会話に戻る」など。開いている間は入力欄ごと隠れる)
  const openConvSettings = () => {
    closeOverlay();
    setSettingsOpen(false);
    markSettingsFrom();
    setConvSettingsOpen(true);
  };
  // 一覧で会話を選んだら、設定の画面を閉じてその会話を出す
  const onOpen = (id: string) => {
    closeOverlay();
    setSettingsOpen(false);
    setConvSettingsOpen(false);
    convs.open(id).then(focusInput, err => addError(errorMessage(err)));
  };
  // 新しい会話の既定を変えたら、新しい会話の画面でこれから作る会話の設定(既定の写し)にも入れる
  const onDefaultsChange = (patch: Partial<ConversationSettings>) => {
    app.updateDefaults(patch);
    if (home) convs.updateSettings(patch);
  };
  // 会話の上限・返事の上限の「適用」: 作業領域をその大きさで作り直してみて、確保できたら save で保存する。
  // VRAM に入らないことを、送信したときではなく変えたときに分かるようにするため(0009)
  const applyLimits = async (history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void) => {
    setBusy("loading");
    try {
      const changed = await ChatService.TryLimits(history, reply);
      save({ history_tokens: history, reply_tokens: reply });
      const size = (history + reply).toLocaleString();
      toasts.notify(changed
        ? `会話の上限 ${history.toLocaleString()} + 返事の上限 ${reply.toLocaleString()} = 作業領域 ${size} トークンに作り直しました(モデルは読み直していません)`
        : `会話の上限 ${history.toLocaleString()} + 返事の上限 ${reply.toLocaleString()} にしました(作業領域 ${size} トークン)`);
    } catch (err) {
      addError(errorMessage(err)); // Go 側で元の大きさに戻している。設定は保存しない
    }
    await models.reload().catch(() => {});
    setBusy("idle");
  };
  const settingsErrors = transcript.items.filter(it => it.kind === "error" && it.id > settingsFrom);

  // 会話の場所に出すもの: 設定の画面 / 新しい会話の画面 / チャット
  let content;
  const viewErrors = settingsErrors.map(it => ({ id: it.id, text: it.kind === "error" ? it.text : "" }));
  if (settingsOpen) {
    content = (
      <SettingsView
        errors={viewErrors}
        onClose={() => setSettingsOpen(false)}
        app={app.settings}
        onDefaultsChange={onDefaultsChange}
        onApplyDefaultLimits={(h, r) => applyLimits(h, r, onDefaultsChange)}
        appDisabled={busy !== "idle"}
        onApplyApp={app.apply}
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
  } else if (showConvSettings && convs.current) {
    const current = convs.current.conversation.settings;
    content = (
      <ConversationSettingsView
        title={convTitle}
        settings={current}
        errors={viewErrors}
        onClose={() => {
          setConvSettingsOpen(false);
          focusInput();
        }}
        onChange={convs.updateSettings}
        onMakeDefault={() => app.setDefaults(current)}
        lastPrompt={chat.lastPrompt}
        servers={mcpServers}
        limitsDisabled={busy !== "idle"}
        onApplyLimits={(h, r) => applyLimits(h, r, convs.updateSettings)}
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
          retry={{ disabled: busy !== "idle", onRegenerate: chat.regenerate, onResend: chat.resend }}
          exclude={{
            disabled: unavailable || (chat.generatingId !== null && chat.generatingId === currentId),
            // 吹き出しを「モデルに渡さない」にする / 戻す(0011)。Go が吹き出し1つ分の発言に印を付け、メーターを数え直す
            onChange: (msgId, excluded) => {
              if (!currentId) return;
              ChatService.SetExcluded(currentId, msgId, excluded).then(
                () => {
                  transcript.setExcluded(msgId, excluded);
                  convs.refreshUsage().catch(err => addError(errorMessage(err)));
                },
                err => addError(errorMessage(err)),
              );
            },
          }}
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
    renamingId,
    onStartRename: setRenamingId,
    onEndRename: endRename,
    settingsOpen,
    onToggleSettings: toggleSettings,
    onToggleOpen: () => {
      // 自分で開け閉めしたときだけ覚える(狭くなって自動で閉じた・F2 のために開いた、は覚えない)。
      // 狭いときに重ねて出す一覧は、そのときだけのものなので覚えない
      if (!narrow) saveStored(STORED_LIST_OPEN, String(!listOpen));
      setListOpen(!listOpen);
    },
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
