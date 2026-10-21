// 設定の画面の「モデル」のタブ(0015 の 8-4)。
//
// アプリの中で動かすとき:すぐ効く操作だけを画面から変える(一覧から読み込む / 降ろす・モデルごとの GPU の層数・置き場所を足す / 外す。0015 の 21)。
// 作業領域・llama.cpp の場所・最初のモデルは表示だけ(起動し直しが要るため)。
// 同時に読み込むのは1つ(VRAM に1つしか載らない)ので、「読み込む」は今のモデルと入れ替える。
//
// aibow-server 越しのとき:モデルの一覧(名前・大きさ・読み込み中か)と作業領域だけを出し、何も変えない(2026-10-22、ユーザーと決めた)。
// 読み込む / 降ろす・層の数・置き場所・出どころ・サーバーの PC のパスは、サーバーの PC の事情でサーバーの側で決めること(中央の制御)。
// つないでくる側から見せたり変えたりするのは別の問題なので出さない。モデルが変わるのは会話を送信するときだけ。
import { useCallback, useEffect, useState } from "react";
import { LuCircleCheck, LuFile, LuFileX, LuFolder, LuFolderX, LuInfo, LuLock, LuX } from "react-icons/lu";
import { ChatService } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { ModelSettingsResult } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry, Place } from "../../bindings/github.com/secondarykey/aibow/models/index";
import type { Models } from "../useModels";
import { NumberInput } from "./ConversationSettingsForm";
import { errorMessage } from "../errors";

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
  const remote = !!models.server;

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
      <ModelTable models={models} disabled={disabled} />

      <h2 className="section" title="モデルを探す場所">置き場所</h2>
      <PlaceList places={places} onRemove={p => changePlaces(() => ChatService.RemoveModelPlace(p))} />
      <AddPlace onAdd={p => changePlaces(() => ChatService.AddModelPlace(p))} onError={onError} />

      {settings && <HostSettings s={settings} />}
    </>
  );
}

// ---- モデルの一覧 ----------------------------------------------------------------------

function ModelTable({ models, disabled }: { models: Models; disabled: boolean }) {
  const { current, loading } = models;
  if (!models.models.length) return <p className="hint">モデルが見つかりません</p>;
  return (
    <div className="model-table">
      <span className="model-table-head">名前</span>
      <span className="model-table-head">出どころ</span>
      <span className="model-table-head num">大きさ</span>
      <span className="model-table-head" title="GPU に載せる層の数。モデルの層数以上なら全部 GPU。読み込み中のモデルなら、変えると読み直す">GPU の層</span>
      <span className="model-table-head" />
      {models.models.map(m => (
        <ModelRow
          key={m.id}
          m={m}
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

function ModelRow({ m, loaded, layers, totalLayers, disabled, onLoad, onUnload, onLayers }: {
  m: ModelEntry;
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
  const title = [m.id, m.place ? `置き場所: ${m.place}` : "", totalLayers ? `層の数: ${totalLayers}` : ""].filter(Boolean).join("\n");
  return (
    <div className={"model-row" + (loaded ? " loaded" : "")}>
      <span className="model-name" title={title}>
        {loaded && <LuCircleCheck className="model-loaded" aria-label="読み込み中" />}
        {m.name}
      </span>
      <span><span className={`origin-chip origin-${m.origin}`}>{ORIGINS[m.origin] ?? m.origin}</span></span>
      <span className="num">{m.size_gb.toFixed(1)} GB</span>
      <span className="model-layers">
        <NumberInput id={`layers-${m.id}`} value={n} min={0} step={1} disabled={disabled} onChange={setN} />
        {n !== layers && <button disabled={disabled} onClick={() => onLayers(n)} title={loaded ? "変えて読み直す" : "次に読み込むときから効く"}>適用</button>}
      </span>
      <span className="model-action">
        {loaded
          ? <button disabled={disabled} onClick={onUnload} title="VRAM を空ける。次に送信するときに会話のモデルを読み込む">降ろす</button>
          : <button disabled={disabled} onClick={onLoad} title="今のモデルと入れ替える">読み込む</button>}
      </span>
    </div>
  );
}

// RemoteModelTable は aibow-server 越しの一覧(名前・大きさ・読み込み中か)。変える操作は無い
function RemoteModelTable({ models }: { models: Models }) {
  const { current } = models;
  if (!models.models.length) return <p className="hint">モデルが見つかりません</p>;
  return (
    <div className="model-table remote">
      <span className="model-table-head">名前</span>
      <span className="model-table-head num">大きさ</span>
      {models.models.map(m => (
        <div key={m.id} className={"model-row" + (current?.id === m.id ? " loaded" : "")}>
          <span className="model-name" title={m.name}>
            {current?.id === m.id && <LuCircleCheck className="model-loaded" aria-label="読み込み中" />}
            {m.name}
          </span>
          <span className="num">{m.size_gb.toFixed(1)} GB</span>
        </div>
      ))}
    </div>
  );
}

// ---- 置き場所(アプリの中で動かすときだけ) ---------------------------------------------

function PlaceList({ places, onRemove }: { places: Place[]; onRemove(path: string): void }) {
  // 手で決めた置き場所(models フォルダ・Ollama)は表示だけ。どこで変えるかを錠の吹き出しに出す
  const lockTitle = (p: Place) => p.origin === "ollama" ? "環境変数 OLLAMA_MODELS で変える" : "環境変数 AIBOW_MODELS_DIR で変える";
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
        title="フォルダか GGUF のファイル"
        onChange={e => setPath(e.target.value)}
        onKeyDown={e => { if (e.key === "Enter" && path.trim()) add(); }}
      />
      <button onClick={() => choose("dir")} title="フォルダを選ぶ"><LuFolder /> フォルダ</button>
      <button onClick={() => choose("file")} title="GGUF のファイルを選ぶ"><LuFile /> ファイル</button>
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
