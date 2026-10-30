// useModels.ts: モデルの一覧と切り替え。
//
// 一覧は models フォルダの GGUF と、Ollama が保存したモデルと、画面で足した置き場所のモデル(0015 の 8-4)。
// 選ぶと Go が今のモデルを解放して、新しいモデルを読み込む(数秒〜十数秒)。
// 設定の画面の「モデル」のタブ(ModelSettings.tsx)からは、降ろす・モデルごとの GPU の層数も変える。
// aibow-server につないでいるとき(0015)は、一覧も読み込みもサーバーのもの。つながらなければエラーのカードを出し、
// 一覧を取り直したとき(ウィンドウに戻ってきたとき・返事が終わったとき)につながれば、そのまま使える。
import { useCallback, useEffect, useRef, useState } from "react";
import { ChatService, onModelLoaded } from "./backend";
// Go の models パッケージ。"…/aibow/models" と書くと、Wails が aibow パッケージに作る models.ts の方が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../bindings/github.com/secondarykey/aibow/models/index";
import type { ModelInfo } from "../bindings/github.com/secondarykey/aibow/engine";
import type { Transcript } from "./transcript";
import type { Notifier } from "./toast";
import { errorMessage } from "./errors";

// 起動時のお知らせ(使われなくなった環境変数など)を出しておく時間。文が長いので普通のトーストより長くする
const NOTICE_MS = 20000;

// unavailable は起動に失敗した状態(lib が無いなど)。何もできないので、ずっと操作できなくする
export type Busy = "idle" | "generating" | "loading" | "unavailable";

export interface Models {
  models: ModelEntry[];      // 選べるモデル(管理者が使えないようにしたモデルは入れない)
  allModels: ModelEntry[];   // モデルのタブの一覧(管理者には、使えないようにしたモデルも印 disabled 付きで入る。0018 の 11-0)
  current: ModelInfo | null; // 今読み込まれているモデル(Go の ModelInfo)
  gpu: boolean;
  loading: boolean;          // モデルを読み込み中か(起動時・切り替え時)
  server: string;            // 生成を頼んでいる aibow-server のつなぎ先(アプリの中で動かしているときは空)
  serverError: string;       // aibow-server につながらない理由(つながっていれば空)
  startupError: string;      // 起動に失敗した理由(lib が無いなど。起動できていれば空)。Go のメソッドはどれもこの理由で断る
  // manage は、aibow-server の中で、サーバーの PC の事情(降ろす・層の数・置き場所・出どころ)を見せて変えてよい人か
  // (ブラウザの管理者か、サーバーの PC の上から開いた画面。0018 の決定事項 8)。モデルのタブをアプリの中で動かすときと同じに出す
  manage: boolean;
  gpuLayers: { [id: string]: number | undefined }; // モデルの ID → 読み込むときの GPU の層数
  formats: { [id: string]: string | undefined };   // モデルの ID → 使う書式の名前(未対応の種類のモデルは入らない。0026)
  lastModel: string;         // 最後に読み込んだモデル(降ろしたあとも、どれを降ろしたかを出すため)
  switchModel(id: string): Promise<boolean>; // 読み込めたら true
  unload(): Promise<void>;                   // 読み込み中のモデルを降ろす(VRAM を空ける)
  setLayers(id: string, n: number): Promise<void>; // モデルの GPU の層数を変える(読み込み中のモデルなら読み直す)
  reload(): Promise<boolean>; // 一覧を取り直す。起動に失敗していたら false
  // aibow-server につながらないことをカードで伝えたままか。serverError(state)と違い、呼んだときの値を返す
  // (reload を待った直後に、同じ理由のエラーを重ねて出さないかを決めるため)
  serverDown(): boolean;
}

// formatWarning は、書式(チャットテンプレート)に対応していない種類のモデルのときのお知らせ。対応していれば null
function formatWarning(info: ModelInfo): string | null {
  return info.format ? null : `${info.name} の書式(種類: ${info.arch || "不明"})には未対応です。送信するとエラーになります`;
}

// 読み込みのお知らせはトースト(n)に出す。「読み込み中…」は出したままにし、終わったら同じ key で置き換える
export function useModels(n: Notifier, t: Pick<Transcript, "addError">, setBusy: (b: Busy) => void): Models {
  const { notify, dismissKey } = n;
  const { addError } = t;
  const [models, setModels] = useState<ModelEntry[]>([]);
  const [allModels, setAllModels] = useState<ModelEntry[]>([]);
  const [current, setCurrent] = useState<ModelInfo | null>(null);
  const [gpu, setGpu] = useState(false);
  const [loading, setLoading] = useState(false);
  const [server, setServer] = useState("");
  const [serverError, setServerError] = useState("");
  const [startupError, setStartupError] = useState("");
  const [manage, setManage] = useState(false);
  const [gpuLayers, setGpuLayers] = useState<{ [id: string]: number | undefined }>({});
  const [formats, setFormats] = useState<{ [id: string]: string | undefined }>({});
  const [lastModel, setLastModel] = useState("");
  const serverDown = useRef(false); // つながらないことをカードで伝えたか(取り直すたびに出さないため)

  // 起動直後は Go がモデルを裏で読み込んでいる最中のことがある。終わると models イベントが来る。
  // Models の戻り値とイベントは別の経路で届くので、イベントが先に着いても大丈夫なように覚えておく。
  const startupLoading = useRef(false);
  const startupDone = useRef(false);
  const unavailable = useRef(false);
  const noticesShown = useRef(false);

  const reload = useCallback(async () => {
    const d = await ChatService.Models();
    if (d.error) {
      // リリース版はコンソールが無いので、起動に失敗した理由はここでしか見えない
      if (!unavailable.current) {
        unavailable.current = true;
        for (const text of d.notices ?? []) notify(text, { ms: NOTICE_MS });
        setBusy("unavailable");
        setStartupError(d.error);
        addError(`起動できません: ${d.error}`);
      }
      return false;
    }
    setModels((d.models ?? []).filter(m => !m.disabled));
    setAllModels(d.models ?? []);
    setCurrent(d.current);
    setGpu(d.gpu);
    setServer(d.server ?? "");
    setGpuLayers(d.gpu_layers ?? {});
    setFormats(d.formats ?? {});
    setLastModel(d.last_model ?? "");
    // 起動時のお知らせ(使われなくなった環境変数など)。一覧は何度も取り直すので、最初の1回だけ出す
    if (!noticesShown.current) {
      noticesShown.current = true;
      for (const text of d.notices ?? []) notify(text, { ms: NOTICE_MS });
    }
    // aibow-server につながらない。カードは止まったときに1回だけ出し、つながったらお知らせで伝える
    setServerError(d.server_error ?? "");
    setManage(!!d.manage);
    if (d.server_error) {
      if (!serverDown.current) {
        serverDown.current = true;
        addError(d.server_error);
      }
      return true;
    }
    if (serverDown.current) {
      serverDown.current = false;
      notify(`aibow-server につながりました(${d.server})`);
    }
    if (d.loading && !startupLoading.current && !startupDone.current) {
      startupLoading.current = true;
      setLoading(true);
      setBusy("loading");
      notify(`${d.loading} を読み込み中…`, { key: "model", sticky: true });
    }
    return true;
  }, [notify, addError, setBusy]);

  // 起動時の読み込みが終わった合図(models イベント)を待つ。最初の一覧の取得(reload)は App が呼ぶ
  useEffect(() => {
    const off = onModelLoaded(async () => {
      startupDone.current = true;
      const wasLoading = startupLoading.current;
      startupLoading.current = false;
      const d = await ChatService.Models();
      setModels(d.models ?? []);
      setCurrent(d.current);
      if (!wasLoading) return;
      if (d.current) {
        notify(formatWarning(d.current) ?? `${d.current.name} を読み込みました(${d.current.load_sec.toFixed(1)} 秒)`, { key: "model" });
      } else {
        dismissKey("model");
        addError("モデルを読み込めませんでした。入力欄の下で選び直してください");
      }
      setLoading(false);
      setBusy("idle");
    });
    return off;
  }, [notify, dismissKey, addError, setBusy]);

  const switchModel = useCallback(async (id: string) => {
    const name = models.find(m => m.id === id)?.name ?? id;
    setLoading(true);
    setBusy("loading");
    notify(`${name} を読み込み中…`, { key: "model", sticky: true });
    let failed = false;
    try {
      const info = await ChatService.SwitchModel(id); // 読み込み終わるまで返らない
      if (info) notify(formatWarning(info) ?? `モデルを ${info.name} に切り替えました(読み込み ${info.load_sec.toFixed(1)} 秒)`, { key: "model" });
    } catch (err) {
      // Go が error を返すと、ここに来る
      addError(errorMessage(err));
      failed = true;
    }
    const d = await ChatService.Models(); // 失敗したときは元のモデルの表示に戻る
    setModels(d.models ?? []);
    setCurrent(d.current);
    setGpuLayers(d.gpu_layers ?? {});
    setFormats(d.formats ?? {});
    setLastModel(d.last_model ?? "");
    if (failed) {
      if (d.current) notify(`元のモデル ${d.current.name} に戻しました`, { key: "model" });
      else dismissKey("model");
    }
    setLoading(false);
    setBusy("idle");
    return !failed;
  }, [models, notify, dismissKey, addError, setBusy]);

  // 降ろす。次に送信すると、送信の前に会話のモデルを読み込む(0014 の 3)
  const unload = useCallback(async () => {
    const name = current?.name ?? "";
    setBusy("loading");
    try {
      await ChatService.UnloadModel();
      notify(`${name} を降ろしました(次に送信するときに読み込みます)`, { key: "model" });
    } catch (err) {
      addError(errorMessage(err));
    }
    await reload().catch(err => addError(errorMessage(err)));
    setBusy("idle");
  }, [current, reload, notify, addError, setBusy]);

  // モデルごとの GPU の層数。読み込み中のモデルなら読み直す(失敗したら Go が元の層の数で読み直している)
  const setLayers = useCallback(async (id: string, n: number) => {
    const loaded = current?.id === id;
    const name = models.find(m => m.id === id)?.name ?? id;
    setBusy("loading");
    if (loaded) notify(`GPU に載せる層を ${n} にして、${name} を読み込み直しています…`, { key: "model", sticky: true });
    try {
      const info = await ChatService.SetModelGPULayers(id, n);
      notify(info
        ? `GPU に載せる層を ${n} にして読み込み直しました(${info.load_sec.toFixed(1)} 秒)`
        : `${name} の GPU に載せる層を ${n} にしました(次に読み込むときから効きます)`, { key: "model" });
    } catch (err) {
      dismissKey("model");
      addError(errorMessage(err));
    }
    await reload().catch(err => addError(errorMessage(err)));
    setBusy("idle");
  }, [current, models, reload, notify, dismissKey, addError, setBusy]);

  const isServerDown = useCallback(() => serverDown.current, []);

  return { models, allModels, current, gpu, loading, server, serverError, startupError, manage, gpuLayers, formats, lastModel, switchModel, unload, setLayers, reload, serverDown: isServerDown };
}
