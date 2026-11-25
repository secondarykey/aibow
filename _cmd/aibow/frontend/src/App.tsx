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
import { ServerContext } from "./serverContext";
import type { Busy } from "./useModels";
import { NIL, useConversations } from "./useConversations";
import { useChat } from "./useChat";
import { useAttachments } from "./attachments";
import { useAppSettings } from "./useAppSettings";
import { ModelBar } from "./components/ModelBar";
import { Meter } from "./components/Meter";
import { TitleBar } from "./components/TitleBar";
import { ConversationList } from "./components/ConversationList";
import { Splitter, useListWidth } from "./components/Splitter";
import { ChatView } from "./components/ChatView";
import { NewChat } from "./components/NewChat";
import { SettingsView, type Tab as SettingsTab } from "./components/SettingsView";
import { AdminView, canAdmin } from "./components/AdminView";
import { ConversationSettingsView } from "./components/ConversationSettingsView";
import { ReadView } from "./components/ReadView";
import { Composer } from "./components/Composer";
import { FolderBar, WorkspaceBar } from "./components/WorkspaceBar";
import { mainFolder, useWorkspaces } from "./workspaces";
import { ToolBar } from "./components/ToolBar";
import { WorktreeBar } from "./components/WorktreeBar";
import { WorktreeDiff } from "./components/WorktreeDiff";
import { useWorktree } from "./worktree";
import { LuSettings } from "react-icons/lu";
import { ChatService, isBrowser, me, onReconnect, onWindowFocus } from "./backend";
import { showName } from "./directory";
import type { ServerInfo } from "../bindings/github.com/secondarykey/aibow/mcp/client";
import type { ConversationRef, SharedRead } from "../bindings/github.com/secondarykey/aibow";
import { useUnsaved } from "./unsaved";
import { ImeFocusRelay } from "./imeFocus";
import { errorMessage, operationEvents } from "./errors";
import { setTheme } from "./theme";
import { FIX, fixedTitle, grantedFolder, lockOf, locked } from "./profileRules";
import { NARROW_QUERY, useMediaQuery } from "./mediaQuery";
import { STORED_LAST_CONVERSATION, STORED_LIST_OPEN, loadStored, saveStored } from "./stored";

// wideListOpen は、広いときに一覧を開いておくか(ハンバーガーで自分で開け閉めしたとき覚えたもの。覚えていなければ開く)
function wideListOpen(): boolean {
  return loadStored(STORED_LIST_OPEN) !== "false";
}

export default function App() {
  // 生成とモデルの読み込みは同時にできない(Go 側のコンテキストが1つしかないため)
  const [busy, setBusy] = useState<Busy>("idle");
  // 設定の画面を開いているか。開いている間は、チャット(か新しい会話の画面)の代わりに設定を出す
  const [settingsOpen, setSettingsOpenState] = useState(false);
  // 管理の画面(0032)。設定の画面と同じ場所に出す別の画面で、同時には開かない
  const [adminOpen, setAdminOpen] = useState(false);
  // setSettingsOpen は設定の画面を開く / 閉じる。管理の画面は閉じる(会話を選ぶ・「+ 新規」などは、どちらの画面も閉じる。同時には開かない)
  const setSettingsOpen = (open: boolean) => {
    setSettingsOpenState(open);
    setAdminOpen(false);
  };
  // 設定の画面を開いたときのタブ(左下の名前のメニューの「アカウント」から開くと account。開き直すたびに作り直すので key も持つ)
  const [settingsTab, setSettingsTab] = useState<{ tab: SettingsTab; n: number }>({ tab: "basic", n: 0 });
  // 会話の設定の画面を開いているか。開いている会話の設定を出す
  const [convSettingsOpen, setConvSettingsOpen] = useState(false);
  const [diffOpen, setDiffOpen] = useState(false); // worktree の差分の画面(0039)
  // 設定・管理・会話の設定の画面のエラー(「直前の操作の結果」。0043)。会話の表示(transcript)とは別に持つ。
  // 以前は会話の表示に足し、画面には「開いてから出た分」だけを出していたので、画面を閉じると、それまでの失敗が全部
  // 新しい会話の画面に並んだ(A1 の「確かめる」3回・A3 のログイン3回が、「+ 新規」を押しても残っていた。2026-11-20、ユーザーの指摘)。
  // 会話の表示のエラーは「どこで何が起きたか」の記録、この画面のエラーは次の操作で役目を終える状態なので、置き場所を分ける
  const [screenErrors, setScreenErrors] = useState<{ id: number; text: string }[]>([]);
  const screenErrorId = useRef(0);
  const clearScreenErrors = useCallback(() => setScreenErrors(e => (e.length === 0 ? e : [])), []);
  // 今その画面を開いているか(描画のたびに下で更新する)。開いている間に出たエラーは、その画面に出す
  const inOpScope = useRef(false);
  // 入力欄に打っている文。設定の画面を開くと入力欄は消えるので、戻ったときに続きを打てるようにここで持つ
  const [text, setText] = useState("");
  const narrow = useMediaQuery(NARROW_QUERY);
  // 一覧を開いているか。広いときは、広いときに自分で開け閉めした状態(stored.ts。起動をまたいで覚える)にする。狭いときは閉じて始める
  const [listOpen, setListOpen] = useState(
    () => !window.matchMedia(NARROW_QUERY).matches && wideListOpen(),
  );
  // 狭くなったら一覧を閉じる。狭いまま開いたときは、閉じずに会話の上に重ねて出す(overlay)。
  // 広くなったら、広いときに自分で開け閉めした状態に戻す(狭いときに重ねて出していたかどうかは引き継がない)
  useEffect(() => {
    setListOpen(!narrow && wideListOpen());
  }, [narrow]);
  // 設定・管理の画面を開いている間は、広いときも一覧を畳んだ細い列にする(会話の外にいることを、一覧の出方で見せる。0063 の決定事項 2・9)。
  // 覚えている開け閉め(listOpen と STORED_LIST_OPEN)は変えず、渡す値だけを変えるので、閉じて会話に戻ると開く前の開け閉めに戻る。
  // その間にハンバーガーを押したら、そのときだけ一覧を開く(peek。決定事項 10)。設定・管理を閉じた・開き直したら戻す
  const offChat = settingsOpen || adminOpen;
  const [peek, setPeek] = useState(false);
  useEffect(() => setPeek(false), [offChat]);
  // 広いときに会話の横に出す一覧が開いているか(狭いときは listOpen を overlay で重ねて出すので、今のまま。決定事項 12)
  const wideOpen = !narrow && (offChat ? peek : listOpen);
  // shownOpen は、今見えている一覧が開いているか。setShown はそれを開け閉めする(F2 で名前を変えるために開くときなど。覚えない)
  const shownOpen = narrow ? listOpen : wideOpen;
  const setShown = (open: boolean) => (!narrow && offChat ? setPeek(open) : setListOpen(open));
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
  // 画面を離れる操作(会話を選ぶ・「+ 新規」・設定や管理の画面を開け閉めする)は、保存していない変更があれば先に知らせる(0053 の決定事項 16)
  const { guard } = useUnsaved();
  const models = useModels(toasts, transcript, setBusy);
  // 起動に失敗したあと(lib が無いなど)は、Go のメソッドがどれも同じ理由で断る。タブを移る・ウィンドウに戻るたびに
  // 同じエラーが積み上がって設定の画面のタブが下に押し出されるので、その理由を含むエラーは出さない(理由は useModels が「起動できません」で1回だけ出す)
  const { startupError, noteServerDown } = models;
  const addTranscriptError = transcript.addError;
  const startupFailure = useCallback((text: string) => startupError !== "" && text.includes(startupError), [startupError]);
  // 会話の表示に出すエラー(会話の中で起きたことの記録。0043 の決定事項 5)。
  // retry(横に「再生成」を出してよいか。0048 の D8)も渡す(以前は落としていて、「再生成」が出なかった。2026-11-21)。
  // aibow-server につながらないことをもう伝えた理由のエラーは出さない(つながらない間に会話を選ぶたびに、同じ「aibow-server につながりません」が
  // 積み重なっていた。記録は消さないので、重ねて出さない。つながらないことは、最初の1回と左下のサーバーのアイコンで分かる。0057 の決定事項 11)
  const addRecordError = useCallback((text: string, caution?: boolean, retry?: boolean) => {
    if (startupFailure(text) || !noteServerDown(text)) return;
    addTranscriptError(text, caution, retry);
  }, [addTranscriptError, startupFailure, noteServerDown]);
  // 画面の操作のエラー。設定・管理・会話の設定の画面を開いていればその画面に、そうでなければ会話の表示に出す。
  // 設定などの画面のエラーは次の操作で消える(0043)ので、つながらないエラーも操作のたびに出す(押しても何も起きないように見えないように)。
  // 印だけは立てる(同じ理由で会話の表示とカードに重ねないため)
  const addError = useCallback((text: string, caution?: boolean) => {
    if (startupFailure(text)) return;
    if (inOpScope.current) {
      noteServerDown(text);
      const id = ++screenErrorId.current;
      setScreenErrors(e => [...e, { id, text }]);
      return;
    }
    if (!noteServerDown(text)) return;
    addTranscriptError(text, caution);
  }, [addTranscriptError, startupFailure, noteServerDown]);
  const convs = useConversations(addError);
  // 送信・生成のエラーは、設定の画面を開いている間に届いても会話の表示に残す(記録なので、次の操作で消さない)
  const chat = useChat({ ...transcript, addError: addRecordError }, setBusy, convs, toasts, models.reload);
  const app = useAppSettings({ addError });
  // 入力欄で読み込んだファイル(0030)。送り先の会話(新しい会話の画面では、これから作る会話の設定)で、会話の上限に入るかを Go が数える
  const attachTarget = useCallback(
    () => ({ id: convs.current?.conversation.id ?? NIL, draft: convs.current ? null : convs.draft }),
    [convs.current, convs.draft],
  );
  const attachments = useAttachments(attachTarget, addError);
  // 画面の明るさ: Go から届いた設定・設定の画面で選んだ値を効かせる(届くまでは前回の写し。theme.ts)
  const theme = app.settings?.display.theme;
  useEffect(() => {
    if (theme !== undefined) setTheme(theme);
  }, [theme]);

  // 起動時: 最後に開いていた会話(無ければ新しい会話の画面)に移ってから、モデルの一覧を取る。
  // 画面を移ると表示が作り直されるので、「読み込み中…」のメモが消えないよう、この順にする。
  // StrictMode では effect が2回呼ばれるので、1回だけにする(2回だと会話が2つ作られることがある)
  const started = useRef(false);
  const restored = useRef(false); // 起動時に最後の会話を開き終えたか(開いている会話を覚え始める合図)
  // 起動時に会話を読めなかったか(aibow-server が止まっていた)。読めないと、最後の会話も開かず、新しい会話の画面の設定(draft)も無いまま
  // (送信できず、プロファイルも選べない)になるので、つながったときに起動時の読み込みからやり直す(下の effect)
  const initFailed = useRef(false);
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    (async () => {
      const initErr = await convs.init(loadStored(STORED_LAST_CONVERSATION)).then(() => null, (err: unknown) => err);
      initFailed.current = initErr !== null;
      restored.current = true;
      const ok = await models.reload().catch(err => {
        addError(`モデル一覧を取得できません: ${errorMessage(err)}`);
        return false;
      });
      if (!ok) return; // 起動に失敗している。理由は useModels が出している
      // aibow-server につながらないときは、useModels がカードを1回出している。会話と設定も同じ理由で読めないが、
      // 同じ理由のカードは addError が重ねて出さない(つながったら読み直す。下の effect)
      if (initErr) addError(`会話を読み込めません: ${errorMessage(initErr)}`);
      await app.reload().catch(err => addError(`設定を読み込めません: ${errorMessage(err)}`));
    })();
  }, [convs.init, models.reload, app.reload, addError]);

  // aibow-server につながったら(起動したときに止まっていた・途中で止まった)、読めなかった設定と会話の一覧を読み直す。
  // 起動時に会話を読めず、まだ何も開いていない(会話も新しい会話の画面の設定も無い)ままなら、起動時と同じく最後の会話を開く
  // (一覧だけを読み直すと、一覧は出ても送信できない画面のまま残り、「+ 新規」を押すまで抜けられなかった)
  const serverError = models.serverError;
  const wasDown = useRef(false);
  const emptyScreen = useRef(false);
  emptyScreen.current = convs.current === null && convs.draft === null;
  useEffect(() => {
    if (serverError) {
      wasDown.current = true;
      return;
    }
    if (!wasDown.current) return;
    wasDown.current = false;
    app.reload().catch(err => addError(`設定を読み込めません: ${errorMessage(err)}`));
    if (initFailed.current && emptyScreen.current) {
      convs.init(loadStored(STORED_LAST_CONVERSATION)).then(
        () => { initFailed.current = false; },
        err => addError(`会話を読み込めません: ${errorMessage(err)}`),
      );
      return;
    }
    initFailed.current = false; // 会話を開いた・新しい会話の画面に移ったあとは、やり直さない(利用者が選んだ画面を変えない)
    convs.reloadList().catch(err => addError(`会話を読み込めません: ${errorMessage(err)}`));
  }, [serverError, app.reload, convs.init, convs.reloadList, addError]);

  // 会話で選べる MCP のサーバー(settings.json に書いたもの全部)。起動したときと、ツールの扱い(確認しない)を変えたときに読む
  const [mcpServers, setMcpServers] = useState<ServerInfo[]>([]);
  // ブラウザでは aibow-server の設定に書いた MCP のサーバー(0024)
  const reloadMcp = useCallback(() => {
    ChatService.MCPServers().then(
      s => setMcpServers(s ?? []),
      err => addError(`MCP のサーバーの一覧を取得できません: ${errorMessage(err)}`),
    );
  }, [addError]);
  useEffect(reloadMcp, [reloadMcp]);
  // その PC のワークスペース(入力欄の上で選ぶ・設定の画面で変える。0035。ブラウザでは空)
  const [workspaces, reloadWorkspaces] = useWorkspaces(addError);

  // GPU に載せる層の数はモデルごとに覚えている(0014 の 4)ので、読み込んでいるモデルが変わったら設定の画面の値を取り直す
  const currentModel = models.current?.id;
  useEffect(() => {
    if (currentModel && restored.current) app.reload().catch(() => {});
  }, [currentModel, app.reload]);

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

  // ウィンドウに戻ってきたら一覧を作り直す(aibow を起動したまま ollama pull したときのため)。
  // 会話の一覧も取り直す(ブラウザのタブを2つ開いて、片方で会話を作った・消したとき。0016 の 9-3)
  useEffect(() => {
    const onFocus = () => {
      if (busy === "idle") models.reload().catch(() => {});
      convs.reloadList().catch(() => {});
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, [busy, models.reload, convs.reloadList]);

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
      if (!shownOpen) {
        openedForRename.current = true;
        setShown(true);
      }
      setRenamingId(currentId);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [currentId, unavailable, renamingId, shownOpen, narrow, offChat]);
  const endRename = () => {
    setRenamingId(null);
    if (openedForRename.current) {
      openedForRename.current = false;
      setShown(false);
    }
  };

  // 「+ 新規」: 新しい会話の画面に移る。会話そのものは、そこで最初に送信したときに作る
  const home = convs.current === null;
  const onNew = () => guard(onNewNow);
  const onNewNow = () => {
    // 設定の画面・管理の画面から戻ってきたときは、新しい会話の画面で選んでいたワークスペースを残す(0049 の決定事項 1。
    // 設定を見に行っただけで、選んだものが消えないように)。新しい会話の画面にいて押したときだけ、下で外す
    const fromSettings = settingsOpen || adminOpen;
    closeOverlay();
    setSettingsOpen(false); // 設定の画面を開いていたら閉じて、新しい会話の画面に戻る
    setConvSettingsOpen(false);
    if (reading) {
      setReading(null);
      convs.goHome().then(focusInput, err => addError(errorMessage(err)));
      return;
    }
    if (home) {
      // すでに新しい会話の画面なら、入力欄に戻すだけ。ただしワークスペースは外す
      // (「+ 新規」はワークスペースを選ばない。0035。一覧のまとまりの「+」で選んでいたときも、ここでは外す。0038)
      if (convs.draft?.workspace && !fromSettings) convs.updateSettings({ workspace: "" });
      // この画面のエラーはまだ会話が無いときのもの(記録する会話が無い)ので、「+ 新規」で新しく始めるときに消す。
      // 会話から移ったとき(下の goHome)は、useChat が表示を空にしている
      transcript.reset([]);
      focusInput();
      return;
    }
    convs.goHome().then(focusInput, err => addError(errorMessage(err)));
  };
  // 一覧のまとまりの「+」(並べ方がワークスペースごとのとき。0038): そのワークスペースを選んだ新しい会話の画面に移る。
  // 上の「+ 新規」と違い、利用者がワークスペースを選んで押しているので、選んでおいてよい(0035 の「前回のワークスペースを選ばない」とは別)。
  // 「ワークスペースなし」の「+」は workspace が "" で、設定の画面から戻ったときも含めて必ず外す(0051 の決定事項 2)
  const onNewIn = (workspace: string) => guard(() => onNewInNow(workspace));
  const onNewInNow = (workspace: string) => {
    closeOverlay();
    setSettingsOpen(false);
    setConvSettingsOpen(false);
    const wasReading = reading !== null;
    if (wasReading) setReading(null);
    const ready = home && !wasReading ? Promise.resolve() : convs.goHome();
    ready.then(() => {
      convs.updateSettings({ workspace });
      focusInput();
    }, err => addError(errorMessage(err)));
  };

  // 送信できない理由。別の会話で生成中なら、会話を読むことはできるが送信はできない
  const blockedReason =
    busy === "generating" && chat.generatingId !== currentId ? "別の会話で返事を生成中です"
      : busy === "generating" ? "返事を生成中です"
        : busy === "loading" ? "モデルを読み込み中です"
          : undefined;

  // 入力欄とモデルの選択。チャットの画面では下に、新しい会話の画面では真ん中に出す
  const canSend = busy === "idle" && (convs.current !== null || convs.draft !== null);
  // 開いている会話の設定(新しい会話の画面では、これから作る会話の設定 = 選んだプロファイルの写し)
  const settings = convs.current?.conversation.settings ?? convs.draft;
  // worktree で作業するか(0039)。アプリだけ。作るのは送信の途中なので、状態は Go の知らせで取り直す(worktree.ts)
  const worktree = useWorktree(convs.current?.conversation.id ?? null, isBrowser ? null : settings ?? null, addError);
  useEffect(() => setDiffOpen(false), [currentId]); // 別の会話を開いたら差分の画面を閉じる
  // 開いている会話のプロファイル(新しい会話の画面では、選んだプロファイル)。固定・使えないを画面に出すため(0014)
  const profile = convs.current ? convs.current.profile : convs.profiles.find(i => i.profile.id === convs.draftProfile)?.profile ?? null;
  const lock = lockOf(profile);
  // ブラウザの会話の資料のフォルダ(名前だけ。0024 の 12-2)。開いている会話は Go が決めたもの、新しい会話の画面は選んだプロファイルのもの
  const folder = !isBrowser ? "" : convs.current ? convs.current.folder ?? "" : grantedFolder(profile);
  // ツールが使うフォルダ(アプリはワークスペースのメインのフォルダ、ブラウザは資料のフォルダの名前)。無ければ空。作業フォルダが要るツールの印に使う
  const workDir = isBrowser ? folder : mainFolder(workspaces.workspaces?.find(w => w.id === settings?.workspace))?.path ?? "";
  const input = (
    <>
      <Composer
        text={text}
        onTextChange={setText}
        canSend={canSend}
        blockedReason={blockedReason}
        home={home}
        focusKey={focusKey}
        onSend={async t => {
          // 送れなかった(発言が保存されなかった)ら、打った文と読み込んだファイルを入力欄に戻す。その間に打ち始めていたら、そちらを残す
          const files = attachments.take();
          if (!(await chat.send(t, files))) {
            setText(cur => cur || t);
            attachments.restore(files);
          }
        }}
        files={attachments.files}
        onAddFiles={fs => void attachments.add(fs)}
        onRemoveFile={attachments.remove}
        top={settings && (
          // 入力欄の上: ワークスペースと、この会話のツール(どちらも会話ごとの設定)。
          // ブラウザの会話はワークスペースを選ばない(ツールはサーバーの PC で動くため。0024 の決定事項 17)。
          // 配られたプロファイルに資料のフォルダがあれば、その名前を押せない形で出す(決定事項 26)
          <div className="composer-top">
            {isBrowser && folder && <FolderBar name={folder} />}
            {!isBrowser && (
              <WorkspaceBar
                workspaces={workspaces.workspaces ?? []}
                selected={settings.workspace ?? ""}
                onChange={workspace => {
                  convs.updateSettings({ workspace });
                  focusInput();
                }}
                onCreated={reloadWorkspaces}
                onNotice={text => toasts.notify(text)}
                onError={addError}
              />
            )}
            {!isBrowser && settings.workspace && worktree.state && (
              <WorktreeBar
                state={worktree.state}
                on={!!settings.worktree}
                changes={worktree.changes}
                onToggle={on => {
                  convs.updateSettings({ worktree: on });
                  focusInput();
                }}
                onOpenDiff={() => setDiffOpen(true)}
              />
            )}
            <ToolBar
              builtinOn={!settings.builtin_off}
              servers={mcpServers}
              selected={settings.mcp_servers ?? []}
              workDir={workDir}
              profile={profile}
              onBuiltinChange={on => convs.updateSettings({ builtin_off: !on })}
              onServersChange={mcp_servers => convs.updateSettings({ mcp_servers })}
            />
          </div>
        )}
      />
      <ModelBar
        models={models}
        disabled={busy !== "idle"}
        size={settings ? settings.history_tokens + settings.reply_tokens : undefined}
        selected={settings?.model || undefined}
        lockReason={lock && locked(lock, FIX.model) ? fixedTitle(lock) : undefined}
        // 選ぶのは「この会話のモデル」(新しい会話の画面では、これから作る会話のモデル。0014 の 3)。
        // 選んでも読み込まず、会話のモデルを変えるだけ。読み込むのは送信(作り直す・今すぐ要約も)の前(Go の ensureModel)。
        // aibow-server 越しでは、選んだときに読み込んでも送信までにほかの人に入れ替えられることがあり、どのみち読み直すため
        // (0014 の「あとから決めたこと(2026-10-31)」)。それまでは枠の破線と吹き出しで見せる
        onSelect={id => convs.updateSettings({ model: id })}
      >
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
  const title = settingsOpen ? "設定" : adminOpen ? "管理" : showConvSettings ? "会話の設定" : convTitle;

  // ブラウザのタブの題名は、開いている会話の名前(タブが並んでも見分けられるように。0016 の 9-3)。
  // デスクトップのウィンドウの名前は Go が決める(タスクバーに出る。main.go)ので変えない
  useEffect(() => {
    if (isBrowser) document.title = currentId ? `${convTitle} - AIBow` : "AIBow";
  }, [currentId, convTitle]);

  // 知らせ(SSE)のつながりが切れて、つなぎ直したら、切れている間のことを取り直す(ブラウザだけ。0016 の 9-3)。
  // 生成中の会話は、送信の終わりで開き直す(useChat)ので、ここでは開き直さない。
  // ただし確認を待っていたら、そのカードは今出す(答えるまで送信が進まないため。0024 の決定事項 15)
  const reconnectState = useRef({ currentId, generatingId: chat.generatingId });
  reconnectState.current = { currentId, generatingId: chat.generatingId };
  useEffect(() => onReconnect(() => {
    const { currentId, generatingId } = reconnectState.current;
    convs.reloadList().catch(() => {});
    models.reload().catch(() => {});
    if (currentId && currentId !== generatingId) convs.open(currentId).catch(err => addError(errorMessage(err)));
    chat.resync();
  }), [convs.reloadList, convs.open, models.reload, addError, chat.resync]);

  // 画面を開くときは、前に開いていたときのエラーを持ち越さない
  const markSettingsFrom = clearScreenErrors;
  const toggleSettings = () => guard(toggleSettingsNow);
  const toggleSettingsNow = () => {
    closeOverlay();
    if (!settingsOpen) markSettingsFrom();
    setConvSettingsOpen(false); // 会話の設定の画面からは、設定の画面に切り替える
    setAdminOpen(false);        // 管理の画面からも、設定の画面に切り替える
    setSettingsTab(t => ({ tab: "basic", n: t.n + 1 }));
    setSettingsOpenState(o => !o);
  };
  // 歯車の隣の盾: 管理の画面を開く / 閉じる(0032 の決定事項 5)
  const toggleAdmin = () => guard(toggleAdminNow);
  const toggleAdminNow = () => {
    closeOverlay();
    if (!adminOpen) markSettingsFrom();
    setConvSettingsOpen(false);
    setSettingsOpenState(false);
    setAdminOpen(o => !o);
  };
  // 左下の名前のメニューの「アカウント」: 設定の画面を「アカウント」のタブで開く(開いていても切り替える。0031 の決定事項 46)
  const openAccount = () => guard(openAccountNow);
  const openAccountNow = () => {
    closeOverlay();
    if (!settingsOpen) markSettingsFrom();
    setConvSettingsOpen(false);
    setSettingsTab(t => ({ tab: "account", n: t.n + 1 }));
    setSettingsOpen(true);
  };
  // メーターの右の歯車: 開いている会話の設定の画面を開く(閉じるのは「会話に戻る」など。開いている間は入力欄ごと隠れる)
  const openConvSettings = () => {
    closeOverlay();
    setSettingsOpen(false);
    markSettingsFrom();
    setConvSettingsOpen(true);
  };
  // 一覧で会話を選んだら、設定の画面を閉じてその会話を出す
  // ほかの人の会話を読むだけで開いているとき(共有された会話・管理者が読む。0018 の 11-2)
  const [reading, setReading] = useState<SharedRead | null>(null);
  inOpScope.current = settingsOpen || adminOpen || (!reading && showConvSettings);
  const [shared, setShared] = useState<ConversationRef[]>([]);
  const reloadShared = useCallback(() => {
    if (!me.login) return; // ログインの要らない形(1人)には、ほかの人がいない
    ChatService.SharedConversations().then(list => setShared(list ?? []), () => {});
  }, []);
  useEffect(() => {
    reloadShared();
    return onWindowFocus(reloadShared); // ほかの人が共有した・やめたことを、ウィンドウに戻ったときに拾う
  }, [reloadShared]);
  const onOpenShared = (id: string) => guard(() => onOpenSharedNow(id));
  const onOpenSharedNow = (id: string) => {
    closeOverlay();
    ChatService.ReadConversation(id).then(
      r => {
        setSettingsOpen(false);
        setConvSettingsOpen(false);
        setReading(r);
      },
      err => {
        addError(errorMessage(err));
        reloadShared();
      },
    );
  };
  // 会話を非アクティブにする / 戻す(0055)。付けた人の一覧だけが変わるので、変えたほうの一覧を読み直す
  const onSetInactive = (id: string, isShared: boolean, inactive: boolean) => {
    ChatService.SetConversationInactive(id, inactive)
      .then(() => (isShared ? reloadShared() : convs.reloadList()))
      .catch(err => {
        addError(errorMessage(err));
        if (isShared) reloadShared(); // 共有をやめられたなど。一覧から消す
      });
  };
  // 写して続ける:写した会話(自分の会話)を開く
  const copyReading = () => {
    if (!reading) return;
    ChatService.CopyShared(reading.conversation.id)
      .then(c => {
        setReading(null);
        toasts.notify(`${showName(reading.owner_name, reading.owner_display, true)} の会話を複製しました`);
        return convs.reloadList().then(() => convs.open(c.id));
      })
      .then(focusInput, err => addError(errorMessage(err)));
  };

  const onOpen = (id: string) => guard(() => onOpenNow(id));
  const onOpenNow = (id: string) => {
    closeOverlay();
    setSettingsOpen(false);
    setConvSettingsOpen(false);
    setReading(null);
    convs.open(id).then(focusInput, err => addError(errorMessage(err)));
  };

  // 見ていない会話の様子をトーストで知らせる(返事が終わった・エラーで終わった・確認を待っている)。
  // 見ている会話は表示で分かるので出さない。押すとその会話を開く。会話ごとに key を分け、新しい様子で置き換える
  const notifyConversation = (id: string, what: "done" | "error" | "waiting") => {
    const name = `「${convs.list.find(c => c.id === id)?.title || "新しい会話"}」`;
    const text = what === "done" ? `${name}の返事が終わりました`
      : what === "error" ? `${name}がエラーで終わりました`
        : `${name}でツールの実行の確認を待っています`;
    toasts.notify(text, {
      key: `conversation:${id}`,
      ms: what === "done" ? 6000 : 10000,
      tone: what === "error" ? "error" : undefined,
      onClick: () => onOpen(id),
      hint: "押すとこの会話を開きます",
    });
  };
  // 終わった会話(useChat の ended)が増えたら知らせる。開いて消えたときは知らせない
  const lastEnded = useRef(chat.ended);
  useEffect(() => {
    for (const [id, how] of Object.entries(chat.ended)) {
      if (lastEnded.current[id] !== how) notifyConversation(id, how);
    }
    lastEnded.current = chat.ended;
  }, [chat.ended]);
  // 確認を待ち始めた会話が、見ている会話でなければ知らせる
  useEffect(() => {
    const id = chat.confirmingId;
    if (id !== null && id !== currentId) notifyConversation(id, "waiting");
  }, [chat.confirmingId]);
  // 会話を開いたら、その会話のトーストは要らないので消す
  useEffect(() => {
    if (currentId !== null) toasts.dismissKey(`conversation:${currentId}`);
  }, [currentId]);
  // プロファイルを変えたら、一覧と、開いている会話の設定(固定した項目)・新しい会話の画面の写しを読み直す
  const onProfilesChanged = () => {
    convs.reloadProfiles().catch(err => addError(errorMessage(err)));
    app.reload().catch(() => {});
  };
  // 会話の上限・返事の上限を変えて保存する前に、作業領域をその大きさで作り直してみる。入らなければ reject し、画面は保存しない(下書きは残る)。
  // VRAM に入らないことを、送信したときではなく保存したときに分かるようにするため(0009。0053 で「適用」を画面の「保存」に入れた)。
  // 作り直したときだけトーストで知らせる(作り直さずに済んだときは、保存しただけなので出さない。0053 の決定事項 14)
  const tryLimits = async (history: number, reply: number) => {
    setBusy("loading");
    try {
      const changed = await ChatService.TryLimits(history, reply); // 失敗したら Go 側で元の大きさに戻している
      if (changed) {
        const size = (history + reply).toLocaleString();
        toasts.notify(`会話の上限 ${history.toLocaleString()} + 返事の上限 ${reply.toLocaleString()} = 作業領域 ${size} トークンに作り直しました(モデルは読み直していません)`);
      }
    } finally {
      await models.reload().catch(() => {});
      setBusy("idle");
    }
  };

  // 会話の場所に出すもの: 設定の画面 / 新しい会話の画面 / チャット
  let content;
  const viewErrors = screenErrors;
  if (settingsOpen) {
    content = (
      <SettingsView
        key={settingsTab.n}
        initialTab={settingsTab.tab}
        errors={viewErrors}
        onClose={() => guard(() => setSettingsOpen(false))}
        app={app.settings}
        models={models.models}
        modelState={models}
        servers={mcpServers}
        onProfilesChanged={onProfilesChanged}
        onTryLimits={tryLimits}
        onError={addError}
        appDisabled={busy !== "idle"}
        // 起動に失敗したとき(lib が無いなど)も、aibow-server にはつなげるようにする(つなぐなら lib は要らない)
        connectDisabled={busy === "generating" || busy === "loading"}
        onDisplayChange={app.setDisplay}
        onToolsChanged={() => {
          reloadMcp();
          convs.refreshUsage().catch(err => addError(errorMessage(err)));
        }}
        onNotice={text => toasts.notify(text, { ms: 8000 })}
        workspaces={workspaces}
        onWorkspacesChanged={reloadWorkspaces}
      />
    );
  } else if (adminOpen) {
    content = (
      <AdminView
        errors={viewErrors}
        onClose={() => guard(() => setAdminOpen(false))}
        onError={addError}
        onReadConversation={onOpenShared}
        models={models}
        servers={mcpServers}
        appDisabled={busy !== "idle"}
        onToolsChanged={() => {
          reloadMcp();
          convs.refreshUsage().catch(err => addError(errorMessage(err)));
        }}
        onTryLimits={tryLimits}
        onProfilesChanged={onProfilesChanged}
      />
    );
  } else if (reading) {
    content = <ReadView read={reading} pinQuestion={app.settings?.display.pin_question ?? false} onCopy={copyReading} />;
  } else if (showConvSettings && convs.current) {
    const current = convs.current.conversation.settings;
    content = (
      <ConversationSettingsView
        id={convs.current.conversation.id}
        title={convTitle}
        onRename={title => convs.rename(convs.current!.conversation.id, title).catch(err => addError(errorMessage(err)))}
        settings={current}
        models={models.models}
        errors={viewErrors}
        onClose={() => guard(() => {
          setConvSettingsOpen(false);
          focusInput();
        })}
        onSave={convs.saveSettings}
        onError={addError}
        worktree={worktree.state}
        onRenameWorktree={name =>
          ChatService.SetWorktreeName(convs.current!.conversation.id, name)
            .catch(err => {
              addError(errorMessage(err));
              throw err;
            })
            .finally(worktree.reload)}
        profile={convs.current.profile}
        folder={folder}
        workDir={workDir}
        onSaveToProfile={() => {
          const id = convs.current!.conversation.id;
          const p = convs.current!.profile;
          if (!p) return;
          convs.flushSettings()
            .then(() => ChatService.SaveToProfile(id, p.id))
            .then(() => {
              toasts.notify(`この会話の設定を、プロファイル「${p.name}」に保存しました`);
              onProfilesChanged();
            })
            .catch(err => addError(errorMessage(err)));
        }}
        onSaveAsProfile={name => {
          const id = convs.current!.conversation.id;
          convs.flushSettings()
            .then(() => ChatService.SaveAsProfile(id, name))
            .then(p => {
              toasts.notify(`この会話の設定から、プロファイル「${p.name}」を作りました`);
              onProfilesChanged();
            })
            .catch(err => addError(errorMessage(err)));
        }}
        lastPrompt={chat.lastPrompt}
        lastPromptFormat={chat.lastPromptFormat}
        servers={mcpServers}
        limitsDisabled={busy !== "idle"}
        onTryLimits={tryLimits}
        share={convs.current.conversation.share ?? ""}
        shareWith={convs.current.conversation.share_with ?? []}
        shareGroups={convs.current.conversation.share_groups ?? []}
        onShare={(share, users, groups) => {
          const id = convs.current!.conversation.id;
          ChatService.SetConversationShare(id, share, users, groups)
            .then(() => Promise.all([convs.open(id), convs.reloadList()])) // 共有の印(この画面と一覧)を読み直す
            .catch(err => addError(errorMessage(err)));
        }}
      />
    );
  } else if (home) {
    content = (
      <NewChat
        items={transcript.items}
        profiles={convs.profiles}
        profile={convs.draftProfile}
        onProfile={id => convs.selectProfile(id).catch(err => addError(errorMessage(err)))}
      >
        {input}
      </NewChat>
    );
  } else {
    content = (
      <section className="center">
        <ChatView
          items={transcript.items}
          firstId={convs.current?.usage?.first_id ?? null}
          conversationId={currentId}
          pinQuestion={app.settings?.display.pin_question ?? false}
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
          discard={{
            disabled: unavailable || (chat.generatingId !== null && chat.generatingId === currentId),
            // 送ったファイルを捨てる(0030 の決定事項 26〜28)。Go が中身を消し、札は「消去済み」にしてメーターを数え直す
            onDiscard: fileId => {
              if (!currentId) return;
              ChatService.DiscardFile(currentId, fileId).then(
                () => {
                  transcript.discardFile(fileId);
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
  // 設定・管理・会話の設定の画面のエラーは、次の操作で消す(errors.ts の operationEvents)。消すのは表示の起点を進めるだけで、
  // 会話の表示のエラー(どこで失敗したかの記録)はそのまま。display: contents の箱なので、並べ方(main の flex の子)は変わらない
  if (settingsOpen || adminOpen || (!reading && showConvSettings)) {
    content = <div className="op-scope" {...operationEvents(markSettingsFrom)}>{content}</div>;
  }

  const listProps = {
    server: models.server,
    serverError: models.serverError,
    list: convs.list,
    currentId,
    generatingId: chat.generatingId,
    waitingId: chat.confirmingId,
    ended: chat.ended,
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
    adminOpen,
    canAdmin: canAdmin(models.manage),
    onToggleAdmin: toggleAdmin,
    onOpenAccount: openAccount,
    onToggleOpen: () => {
      // 設定・管理の画面を開いている間に開け閉めするのは、そのときだけ(覚えない。0063 の決定事項 10)
      if (!narrow && offChat) {
        setPeek(!peek);
        return;
      }
      // 自分で開け閉めしたときだけ覚える(狭くなって自動で閉じた・F2 のために開いた、は覚えない)。
      // 狭いときに重ねて出す一覧は、そのときだけのものなので覚えない
      if (!narrow) saveStored(STORED_LIST_OPEN, String(!listOpen));
      setListOpen(!listOpen);
    },
    // 設定・管理の画面を開いている間は、開いている会話を選んだ色にしない(その会話の設定を触っているように見えないように。決定事項 11)
    showCurrent: !offChat,
    width: listWidth.width,
    shared,
    readingId: reading?.conversation.id ?? null,
    onOpenShared,
    onSetInactive,
    workspaces: workspaces.workspaces ?? [],
    currentWorkspace: convs.current ? convs.current.conversation.settings.workspace ?? "" : null,
    onNewIn,
  };

  // aibow-server につないでいるか(上限の欄の説明を変える。0015)
  const server = models.server ? { url: models.server, nCtx: models.current?.n_ctx ?? 0 } : null;

  return (
    <ServerContext.Provider value={server}>
      {/* 狭いときは、ここ(会話の横)はいつも畳んだ細い列で、開いた一覧は下の overlay に重ねて出す */}
      <ConversationList {...listProps} open={wideOpen} />
      {wideOpen && (
        <Splitter width={listWidth.width} onResize={listWidth.set} onCommit={listWidth.commit} onReset={listWidth.reset} />
      )}
      <div className="workspace">
        {/* ブラウザにはブラウザのタイトルバーがあるので、ウィンドウのボタンのあるタイトルバーは出さない(0016 の 17) */}
        {!isBrowser && <TitleBar title={title} />}
        <main>{content}</main>
      </div>
      {overlay && (
        // 後ろの影を押したら閉じる(一覧の中を押したときは閉じない)
        <div className="list-overlay" onMouseDown={e => e.target === e.currentTarget && setListOpen(false)}>
          <ConversationList {...listProps} open />
        </div>
      )}
      <Toasts toasts={toasts.toasts} onDismiss={toasts.dismiss} />
      {diffOpen && convs.current && worktree.state?.active && (
        <WorktreeDiff
          id={convs.current.conversation.id}
          state={worktree.state}
          generating={chat.generatingId === convs.current.conversation.id}
          onClose={() => { setDiffOpen(false); focusInput(); }}
          onChanged={worktree.reload}
          onDiscarded={() => {
            // Go は会話の切り替えも外している。画面の会話の設定もそれに合わせる(保存し直しても同じ値)
            convs.updateSettings({ worktree: false });
            worktree.reload();
          }}
          onNotice={text => toasts.notify(text, { ms: 8000 })}
        />
      )}
      <ImeFocusRelay />
    </ServerContext.Provider>
  );
}
