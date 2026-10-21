// 設定の画面の「モデル」のタブ(0015 の 8-4)。
//
// すぐ効く操作だけを画面から変える:一覧から読み込む / 降ろす・モデルごとの GPU の層数・置き場所を足す / 外す(0015 の 21)。
// 作業領域・ポート・llama.cpp の場所・最初のモデルは表示だけ(起動し直しが要り、aibow-server ならつないでいる全員に響くため)。
// 直接(アプリの中で動かす)でも aibow-server 越しでも同じに見せる。aibow-server 越しなら、一覧・置き場所・層の数はサーバーの PC のもの。
// 同時に読み込むのは1つ(VRAM に1つしか載らない)ので、「読み込む」は今のモデルと入れ替える。
// aibow-server 越しのときは「読み込む」「降ろす」を出さない(2026-10-22、ユーザーと決めた)。会話に結びつかない読み込み・降ろすは、
// サーバーを使っているほかの利用者(Cline など)と取り合いになるだけなので。モデルが変わるのは会話を送信するときだけにする。
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

  // タブを開いたときに取り直す(一覧の層の数・置き場所は、ほかで変わっていることがあるため)
  const refresh = useCallback(async () => {
    try {
      const [p, s] = await Promise.all([ChatService.ModelPlaces(), ChatService.ModelSettings(), reload()]);
      setPlaces(p ?? []);
      setSettings(s);
    } catch (err) {
      onError(errorMessage(err));
    }
  }, [reload, onError]);
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
  const remote = !!models.server;
  return (
    <>
      <h2 title={remote ? `aibow-server(${models.server})のモデル。読み込めるのは1つ` : "読み込めるのは1つ(VRAM に1つしか載らない)"}>モデル</h2>
      <ModelTable models={models} disabled={disabled} remote={remote} />

      <h2 className="section" title={remote ? "置き場所のパスは aibow-server の PC のもの" : "モデルを探す場所"}>置き場所</h2>
      <PlaceList places={places} remote={remote} onRemove={p => changePlaces(() => ChatService.RemoveModelPlace(p))} />
      <AddPlace localPaths={settings?.local_paths ?? false} remote={remote} onAdd={p => changePlaces(() => ChatService.AddModelPlace(p))} onError={onError} />

      {settings && <HostSettings s={settings} />}
    </>
  );
}

// ---- モデルの一覧 ----------------------------------------------------------------------

function ModelTable({ models, disabled, remote }: { models: Models; disabled: boolean; remote: boolean }) {
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
          remote={remote}
          onLoad={() => models.switchModel(m.id)}
          onUnload={() => models.unload()}
          onLayers={n => models.setLayers(m.id, n)}
        />
      ))}
    </div>
  );
}

function ModelRow({ m, loaded, layers, totalLayers, disabled, remote, onLoad, onUnload, onLayers }: {
  m: ModelEntry;
  loaded: boolean;
  layers: number;
  totalLayers?: number; // 読み込み中のモデルの層の数(読み込んでいなければ分からない)
  disabled: boolean;
  remote: boolean; // aibow-server 越し(読み込む・降ろすは出さない)
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
        {remote ? null : loaded
          ? <button disabled={disabled} onClick={onUnload} title="VRAM を空ける。次に送信するときに会話のモデルを読み込む">降ろす</button>
          : <button disabled={disabled} onClick={onLoad} title="今のモデルと入れ替える">読み込む</button>}
      </span>
    </div>
  );
}

// ---- 置き場所 ---------------------------------------------------------------------------

function PlaceList({ places, remote, onRemove }: { places: Place[]; remote: boolean; onRemove(path: string): void }) {
  // 手で決めた置き場所(models フォルダ・Ollama)は表示だけ。どこで変えるかを錠の吹き出しに出す
  const lockTitle = (p: Place) => remote
    ? "aibow-server の server.json(modelsDir)か環境変数で変える。起動し直しが要る"
    : p.origin === "ollama" ? "環境変数 OLLAMA_MODELS で変える" : "環境変数 AIBOW_MODELS_DIR で変える";
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

// 置き場所を足す。この PC のパスが使えるときだけ、フォルダ / ファイルを選ぶダイアログのボタンを出す(0015 の 31)
function AddPlace({ localPaths, remote, onAdd, onError }: {
  localPaths: boolean;
  remote: boolean;
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
        title={remote ? "aibow-server の PC のフォルダか GGUF のファイル" : "フォルダか GGUF のファイル"}
        onChange={e => setPath(e.target.value)}
        onKeyDown={e => { if (e.key === "Enter" && path.trim()) add(); }}
      />
      {localPaths && <button onClick={() => choose("dir")} title="フォルダを選ぶ"><LuFolder /> フォルダ</button>}
      {localPaths && <button onClick={() => choose("file")} title="GGUF のファイルを選ぶ"><LuFile /> ファイル</button>}
      <button disabled={!path.trim()} onClick={add}>足す</button>
    </div>
  );
}

// ---- 表示だけの設定 --------------------------------------------------------------------

function HostSettings({ s }: { s: ModelSettingsResult }) {
  const v = s.settings;
  const remote = !!s.server;
  const how = remote
    ? `server.json で変え、aibow-server の起動し直しが要ります(${v.config_file}${v.config_found ? "" : "。まだ無いファイル"})`
    : "環境変数で変え、aibow の起動し直しが要ります";
  const rows: [string, string][] = remote
    ? [
      ["作業領域", `${v.n_ctx.toLocaleString()} トークン`],
      ["ポート", String(v.port)],
      ["最初のモデル", v.model || "一覧の先頭(書式に対応しているもの)"],
      ["llama.cpp", v.lib],
      ["設定ファイル", v.config_found ? v.config_file : `${v.config_file}(無い)`],
    ]
    : [
      ["作業領域", "会話ごと(会話の上限 + 返事の上限)"],
      ["最初のモデル", v.model || "最後に使ったモデル"],
      ["llama.cpp", v.lib],
    ];
  return (
    <>
      <h2 className="section">
        {remote ? "aibow-server の設定" : "起動の設定"}
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
