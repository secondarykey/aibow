// 設定の画面の「モデル」のタブ(0015 の 8-4)。
//
// アプリの中で動かすとき:すぐ効く操作だけを画面から変える(一覧から読み込む / 降ろす・モデルごとの GPU の層数・置き場所を足す / 外す。0015 の 21)。
// 作業領域・llama.cpp の場所・最初のモデルは表示だけ(起動し直しが要るため)。
// 同時に読み込むのは1つ(VRAM に1つしか載らない)ので、「読み込む」は今のモデルと入れ替える。
//
// aibow-server 越しのとき:モデルの一覧(名前・大きさ・読み込み中か)と作業領域だけを出し、何も変えない(2026-10-22、ユーザーと決めた)。
// 読み込む / 降ろす・層の数・置き場所・出どころ・サーバーの PC のパスは、サーバーの PC の事情でサーバーの側で決めること(中央の制御)。
// つないでくる側から見せたり変えたりするのは別の問題なので出さない。モデルが変わるのは会話を送信するときだけ。
//
// ただしブラウザの管理者と、サーバーの PC の上から開いたブラウザ(models.manage)には、アプリの中で動かすときと同じものを出す
// (0018 の決定事項 8)。置き場所はサーバーの PC のパスを書く(ブラウザからはサーバーの PC のフォルダを選べないので、選ぶボタンは出さない)。
import { useCallback, useEffect, useState } from "react";
import { LuCircleCheck, LuFile, LuFileX, LuFolder, LuFolderX, LuInfo, LuLock, LuX } from "react-icons/lu";
import { ChatService } from "../backend";
import type { ModelSettingsResult } from "../../bindings/github.com/secondarykey/aibow/ui";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry, Place } from "../../bindings/github.com/secondarykey/aibow/models/index";
import type { Models } from "../useModels";
import { NumberInput } from "./ConversationSettingsForm";
import { errorMessage } from "../errors";
import { isBrowser } from "../backend";

interface Props {
  models: Models;
  disabled: boolean; // 生成中・読み込み中は、読み込む / 降ろす・層の数を変えられない(エンジンが1つのため)
  onError(text: string): void;
}

// どこから来たか(models.Entry の origin)の表示
const ORIGINS: Record<string, string> = { models: "models フォルダ", ollama: "Ollama", added: "足した場所" };

export function ModelSettings({ models, disabled, onError }: Props) {
  const [places, setPlaces] = useState<Place[]>([]);
  const [settings, setSettings] = useState<ModelSettingsResult | null>(null);
  const { reload } = models;
  // remote は、一覧と作業領域だけを出す形(aibow-server 越しで、サーバーの PC の事情を変えてよい人でない)
  const remote = !!models.server && !models.manage;

  // タブを開いたときに取り直す(一覧の層の数・置き場所は、ほかで変わっていることがあるため)。aibow-server 越しなら置き場所は聞かない
  const refresh = useCallback(async () => {
    try {
      const [p, s] = await Promise.all([remote ? [] : ChatService.ModelPlaces(), ChatService.ModelSettings(), reload()]);
      setPlaces(p ?? []);
      setSettings(s);
    } catch (err) {
      onError(errorMessage(err));
    }
  }, [remote, reload, onError]);
  const [opened] = useState(() => refresh); // 開いたときの1回だけ(refresh を毎回作り直しても、取り直しを繰り返さないように)
  useEffect(() => { opened(); }, [opened]);

  // 置き場所を足す / 外したら、一覧のモデルも変わるので取り直す
  const changePlaces = async (f: () => Promise<Place[] | null>) => {
    try {
      setPlaces((await f()) ?? []);
    } catch (err) {
      onError(errorMessage(err));
      return false;
    }
    await reload().catch(err => onError(errorMessage(err)));
    return true;
  };

  if (models.serverError) return <p className="hint">{models.serverError}</p>;
  if (remote) {
    return (
      <>
        <h2 title="会話を送信するときに、その会話のモデルに切り替わる">モデル</h2>
        <RemoteModelTable models={models} />
        {settings && <RemoteSettings server={models.server} nCtx={settings.settings.n_ctx} />}
      </>
    );
  }
  return (
    <>
      <h2 title="読み込めるのは1つ(VRAM に1つしか載らない)">モデル</h2>
      <ModelTable models={models} disabled={disabled} onError={onError} />

      <h2 className="section" title={models.server ? "aibow-server の PC でモデルを探す場所" : "モデルを探す場所"}>置き場所</h2>
      <PlaceList places={places} server={!!models.server} onRemove={p => changePlaces(() => ChatService.RemoveModelPlace(p))} />
      <AddPlace onAdd={p => changePlaces(() => ChatService.AddModelPlace(p))} onError={onError} />

      {settings && (models.server
        ? <RemoteSettings server={models.server} nCtx={settings.settings.n_ctx} />
        : <HostSettings s={settings} />)}
    </>
  );
}

// ---- モデルの一覧 ----------------------------------------------------------------------

function ModelTable({ models, disabled, onError }: { models: Models; disabled: boolean; onError(text: string): void }) {
  const { current, loading, manage, reload } = models;
  if (!models.allModels.length) return <p className="hint">モデルが見つかりません</p>;
  // 使えるモデルを決める(aibow-server の管理者だけ。0018 の決定事項 13・18)。使えなくした読み込み中のモデルはサーバーが降ろす
  const setEnabled = async (id: string, enabled: boolean) => {
    try {
      await ChatService.SetModelEnabled(id, enabled);
    } catch (err) {
      onError(errorMessage(err));
    }
    await reload().catch(err => onError(errorMessage(err)));
  };
  return (
    <div className={"model-table" + (manage ? " manage" : "")}>
      {manage && <span className="model-table-head" title="使えないモデルは、ほかの人の一覧に出ず、頼まれても読み込まない">使える</span>}
      <span className="model-table-head">名前</span>
      <span className="model-table-head" title="会話を1本の文字列にする書き方(チャットテンプレート)。モデルの種類で決まる">書式</span>
      <span className="model-table-head">出どころ</span>
      <span className="model-table-head num">大きさ</span>
      <span className="model-table-head" title="GPU に載せる層の数。モデルの層数以上なら全部 GPU。読み込み中のモデルなら、変えると読み直す">GPU の層</span>
      <span className="model-table-head" />
      {models.allModels.map(m => (
        <ModelRow
          key={m.id}
          m={m}
          format={models.formats[m.id]}
          manage={manage}
          onEnabled={enabled => setEnabled(m.id, enabled)}
          loaded={current?.id === m.id}
          layers={models.gpuLayers[m.id] ?? 999}
          totalLayers={current?.id === m.id ? current.layers : undefined}
          disabled={disabled || loading}
          onLoad={() => models.switchModel(m.id)}
          onUnload={() => models.unload()}
          onLayers={n => models.setLayers(m.id, n)}
        />
      ))}
    </div>
  );
}

function ModelRow({ m, format, manage, onEnabled, loaded, layers, totalLayers, disabled, onLoad, onUnload, onLayers }: {
  m: ModelEntry;
  format?: string; // 使う書式の名前(未対応の種類なら無い)
  manage: boolean; // 使えるかのスイッチを出す(aibow-server の管理者)
  onEnabled(enabled: boolean): void;
  loaded: boolean;
  layers: number;
  totalLayers?: number; // 読み込み中のモデルの層の数(読み込んでいなければ分からない)
  disabled: boolean;
  onLoad(): void;
  onUnload(): void;
  onLayers(n: number): void;
}) {
  const [n, setN] = useState(layers);
  useEffect(() => setN(layers), [layers]);
  const title = [m.id, m.place ? `置き場所: ${m.place}` : "", totalLayers ? `層の数: ${totalLayers}` : "", samplingText(m)].filter(Boolean).join("\n");
  return (
    <div className={"model-row" + (loaded ? " loaded" : "") + (m.disabled ? " model-disabled" : "")}>
      {manage && (
        <span>
          <input
            type="checkbox"
            checked={!m.disabled}
            disabled={disabled}
            onChange={e => onEnabled(e.target.checked)}
            aria-label="使える"
            title={m.disabled ? "使えるようにする" : "使えないようにする(読み込み中なら降ろす)"}
          />
        </span>
      )}
      <span className="model-name" title={title}>
        {loaded && <LuCircleCheck className="model-loaded" aria-label="読み込み中" />}
        {m.name}
      </span>
      <FormatCell m={m} format={format} />
      <span><span className={`origin-chip origin-${m.origin}`}>{ORIGINS[m.origin] ?? m.origin}</span></span>
      <span className="num">{m.size_gb.toFixed(1)} GB</span>
      <span className="model-layers">
        <NumberInput id={`layers-${m.id}`} value={n} min={0} step={1} disabled={disabled} onChange={setN} />
        {n !== layers && <button disabled={disabled} onClick={() => onLayers(n)} title={loaded ? "変えて読み直す" : "次に読み込むときから効く"}>適用</button>}
      </span>
      <span className="model-action">
        {loaded
          ? <button disabled={disabled} onClick={onUnload} title="VRAM を空ける。次に送信するときに会話のモデルを読み込む">降ろす</button>
          : !m.disabled && <button disabled={disabled} onClick={onLoad} title="今のモデルと入れ替える">読み込む</button>}
      </span>
    </div>
  );
}

// samplingText は GGUF が勧めるサンプリングの値(general.sampling.*)。表示だけで、生成には使わない(0026 の決定事項 37)
function samplingText(m: ModelEntry): string {
  const s = m.sampling;
  if (!s) return "";
  const parts = [
    s.temp != null ? `temperature ${s.temp}` : "",
    s.top_k != null ? `top_k ${s.top_k}` : "",
    s.top_p != null ? `top_p ${s.top_p}` : "",
  ].filter(Boolean);
  return parts.length ? `GGUF が勧めるサンプリング: ${parts.join(" / ")}` : "";
}

// FormatCell はモデルの書式の名前(どの書式で動くかを一覧で見えるように。0026 の決定事項 17)。
// 未対応の種類のモデルは、種類の名前と「未対応」を出す(読み込めても送信できない)
function FormatCell({ m, format }: { m: ModelEntry; format?: string }) {
  if (format) return <span className="model-format" title={`種類: ${m.arch}`}>{format}</span>;
  return <span className="model-format unsupported" title="この種類のモデルの書式には未対応です">{m.arch ? `未対応(${m.arch})` : "不明"}</span>;
}

// RemoteModelTable は aibow-server 越しの一覧(名前・大きさ・読み込み中か)。変える操作は無い
function RemoteModelTable({ models }: { models: Models }) {
  const { current } = models;
  if (!models.models.length) return <p className="hint">モデルが見つかりません</p>;
  return (
    <div className="model-table remote">
      <span className="model-table-head">名前</span>
      <span className="model-table-head" title="会話を1本の文字列にする書き方(チャットテンプレート)。モデルの種類で決まる">書式</span>
      <span className="model-table-head num">大きさ</span>
      {models.models.map(m => (
        <div key={m.id} className={"model-row" + (current?.id === m.id ? " loaded" : "")}>
          <span className="model-name" title={[m.name, samplingText(m)].filter(Boolean).join("\n")}>
            {current?.id === m.id && <LuCircleCheck className="model-loaded" aria-label="読み込み中" />}
            {m.name}
          </span>
          <FormatCell m={m} format={models.formats[m.id]} />
          <span className="num">{m.size_gb.toFixed(1)} GB</span>
        </div>
      ))}
    </div>
  );
}

// ---- 置き場所(アプリの中で動かすときだけ) ---------------------------------------------

function PlaceList({ places, server, onRemove }: { places: Place[]; server: boolean; onRemove(path: string): void }) {
  // 手で決めた置き場所(models フォルダ・Ollama)は表示だけ。どこで変えるかを錠の吹き出しに出す(aibow-server は server.json)
  const lockTitle = (p: Place) => p.origin === "ollama"
    ? "環境変数 OLLAMA_MODELS で変える"
    : server ? "aibow-server の server.json の modelsDir で変える" : "環境変数 AIBOW_MODELS_DIR で変える";
  return (
    <div className="place-list">
      {places.map(p => {
        const Icon = p.missing ? (p.kind === "file" ? LuFileX : LuFolderX) : p.kind === "file" ? LuFile : LuFolder;
        const what = p.origin === "added" ? "" : `${ORIGINS[p.origin]}・`;
        return (
          <div key={p.origin + p.path} className={"place-row" + (p.missing ? " missing" : "")}>
            <Icon className="place-icon" aria-hidden="true" />
            <span className="place-path" title={p.path}>{p.path}</span>
            <span className="place-note">{p.missing ? p.missing : `${what}${p.models} 個`}</span>
            {p.origin === "added"
              ? <button className="place-remove" onClick={() => onRemove(p.path)} title="置き場所を外す(モデルのファイルは消さない)" aria-label="外す"><LuX /></button>
              : <LuLock className="place-lock" title={lockTitle(p)} aria-label={lockTitle(p)} />}
          </div>
        );
      })}
    </div>
  );
}

// 置き場所を足す。パスを書くか、フォルダ / ファイルを選ぶダイアログで選ぶ
function AddPlace({ onAdd, onError }: {
  onAdd(path: string): Promise<boolean>;
  onError(text: string): void;
}) {
  const [path, setPath] = useState("");
  const choose = async (kind: "dir" | "file") => {
    try {
      const p = await ChatService.ChooseModelPlace(kind);
      if (p) setPath(p);
    } catch (err) {
      onError(errorMessage(err));
    }
  };
  const add = async () => {
    if (await onAdd(path)) setPath("");
  };
  return (
    <div className="place-add">
      <input
        type="text"
        value={path}
        placeholder="D:\models\gguf"
        title={isBrowser ? "aibow-server の PC のフォルダか GGUF のファイル" : "フォルダか GGUF のファイル"}
        onChange={e => setPath(e.target.value)}
        onKeyDown={e => { if (e.key === "Enter" && path.trim()) add(); }}
      />
      {!isBrowser && <>
        <button onClick={() => choose("dir")} title="フォルダを選ぶ"><LuFolder /> フォルダ</button>
        <button onClick={() => choose("file")} title="GGUF のファイルを選ぶ"><LuFile /> ファイル</button>
      </>}
      <button disabled={!path.trim()} onClick={add}>足す</button>
    </div>
  );
}

// ---- 表示だけの設定 --------------------------------------------------------------------

// HostSettings は、アプリの中で動かすときの起動の設定(環境変数で決まる)。
function HostSettings({ s }: { s: ModelSettingsResult }) {
  const v = s.settings;
  const how = "環境変数で変え、aibow の起動し直しが要ります";
  return (
    <SettingRows
      title="起動の設定"
      how={how}
      rows={[
        ["作業領域", "会話ごと(会話の上限 + 返事の上限)"],
        ["最初のモデル", v.model || "最後に使ったモデル"],
        ["llama.cpp", v.lib],
      ]}
    />
  );
}

// RemoteSettings は aibow-server 越しのときの設定。会話の上限 + 返事の上限がこの作業領域に入る必要があるので、作業領域だけを出す
function RemoteSettings({ server, nCtx }: { server: string; nCtx: number }) {
  return (
    <SettingRows
      title="aibow-server"
      how="作業領域は aibow-server が起動のときに決めた大きさ。会話の上限 + 返事の上限はこの中で使う"
      rows={[
        ["つなぎ先", server],
        ["作業領域", `${nCtx.toLocaleString()} トークン`],
      ]}
    />
  );
}

function SettingRows({ title, how, rows }: { title: string; how: string; rows: [string, string][] }) {
  return (
    <>
      <h2 className="section">
        {title}
        <LuInfo className="settings-info" title={how} aria-label={how} />
      </h2>
      <div className="host-settings">
        {rows.map(([k, val]) => (
          <div key={k} className="host-setting">
            <span className="host-key">{k}</span>
            <span className="host-value" title={val}>{val}</span>
          </div>
        ))}
      </div>
    </>
  );
}
