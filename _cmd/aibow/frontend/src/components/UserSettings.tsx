// 設定の画面の「利用者」のタブ(0018 の決定事項 7。11-0。0031 で一覧 + 詳細にした)。ブラウザの管理者だけに出す。
//
//	[名前で探す…] [すべて | 招待中 | 管理者 | API キーあり]   [+ 利用者を作る]
//	名前              状態      作った日      会話           … 見出しを押すと並べ替え。行を押すと一覧の下に詳細(0031 の決定事項 14・35)
//	▼ 詳細: パスワードのリンク・API キー・その人の会話(新しい順 20 件 + もっと見る)・消す
//
// 一覧は一度に取って画面で探す・絞る・並べ替える(サーバーで探す口は作らない。0031 の決定事項 13)。
// 作る・パスワードと API キーの作り直し・消すは、aibow-server の user コマンドと同じ処理(_cmd/aibow-server の admin.go)。
// 作る・再設定は、パスワードを作らず、本人がパスワードを決めるリンク(1 時間・一度だけ)を出す(0022)。自分には再設定を出さない(自分を締め出さないように)。
// リンクと API キーは、作ったときの返事でだけ受け取り、画面に一度だけ出す(保存されるのはハッシュだけで、あとからは見られない)。
// 管理者の印を付ける / 外すのはコマンドだけ(0018 の決定事項 6)なので、ここでは印を見せるだけにする。
// その人の会話は押すと読むだけで開く(0018 の決定事項 25。読んだことはログに残る)。
// 消すと、その人の会話・プロファイル・設定も消えるので、ログインの名前を打つまで消せない(0031 の決定事項 15・36)。
import { useCallback, useEffect, useMemo, useState } from "react";
import { LuArrowDown, LuArrowUp, LuPlus, LuX } from "react-icons/lu";
import { admin, ChatService, linkURL, me, type AdminUser, type Link } from "../backend";
import type { ConversationRef } from "../../bindings/github.com/secondarykey/aibow";
import { errorMessage } from "../errors";
import { CopyButton } from "./CopyButton";

// Secret は、一度だけ出すリンクか API キー
interface Secret {
  name: string;
  label: string; // 招待のリンク / 再設定のリンク / API キー
  value: string;
  until?: string; // リンクの期限(1 時間)
}

// linkSecret は、リンクの合言葉を、一度だけ出す URL にする
function linkSecret(label: string, l: Link): Secret {
  return { name: l.name, label, value: linkURL(l.token), until: timeOf(l.expiresAt) };
}

function timeOf(at: string): string {
  return new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// 絞り込み
type Filter = "all" | "invited" | "admin" | "key";
const FILTERS: { id: Filter; label: string; title: string; match(u: AdminUser): boolean }[] = [
  { id: "all", label: "すべて", title: "全員", match: () => true },
  { id: "invited", label: "招待中", title: "本人がリンクでパスワードを決めていない人(ログインできない)", match: u => invited(u) },
  { id: "admin", label: "管理者", title: "管理者の印のある人", match: u => u.admin },
  { id: "key", label: "API キーあり", title: "API キーを持っている人", match: u => u.key },
];

// 並べ替え
type SortKey = "name" | "state" | "created" | "conversations";
interface Sort { key: SortKey; desc: boolean }

const invited = (u: AdminUser) => !u.password && !u.local;

// states は、状態の文字の印(アイコンではなく文字。0031 の決定事項 14)
function states(u: AdminUser): string[] {
  const out: string[] = [];
  if (u.admin) out.push("管理者");
  if (invited(u)) out.push("招待中");
  if (u.local) out.push("ログインなし");
  return out;
}

function compare(a: AdminUser, b: AdminUser, key: SortKey): number {
  switch (key) {
    case "state": return states(a).join().localeCompare(states(b).join()) || a.name.localeCompare(b.name);
    case "created": return a.created_at.localeCompare(b.created_at);
    case "conversations": return a.conversations - b.conversations;
  }
  return a.name.localeCompare(b.name);
}

export function UserSettings({ onError, onRead }: { onError(text: string): void; onRead(id: string): void }) {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [sort, setSort] = useState<Sort>({ key: "name", desc: false });
  const [selected, setSelected] = useState<string | null>(null); // 詳細を出している人
  const [creating, setCreating] = useState(false);
  const [secret, setSecret] = useState<Secret | null>(null);

  const reload = useCallback(async () => {
    try {
      setUsers(await admin.users());
    } catch (err) {
      onError(errorMessage(err));
    }
  }, [onError]);
  useEffect(() => { reload(); }, [reload]);

  // run は操作を1つ行い、一覧を取り直す。リンクか API キーが返れば一度だけ出す
  const run = async (f: () => Promise<Secret | null>) => {
    try {
      const s = await f();
      if (s) setSecret(s);
    } catch (err) {
      onError(errorMessage(err));
      return false;
    }
    await reload();
    return true;
  };

  const create = (name: string) => run(async () => {
    const s = linkSecret("招待のリンク", await admin.create(name));
    setCreating(false);
    setSelected(name);
    return s;
  });
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    const match = FILTERS.find(f => f.id === filter)!.match;
    const list = users.filter(u => match(u) && (q === "" || u.name.toLowerCase().includes(q)));
    list.sort((a, b) => compare(a, b, sort.key) * (sort.desc ? -1 : 1));
    return list;
  }, [users, query, filter, sort]);
  const user = users.find(u => u.name === selected) ?? null;

  const head = (key: SortKey, label: string, cls = "") => (
    <button
      type="button"
      className={"model-table-head user-sort " + cls + (sort.key === key ? " current" : "")}
      onClick={() => setSort(s => ({ key, desc: s.key === key ? !s.desc : false }))}
    >
      {label}
      {sort.key === key && (sort.desc ? <LuArrowDown /> : <LuArrowUp />)}
    </button>
  );

  return (
    <>
      <h2 title="管理者の印は、aibow-server の PC で aibow-server user admin / unadmin で付け外しする">利用者</h2>
      {secret && <SecretBox secret={secret} onClose={() => setSecret(null)} />}
      <div className="user-toolbar">
        <input type="text" className="user-search" value={query} placeholder="名前で探す…" onChange={e => setQuery(e.target.value)} />
        <div className="segmented" role="group">
          {FILTERS.map(f => (
            <button key={f.id} type="button" className={filter === f.id ? "current" : ""} title={f.title} aria-pressed={filter === f.id} onClick={() => setFilter(f.id)}>
              {f.label}
            </button>
          ))}
        </div>
        <span className="spacer" />
        <button type="button" className="user-create" onClick={() => setCreating(c => !c)} aria-expanded={creating}><LuPlus /> 利用者を作る</button>
      </div>
      {creating && <AddUser onAdd={create} onCancel={() => setCreating(false)} />}
      <div className="user-table">
        {head("name", "名前")}
        {head("state", "状態")}
        {head("created", "作った日")}
        {head("conversations", "会話", "num")}
        {shown.map(u => (
          <button
            key={u.name}
            type="button"
            className={"model-row user-row" + (u.name === selected ? " current" : "")}
            aria-expanded={u.name === selected}
            onClick={() => setSelected(s => (s === u.name ? null : u.name))}
          >
            <span className="model-name">{u.name}</span>
            <span className="user-states">{states(u).map(s => <span key={s} className="origin-chip">{s}</span>)}</span>
            <span className="user-date">{u.created_at.slice(0, 10)}</span>
            <span className="num">{u.conversations}</span>
          </button>
        ))}
      </div>
      {shown.length === 0 && <p className="hint">見つかりません</p>}
      {user && (
        <UserDetail
          key={user.name}
          user={user}
          onClose={() => setSelected(null)}
          onRead={onRead}
          onError={onError}
          onResetLink={label => run(async () => linkSecret(label, await admin.resetPassword(user.name)))}
          onRenewKey={() => run(async () => {
            const r = await admin.renewKey(user.name);
            return { name: r.name, label: "API キー", value: r.key };
          })}
          onRemove={() => run(async () => {
            await admin.remove(user.name);
            setSelected(null);
            if (secret?.name === user.name) setSecret(null);
            return null;
          })}
        />
      )}
    </>
  );
}

// PAGE は、その人の会話を一度に出す数(0031 の決定事項 16)
const PAGE = 20;

// UserDetail は、一覧で押した人の詳細(一覧の下に出す。0031 の決定事項 14・35)
function UserDetail({ user, onClose, onRead, onError, onResetLink, onRenewKey, onRemove }: {
  user: AdminUser;
  onClose(): void;
  onRead(id: string): void;
  onError(text: string): void;
  onResetLink(label: string): Promise<boolean>;
  onRenewKey(): Promise<boolean>;
  onRemove(): Promise<boolean>;
}) {
  const self = user.name === me.name;
  const [convs, setConvs] = useState<ConversationRef[] | null>(null);
  const [limit, setLimit] = useState(PAGE);
  const [resetting, setResetting] = useState(false);
  const [removing, setRemoving] = useState(false);
  useEffect(() => {
    if (self) return; // 自分の会話は会話の一覧で開く
    ChatService.UserConversations(user.name).then(list => setConvs(list ?? []), err => onError(errorMessage(err)));
  }, [user.name, self, onError]);

  return (
    <section className="user-detail">
      <div className="user-detail-head">
        <h2 className="section">{user.name}</h2>
        <span className="user-states">{states(user).map(s => <span key={s} className="origin-chip">{s}</span>)}</span>
        <span className="spacer" />
        <button className="place-remove" onClick={onClose} title="閉じる" aria-label="閉じる"><LuX /></button>
      </div>

      {!user.local && !self && (
        <div className="user-field">
          <span className="user-field-label">パスワード</span>
          {invited(user) ? (
            <>
              <span className={"user-field-value" + (linkExpired(user) ? " expired" : "")}>{inviteState(user)}</span>
              <button onClick={() => onResetLink("招待のリンク")} title="本人がパスワードを決めるリンク(1 時間・一度だけ)を出し直す。前のリンクは使えなくなる">リンクを出し直す</button>
            </>
          ) : resetting ? (
            <>
              <span className="confirm-text" title="その人のログイン中の画面とアプリも切れる。リンクでパスワードを決めるまでログインできない">今のパスワードは使えなくなります</span>
              <button className="danger" onClick={() => { setResetting(false); onResetLink("再設定のリンク"); }}>リンクを出す</button>
              <button onClick={() => setResetting(false)}>やめる</button>
            </>
          ) : (
            <button onClick={() => setResetting(true)} title="本人がパスワードを決め直すリンク(1 時間・一度だけ)を出す。出した時点で今のパスワードは使えなくなる">決め直すリンクを出す</button>
          )}
        </div>
      )}

      <div className="user-field">
        <span className="user-field-label">API キー</span>
        <span className="user-field-value">{user.key ? "あり" : "なし"}</span>
        <button onClick={onRenewKey} title={user.key ? "新しい API キーを作る(前のキーは使えなくなる)" : "API キーを作る"}>{user.key ? "作り直す" : "作る"}</button>
      </div>

      {!self && (
        <>
          <h3 className="user-sub" title="押すと読むだけで開く(読んだことは記録に残る)">会話({user.conversations})</h3>
          {convs === null ? null : convs.length === 0 ? (
            <p className="hint">会話がありません</p>
          ) : (
            <div className="user-convs">
              {convs.slice(0, limit).map(c => (
                <button key={c.id} type="button" className="user-conv" onClick={() => onRead(c.id)}>
                  <span className="user-conv-title">{c.title || "新しい会話"}</span>
                  <span className="user-date">{c.updated_at.slice(0, 16).replace("T", " ")}</span>
                </button>
              ))}
              {convs.length > limit && <button type="button" className="more" onClick={() => setLimit(l => l + PAGE)}>もっと見る(残り {convs.length - limit} 件)</button>}
            </div>
          )}
        </>
      )}

      {!user.local && !self && (
        removing
          ? <RemoveConfirm user={user} onRemove={onRemove} onCancel={() => setRemoving(false)} />
          : <div className="user-remove"><button className="danger-outline" onClick={() => setRemoving(true)}>この人を消す</button></div>
      )}
    </section>
  );
}

function linkExpired(u: AdminUser): boolean {
  return !u.link_expires_at || new Date(u.link_expires_at).getTime() <= Date.now();
}

// inviteState は、招待中の人のリンクの様子
function inviteState(u: AdminUser): string {
  if (!u.link_expires_at) return "招待のリンクがありません";
  if (linkExpired(u)) return "招待のリンクの期限が切れています";
  return `招待のリンクは ${timeOf(u.link_expires_at)} まで`;
}

// RemoveConfirm は、消すときの確かめ(ログインの名前を打つまで消せない。0031 の決定事項 15・36)
function RemoveConfirm({ user, onRemove, onCancel }: { user: AdminUser; onRemove(): Promise<boolean>; onCancel(): void }) {
  const [typed, setTyped] = useState("");
  return (
    <div className="user-remove confirming">
      <p className="confirm-text">
        {user.name} を消します。会話 {user.conversations} 件・プロファイル・人ごとの設定・API キーも消え、共有の相手からも外れます。元に戻せません。
      </p>
      <div className="user-remove-row">
        <input type="text" value={typed} placeholder={user.name} aria-label="消す人のログインの名前" onChange={e => setTyped(e.target.value)} autoFocus />
        <button className="danger" disabled={typed !== user.name} onClick={onRemove}>消す</button>
        <button onClick={onCancel}>やめる</button>
      </div>
    </div>
  );
}

// SecretBox は、作ったリンクか API キーを一度だけ出す(閉じたら、もう見られない)
function SecretBox({ secret, onClose }: { secret: Secret; onClose(): void }) {
  return (
    <div className="secret-box" title="今だけ表示する。閉じたらもう見られない(忘れたら作り直す)。リンクは本人に渡し、開いてパスワードを決めてもらう">
      <span className="secret-label">{secret.name} の{secret.label}{secret.until && `(${secret.until} まで・一度だけ)`}</span>
      <code className="secret-value">{secret.value}</code>
      <CopyButton text={secret.value} className="place-remove" />
      <button className="place-remove" onClick={onClose} title="閉じる" aria-label="閉じる"><LuX /></button>
    </div>
  );
}

function AddUser({ onAdd, onCancel }: { onAdd(name: string): Promise<boolean>; onCancel(): void }) {
  const [name, setName] = useState("");
  const add = async () => {
    if (await onAdd(name.trim())) setName("");
  };
  return (
    <div className="place-add">
      <input
        type="text"
        value={name}
        autoFocus
        placeholder="ログインの名前"
        title="ログインの名前(1〜32 文字。空白は使えない)。作ると、本人がパスワードを決める招待のリンクを一度だけ出す"
        onChange={e => setName(e.target.value)}
        onKeyDown={e => {
          if (e.key === "Enter" && name.trim()) add();
          if (e.key === "Escape") {
            e.stopPropagation(); // 重ねて出した画面を Esc で閉じる仕組み(App.tsx)まで届けない
            onCancel();
          }
        }}
      />
      <button disabled={!name.trim()} onClick={add}>作る</button>
      <button onClick={onCancel}>やめる</button>
    </div>
  );
}
