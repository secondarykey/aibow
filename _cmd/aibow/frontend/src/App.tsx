// App.tsx: 画面全体。状態を持つのは3つのフック。
//
//	useTranscript … 画面に並べる表示(吹き出し・メモ・エラー)
//	useModels     … モデルの一覧と切り替え(Go の ChatService.Models / SwitchModel)
//	useChat       … 送信と停止、会話履歴(Go の ChatService.Chat と chat イベント)
//
// Go のメソッドは、wails3 generate bindings が作った frontend/bindings の関数として呼ぶ。
import { useEffect, useState } from "react";
import { useTranscript } from "./transcript";
import { useModels } from "./useModels";
import type { Busy } from "./useModels";
import { useChat } from "./useChat";
import type { ChatSettings } from "./useChat";
import { Header } from "./components/Header";
import { ChatView } from "./components/ChatView";
import { SettingsPanel } from "./components/SettingsPanel";
import { Composer } from "./components/Composer";
import { ImeFocusRelay } from "./imeFocus";

const defaultSettings: ChatSettings = {
  system: "あなたは親切なアシスタントです。日本語で簡潔に答えてください。",
  think: true,
  temperature: 0.7,
};

export default function App() {
  // 生成とモデルの読み込みは同時にできない(Go 側のコンテキストが1つしかないため)
  const [busy, setBusy] = useState<Busy>("idle");
  const [settings, setSettings] = useState(defaultSettings);

  const transcript = useTranscript();
  const models = useModels(transcript, setBusy);
  const chat = useChat(transcript, setBusy, models.current, settings);

  // ウィンドウに戻ってきたら一覧を作り直す(aibow を起動したまま ollama pull したときのため)
  useEffect(() => {
    const onFocus = () => {
      if (busy === "idle") models.reload().catch(() => {});
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [busy, models.reload]);

  return (
    <>
      <Header models={models} disabled={busy !== "idle"} onClear={chat.clear} />
      <main>
        <ChatView items={transcript.items} />
        <SettingsPanel settings={settings} onChange={setSettings} lastPrompt={chat.lastPrompt} />
      </main>
      <Composer
        canSend={busy === "idle"}
        canStop={busy === "generating"}
        onSend={chat.send}
        onStop={chat.stop}
      />
      <ImeFocusRelay />
    </>
  );
}
