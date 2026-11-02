// MCP のサーバーを足す・変える・消す・試しにつなぐ部品(0034)。設定の画面の「MCP」のタブ(アプリだけ)で使う。
//
// 一覧(名前・種類・command か url・どこに書いたか)と、行を押すと下に開くフォーム、JSON を貼る欄、試しにつなぐ、を1つにしている。
// Go を呼ぶ口(一覧・足す・変える・消す・試す・JSON を読み取る)は外から受け取る(api)。
// aibow-server(server.json)の分も、あとで同じ部品に口だけ替えて使う見込み(0034 の決定事項 4。ServerTools・ProfileTools と同じ考え方)。
//
// 画面から変えられるのは mcp.json の分だけ。settings.json に手で書いたサーバーは「settings.json」の印を付けて出し、試しにつなぐだけできる(決定事項 13)。
// headers と env の値は Go が伏せて返す(キーだけ)。フォームでも伏せ字で出し、空のまま保存したら前の値のまま(決定事項 10)。
// 名前は変えられない(会話の設定と tools.json のキーのため。決定事項 11)。
import { useCallback, useEffect, useState } from "react";
import { LuClipboardPaste, LuLoaderCircle, LuPlug, LuPlus, LuTrash2, LuX } from "react-icons/lu";
import type { KnownTool, MCPServerEntry, MCPServerInput, MCPServerList, ParsedMCPServer } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerConfig } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { errorMessage } from "../errors";

// McpEditorAPI は、Go を呼ぶ口(アプリでは ChatService のメソッド)。足す・変える・消すは、画面のお知らせに出す文を返す
export interface McpEditorAPI {
  list(): Promise<MCPServerList>;
  add(servers: MCPServerInput[]): Promise<string[] | null>;
  update(name: string, config: ServerConfig): Promise<string[] | null>;
  remove(name: string): Promise<string[] | null>;
  try(name: string, config: ServerConfig, add: boolean): Promise<KnownTool[] | null>;
  parse(text: string): Promise<ParsedMCPServer[] | null>;
}

interface Props {
  api: McpEditorAPI;
  onChanged(): void;           // 足した・変えた・消した(会話で選べるサーバーとメーターを読み直す)
  onNotice(text: string): void; // お知らせ(足したサーバーは最初から使わない・動いていたので止めた)
}

// KV は env・headers の1組。saved は保存済みのキー(値は伏せてあり、空のままなら前の値を使う)
interface KV { key: string; value: string; saved: boolean }

// Draft はフォームの中身
interface Draft {
  name: string;
  kind: "command" | "url";
  command: string;
  args: string; // 1行に1つ
  env: KV[];
  url: string;
  headers: KV[];
  startup: string; // 秒(空なら 60)
  call: string;
  type?: string; // 貼った JSON に書いてあったもの(フォームには出さず、種類を変えたら捨てる)
}

// 開いているフォーム。add は足す(名前を打てる)、edit は mcp.json のものを変える、view は settings.json のもの(試すだけ)
type Mode = { kind: "add" } | { kind: "edit" | "view"; name: string };

const EMPTY: Draft = { name: "", kind: "command", command: "", args: "", env: [], url: "", headers: [], startup: "", call: "" };

function toKV(m: { [_ in string]?: string } | null | undefined, saved: boolean): KV[] {
  return Object.entries(m ?? {}).map(([key, value]) => ({ key, value: saved ? "" : value ?? "", saved }));
}

// draftOf は、Go の設定をフォームの中身にする。saved なら env・headers の値は伏せてある(Go が空で返す)
function draftOf(name: string, c: ServerConfig, saved: boolean): Draft {
  return {
    name,
    kind: c.url ? "url" : "command",
    command: c.command ?? "",
    args: (c.args ?? []).join("\n"),
    env: toKV(c.env, saved),
    url: c.url ?? "",
    headers: toKV(c.headers, saved),
    startup: c.startupTimeout ? String(c.startupTimeout) : "",
    call: c.callTimeout ? String(c.callTimeout) : "",
    type: c.type || undefined,
  };
}

function fromKV(kv: KV[]): { [_ in string]?: string } | undefined {
  const m = Object.fromEntries(kv.filter(e => e.key.trim() !== "").map(e => [e.key.trim(), e.value]));
  return Object.keys(m).length > 0 ? m : undefined;
}

// configOf は、フォームの中身を Go の設定にする。種類に合わない欄は送らない(書いてあっても使わない)
function configOf(d: Draft): ServerConfig {
  const seconds = (s: string) => (s.trim() === "" ? undefined : Number(s));
  const c: ServerConfig = { type: d.type, startupTimeout: seconds(d.startup), callTimeout: seconds(d.call) };
  if (d.kind === "url") {
    c.url = d.url.trim();
    c.headers = fromKV(d.headers);
  } else {
    c.command = d.command.trim();
    c.args = d.args.split(/\r?\n/u).map(a => a.trim()).filter(a => a !== "");
    c.env = fromKV(d.env);
  }
  return c;
}

// summary は、一覧に出すつなぎ方(command と args か、url)
function summary(c: ServerConfig): string {
  return c.url ? c.url : [c.command ?? "", ...(c.args ?? [])].join(" ");
}

export function McpEditor({ api, onChanged, onNotice }: Props) {
  const [list, setList] = useState<MCPServerList | null>(null);
  const [error, setError] = useState("");
  const [mode, setMode] = useState<Mode | null>(null);
  const [draft, setDraft] = useState<Draft>(EMPTY);
  const [pasteOpen, setPasteOpen] = useState(false);

  const reload = useCallback(() => {
    api.list().then(setList, err => setError(errorMessage(err)));
  }, [api]);
  useEffect(reload, [reload]);

  const open = (m: Mode | null, d: Draft = EMPTY) => {
    setMode(m);
    setDraft(d);
    setError("");
  };
  const select = (s: MCPServerEntry) => {
    if (mode && mode.kind !== "add" && mode.name === s.name && (mode.kind === "view") === (s.source === "settings")) {
      open(null); // もう一度押したら閉じる
      return;
    }
    open({ kind: s.source === "settings" ? "view" : "edit", name: s.name }, draftOf(s.name, s.config, true));
  };

  // done は、保存したあと(お知らせを出し、一覧と会話の側を読み直す)
  const done = (notices: string[] | null) => {
    (notices ?? []).forEach(onNotice);
    reload();
    onChanged();
  };

  const servers = list?.servers ?? [];
  const locked = !!list?.error; // mcp.json が読めなければ、画面からは保存しない(壊れたファイルを上書きしない)

  return (
    <div className="mcp-editor">
      {(error || list?.error) && <div className="msg error">{error || list?.error}</div>}
      <div className="user-toolbar">
        <button type="button" className="user-create" disabled={locked} onClick={() => open({ kind: "add" })} title="フォームに書いて足す">
          <LuPlus /> 足す
        </button>
        <button type="button" className="user-create" disabled={locked} onClick={() => setPasteOpen(o => !o)} title="MCP サーバーの説明にある JSON({&quot;mcpServers&quot;: …})を貼って足す">
          <LuClipboardPaste /> JSON を貼る
        </button>
      </div>

      {pasteOpen && (
        <PasteBox
          api={api}
          onClose={() => setPasteOpen(false)}
          onForm={d => { setPasteOpen(false); open({ kind: "add" }, d); }}
          onAdded={n => { setPasteOpen(false); done(n); }}
        />
      )}

      {servers.length === 0 ? (
        <p className="tool-none">(MCP のサーバーはまだありません)</p>
      ) : (
        <div className="mcp-table">
          {servers.map(s => {
            const current = !!mode && mode.kind !== "add" && mode.name === s.name && (mode.kind === "view") === (s.source === "settings");
            return (
              <button
                key={s.source + ":" + s.name}
                type="button"
                className={"user-row model-row mcp-row" + (current ? " current" : "") + (s.shadowed ? " shadowed" : "")}
                onClick={() => select(s)}
                title={s.source === "settings" ? "settings.json に手で書いたサーバー(画面からは変えられません。試しにつなぐことはできます)" : "画面から足したサーバー(押すと変える・消す)"}
              >
                <span className="mcp-name">{s.name}</span>
                <span className="origin-chip">{s.config.url ? "url" : "command"}</span>
                <span className="mcp-summary">{summary(s.config)}</span>
                <span>
                  {s.source === "settings" && <span className="origin-chip">settings.json</span>}
                  {s.shadowed && <span className="origin-chip" title="settings.json に同じ名前があるので、こちらは使われていません">使われていない</span>}
                </span>
              </button>
            );
          })}
        </div>
      )}

      {mode && (
        <ServerForm
          key={mode.kind === "add" ? "add" : mode.kind + ":" + mode.name}
          api={api}
          mode={mode}
          draft={draft}
          locked={locked}
          onChange={setDraft}
          onClose={() => open(null)}
          onSaved={n => {
            open(null);
            done(n);
          }}
        />
      )}

      {list && (
        <p className="hint tool-path" title="画面から足したサーバーの保存先(手で書いた settings.json は変えません)">
          {list.path}
        </p>
      )}
    </div>
  );
}

// ServerForm は、足す・変えるフォーム(settings.json のものは見るだけ)。試しにつなぐは、保存する前の中身で行う
function ServerForm({ api, mode, draft, locked, onChange, onClose, onSaved }: {
  api: McpEditorAPI;
  mode: Mode;
  draft: Draft;
  locked: boolean;
  onChange(d: Draft): void;
  onClose(): void;
  onSaved(notices: string[] | null): void;
}) {
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [trying, setTrying] = useState(false);
  const [tried, setTried] = useState<{ tools?: KnownTool[]; error?: string } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const view = mode.kind === "view";
  const set = (patch: Partial<Draft>) => {
    onChange({ ...draft, ...patch });
    setTried(null); // 中身を変えたら、前に試した結果は当てにならない
  };

  const run = (p: Promise<string[] | null>) => {
    setSaving(true);
    setError("");
    p.then(onSaved, err => setError(errorMessage(err))).finally(() => setSaving(false));
  };
  const save = () => {
    const config = configOf(draft);
    run(mode.kind === "add" ? api.add([{ name: draft.name.trim(), config }]) : api.update(draft.name, config));
  };
  const tryConnect = () => {
    setTrying(true);
    setTried(null);
    api.try(draft.name, configOf(draft), mode.kind === "add").then(
      tools => setTried({ tools: tools ?? [] }),
      err => setTried({ error: errorMessage(err) }),
    ).finally(() => setTrying(false));
  };

  return (
    <section className="user-detail mcp-form">
      <div className="user-detail-head">
        <h2 className="section">{mode.kind === "add" ? "MCP のサーバーを足す" : draft.name}</h2>
        {view && <span className="origin-chip">settings.json</span>}
        <span className="spacer" />
        <button type="button" className="place-remove" onClick={onClose} title="閉じる"><LuX /></button>
      </div>
      {error && <div className="msg error">{error}</div>}

      {mode.kind === "add" && (
        <>
          <label htmlFor="mcp-name" title="ツールの名前の頭(名前__ツール)と、会話で選ぶときの名前になる。あとから変えられない">名前</label>
          <input id="mcp-name" type="text" value={draft.name} onChange={e => set({ name: e.target.value })} placeholder="filesystem" autoFocus />
        </>
      )}

      <div className="segmented mcp-kind" role="group">
        {(["command", "url"] as const).map(k => (
          <button
            key={k}
            type="button"
            className={draft.kind === k ? "current" : ""}
            disabled={view}
            onClick={() => set({ kind: k, type: undefined })}
            title={k === "command" ? "aibow がサーバーを起動する(標準入出力で話す)" : "動いているサーバーに HTTP でつなぐ(Streamable HTTP)"}
          >
            {k}
          </button>
        ))}
      </div>

      {draft.kind === "command" ? (
        <>
          <label htmlFor="mcp-command">command</label>
          <input id="mcp-command" type="text" value={draft.command} disabled={view} onChange={e => set({ command: e.target.value })} placeholder="npx" />
          <label htmlFor="mcp-args" title="1行に1つ。${workdir} は会話の作業フォルダに置き換わる">args</label>
          <textarea id="mcp-args" rows={3} value={draft.args} disabled={view} onChange={e => set({ args: e.target.value })} placeholder={"-y\n@modelcontextprotocol/server-filesystem\n${workdir}"} />
          <KVEditor label="env" rows={draft.env} disabled={view} onChange={env => set({ env })} />
        </>
      ) : (
        <>
          <label htmlFor="mcp-url">url</label>
          <input id="mcp-url" type="text" value={draft.url} disabled={view} onChange={e => set({ url: e.target.value })} placeholder="https://example.com/mcp" />
          <KVEditor label="headers" rows={draft.headers} disabled={view} onChange={headers => set({ headers })} />
        </>
      )}
      {(draft.env.length > 0 || draft.headers.length > 0) && !view && (
        <p className="hint">mcp.json には平文で書かれます(settings.json と同じ)。</p>
      )}

      <div className="mcp-timeouts">
        <label title="起動してつながる(ツールの一覧を受け取る)までの上限の秒数。空なら 60">
          startupTimeout
          <input type="number" min={1} value={draft.startup} disabled={view} onChange={e => set({ startup: e.target.value })} placeholder="60" />
        </label>
        <label title="ツール1回の実行の上限の秒数。空なら 60">
          callTimeout
          <input type="number" min={1} value={draft.call} disabled={view} onChange={e => set({ call: e.target.value })} placeholder="60" />
        </label>
      </div>

      {tried && (
        tried.error
          ? <div className="msg error mcp-tried">{tried.error}</div>
          : <p className="mcp-tried ok" title={(tried.tools ?? []).map(t => `${t.name}: ${t.description}`).join("\n")}>
              ツール {tried.tools?.length ?? 0} 個: {(tried.tools ?? []).map(t => t.name).join(", ")}
            </p>
      )}

      <div className="mcp-actions">
        <button type="button" onClick={tryConnect} disabled={trying || saving || draft.name.trim() === ""} title="保存する前の中身で、一時的に起動してツールの一覧を受け取る(何も保存しません)">
          {trying ? <LuLoaderCircle className="spin" /> : <LuPlug />} 試しにつなぐ
        </button>
        {!view && (
          <button type="button" className="apply" onClick={save} disabled={saving || locked}>
            {mode.kind === "add" ? "足す" : "保存"}
          </button>
        )}
        <span className="spacer" />
        {mode.kind === "edit" && (
          confirmDelete ? (
            <>
              <span className="confirm-text">消しますか?</span>
              <button type="button" className="danger" disabled={saving || locked} onClick={() => run(api.remove(draft.name))}>消す</button>
              <button type="button" onClick={() => setConfirmDelete(false)}>やめる</button>
            </>
          ) : (
            <button type="button" className="danger-outline" disabled={saving || locked} onClick={() => setConfirmDelete(true)} title="mcp.json から消す(ツールの扱いの設定は残す)">
              <LuTrash2 /> 消す
            </button>
          )
        )}
      </div>
    </section>
  );
}

// KVEditor は、env・headers のキーと値の組。値は伏せ字(保存済みのキーは、空のままなら前の値)
function KVEditor({ label, rows, disabled, onChange }: { label: string; rows: KV[]; disabled: boolean; onChange(rows: KV[]): void }) {
  const put = (i: number, patch: Partial<KV>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <>
      <div className="field-label">
        <label>{label}</label>
        {!disabled && (
          <button type="button" className="place-remove" onClick={() => onChange([...rows, { key: "", value: "", saved: false }])} title={`${label} を足す`}>
            <LuPlus />
          </button>
        )}
      </div>
      {rows.map((r, i) => (
        <div key={i} className="mcp-kv">
          <input type="text" value={r.key} disabled={disabled || r.saved} onChange={e => put(i, { key: e.target.value })} placeholder="名前" />
          <input
            type="password"
            autoComplete="off"
            value={r.value}
            disabled={disabled}
            onChange={e => put(i, { value: e.target.value })}
            placeholder={r.saved ? "••••••" : "値"}
            title={r.saved ? "保存した値は伏せてある。空のままなら前の値のまま" : undefined}
          />
          {!disabled && (
            <button type="button" className="place-remove" onClick={() => onChange(rows.filter((_, j) => j !== i))} title="外す"><LuX /></button>
          )}
        </div>
      ))}
    </>
  );
}

// PasteBox は、JSON を貼る欄。1つならフォームに入れ、いくつかなら足すものを確かめてから足す(0034 の決定事項 7)
function PasteBox({ api, onClose, onForm, onAdded }: {
  api: McpEditorAPI;
  onClose(): void;
  onForm(d: Draft): void;
  onAdded(notices: string[] | null): void;
}) {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [parsed, setParsed] = useState<ParsedMCPServer[] | null>(null);
  const [checked, setChecked] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState(false);

  const read = () => {
    setError("");
    setParsed(null);
    api.parse(text).then(list => {
      const l = list ?? [];
      // 1つで、足せるか名前を打つものなら、フォームに入れる(形の違うものもフォームで直せるように)
      if (l.length === 1 && (!l[0].error || l[0].name === "")) {
        onForm(draftOf(l[0].name, l[0].config, false));
        return;
      }
      setParsed(l);
      setChecked(Object.fromEntries(l.map(p => [p.name, !p.error])));
    }, err => setError(errorMessage(err)));
  };
  const add = () => {
    const servers = (parsed ?? []).filter(p => !p.error && checked[p.name]).map(p => ({ name: p.name, config: p.config }));
    setBusy(true);
    setError("");
    api.add(servers).then(onAdded, err => setError(errorMessage(err))).finally(() => setBusy(false));
  };
  const count = (parsed ?? []).filter(p => !p.error && checked[p.name]).length;

  return (
    <section className="user-detail mcp-paste">
      <div className="user-detail-head">
        <h2 className="section">JSON を貼る</h2>
        <span className="spacer" />
        <button type="button" className="place-remove" onClick={onClose} title="閉じる"><LuX /></button>
      </div>
      {error && <div className="msg error">{error}</div>}
      <textarea
        rows={6}
        value={text}
        onChange={e => { setText(e.target.value); setParsed(null); }}
        placeholder={'{\n  "mcpServers": {\n    "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "${workdir}"] }\n  }\n}'}
        spellCheck={false}
        autoFocus
      />
      {!parsed && (
        <div className="mcp-actions">
          <button type="button" onClick={read} disabled={text.trim() === ""}>読み取る</button>
        </div>
      )}
      {parsed && (
        <>
          <div className="mcp-parsed">
            {parsed.map(p => (
              <label key={p.name} className={"check" + (p.error ? " unused" : "")} title={p.error || summary(p.config)}>
                <input type="checkbox" checked={!p.error && !!checked[p.name]} disabled={!!p.error} onChange={e => setChecked(c => ({ ...c, [p.name]: e.target.checked }))} />
                <span className="mcp-name">{p.name}</span>
                {p.error ? <span className="mcp-parse-error">{p.error}</span> : <span className="mcp-summary">{summary(p.config)}</span>}
              </label>
            ))}
          </div>
          <div className="mcp-actions">
            <button type="button" className="apply" onClick={add} disabled={busy || count === 0}>{count} 個を足す</button>
          </div>
        </>
      )}
    </section>
  );
}
