// App.tsx: 画面全体。左に会話の一覧(ウィンドウの上から下まで。上のハンバーガーで開閉)、
// 右に「タイトルバー(Frameless のため自前。開いている会話の名前)+ チャット(下に入力欄とモデルの選択)」。
// 右の設定パネルは一覧の下の「設定」で開閉する。
//
//	useTranscript    … 画面に並べる表示(吹き出し・メモ・エラー)
//	useModels        … モデルの一覧と切り替え(Go の ChatService.Models / SwitchModel)
//	useConversations … 会話の一覧・開いている会話・会話ごとの設定
//	useChat          … 送信と停止(Go の ChatService.Send と chat イベント)
//	useAppSettings   … アプリ全体の設定(コンテキスト長・GPU の層数・新しい会話の既定)
//
// 会話の履歴と設定は Go が SQLite に保存している。画面は表示と入力に専念する。
// Go のメソッドは、wails3 generate bindings が作った frontend/bindings の関数として呼ぶ。
import { useEffect, useRef, useState } from "react";
import { useTranscript } from "./transcript";
import { useModels } from "./useModels";
import type { Busy } from "./useModels";
import { useConversations } from "./useConversations";
import { useChat } from "./useChat";
import { useAppSettings } from "./useAppSettings";
import { ModelBar } from "./components/ModelBar";
import { TitleBar } from "./components/TitleBar";
import { ConversationList } from "./components/ConversationList";
import { ChatView } from "./components/ChatView";
import { SettingsPanel } from "./components/SettingsPanel";
import { Composer } from "./components/Composer";
import { ImeFocusRelay } from "./imeFocus";
import { errorMessage } from "./errors";

export default function App() {
  // 生成とモデルの読み込みは同時にできない(Go 側のコンテキストが1つしかないため)
  const [busy, setBusy] = useState<Busy>("idle");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [listOpen, setListOpen] = useState(true);

  const transcript = useTranscript();
  const { addError } = transcript;
  const models = useModels(transcript, setBusy);
  const convs = useConversations(addError);
  const chat = useChat(transcript, setBusy, convs);
  const app = useAppSettings(transcript, setBusy, models.reload);

  // 起動時: 一番新しい会話を開いてから、モデルの一覧を取る。
  // 会話を開くと表示が作り直されるので、「読み込み中…」のメモが消えないよう、この順にする。
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

  // 「+ 新規」: 開いている会話がまだ空なら、新しく作らずにそれを使う(空の会話が一覧に並ばないように)
  const onNew = () => {
    if (!transcript.items.some(it => it.kind === "user")) return;
    convs.create().catch(err => addError(errorMessage(err)));
  };

  // 送信できない理由。別の会話で生成中なら、会話を読むことはできるが送信はできない
  const blockedReason =
    busy === "generating" && chat.generatingId !== currentId ? "別の会話で返事を生成中です"
      : busy === "generating" ? "返事を生成中です"
        : busy === "loading" ? "モデルを読み込み中です"
          : undefined;

  // タイトルバーに出す名前は一覧から取る(名前の変更や最初の送信で付いたタイトルが、すぐ反映されるように)
  const title = convs.list.find(c => c.id === currentId)?.title || "新しい会話";

  return (
    <>
      <ConversationList
        list={convs.list}
        currentId={currentId}
        generatingId={chat.generatingId}
        disabled={unavailable}
        onOpen={id => convs.open(id).catch(err => addError(errorMessage(err)))}
        onNew={onNew}
        onDelete={id => convs.remove(id).catch(err => addError(errorMessage(err)))}
        onRename={(id, title) => convs.rename(id, title).catch(err => addError(errorMessage(err)))}
        settingsOpen={settingsOpen}
        onToggleSettings={() => setSettingsOpen(o => !o)}
        open={listOpen}
        onToggleOpen={() => setListOpen(o => !o)}
      />
      <div className="workspace">
        <TitleBar title={title} />
        <main>
          <section className="center">
            <ChatView items={transcript.items} onStop={chat.stop} />
            <Composer
              canSend={busy === "idle" && currentId !== null}
              blockedReason={blockedReason}
              onSend={chat.send}
            />
            <ModelBar models={models} disabled={busy !== "idle"} />
          </section>
          {settingsOpen && <SettingsPanel
            settings={convs.current?.conversation.settings ?? null}
            onChange={convs.updateSettings}
            onMakeDefault={() => convs.current && app.setDefaults(convs.current.conversation.settings)}
            app={app.settings}
            appDisabled={busy !== "idle"}
            onApplyApp={app.apply}
            lastPrompt={chat.lastPrompt}
          />}
        </main>
      </div>
      <ImeFocusRelay />
    </>
  );
}
