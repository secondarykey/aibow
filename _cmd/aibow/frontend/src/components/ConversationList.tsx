// 左側: 会話の一覧(更新の新しい順)。新規作成・切り替え・名前の変更・削除。
// 名前の変更はダブルクリックか F2(F2 は App.tsx が受けて renamingId を決める)。
// 会話の設定の歯車は、一覧を畳んでいても開けるよう、入力欄の下(メーターの右)に置く(App.tsx)。
// 下にアプリの名前とバージョン、右に設定の画面の開閉ボタン(アイコンだけ)。管理できる人には、その左に管理の画面の盾(0032)。
// ウィンドウの上から下までの列。上のハンバーガーで一覧を畳める。畳んだときもアイコンだけの細い列を残し、
// ハンバーガー・新規・設定はそこに集める。上の空いているところはタイトルバーと同じくドラッグでウィンドウを動かせる。
// 「+ 新規」は一覧の一番上の行に置く(スクロールしても隠れないよう、一覧の外に固定する)。
// 開いているときも畳んだときも同じアイコンを使い、開いているときだけ文字を添える
//
// 会話の名前の左の印(丸):
//   生成中   … 青の点滅
//   確認待ち … 橙(点滅しない。ツールを実行する前の確認に答えるまで、送信が止まっている)
//   エラー   … 赤(点滅しない。ほかの会話を見ている間にエラーで終わった。その会話を開くと消え、エラーが表示に出る)
//   終わった … 青(点滅しない。ほかの会話を見ている間に返事が終わった。その会話を開くと消える)
// 一覧を畳んでいるときは、いちばん気にしてほしい印(確認待ち → エラー → 生成中 → 終わった)をハンバーガーに出す
//
// 並べ方(0038):一覧の上(ハンバーガーの右端)のメニューで、新しい順とワークスペースごとを切り替える(アプリだけ。conversationGroups.ts)。
// ワークスペースごとのときは、まとまりの見出しで開け閉めでき(PC ごとに覚える)、見出しの「+」でそのワークスペースを選んだ新しい会話を始める。
// 開いた会話・生成中の会話のまとまりは自動で開く(畳んだまとまりの中に隠れないように)。畳んだ見出しには、中の会話のいちばん気にしてほしい印を出す。
// 上の「+ 新規」は今までどおりワークスペースを選ばない(0035:前の会話のフォルダを引き継がせない)
//
// 共有(0018 の 11-2):共有した会話は名前の右に共有のアイコン。ほかの人に共有された会話は、一覧の下の「共有された会話」の欄
// (開閉できる・持ち主の名前付き。押すと読むだけで開く。「自分に共有」と「全員に共有」に分け、持ち主とタイトルで絞る。0031 の決定事項 17)。ログインが要る aibow-server につないでいるときは、
// 一番下の名前の横に「管理者が読めます」を常に出す(決定事項 23。畳んだときは目のアイコンだけ)
import { useEffect, useMemo, useRef, useState } from "react";
import { ChatService, isBrowser, me } from "../backend";
import { canLogout, signedIn, signOut } from "./AccountSettings";
import { showName } from "../directory";
import type { KeyboardEvent } from "react";
import { LuCheck, LuChevronDown, LuChevronRight, LuEye, LuFolderTree, LuList, LuMenu, LuPlus, LuSettings, LuShare2, LuShield, LuUser } from "react-icons/lu";
import { useMenuDismiss } from "./SplitButton";
import { toggleMaximiseOnDoubleClick } from "./TitleBar";
import type { Conversation, ConversationRef } from "../../bindings/github.com/secondarykey/aibow";
import type { WorkspaceView } from "../../bindings/github.com/secondarykey/aibow/ui";
import { groupByWorkspace, LIST_GROUPINGS, useClosedGroups, useListGrouping } from "../conversationGroups";
import { unappliedCount } from "../worktree";
import type { ConversationGroup, ListGrouping } from "../conversationGroups";
import type { Ended } from "../useChat";

interface Props {
  list: Conversation[];
  currentId: string | null;
  generatingId: string | null;
  waitingId: string | null; // ツールの実行前の確認を待っている会話(生成中の印を「確認待ち」にする)
  ended: Record<string, Ended>; // ほかの会話を見ている間に送信が終わった会話と終わり方(開くと消える。useChat が持つ)
  disabled: boolean;
  onOpen(id: string): void;
  onNew(): void;
  onDelete(id: string): void;
  onRename(id: string, title: string): void;
  renamingId: string | null; // 名前を変えている会話(入力欄にする)
  onStartRename(id: string): void;
  onEndRename(): void;
  settingsOpen: boolean;
  onToggleSettings(): void;
  onOpenAccount(): void; // 設定の画面を「アカウント」のタブで開く(左下の名前のメニュー)
  adminOpen: boolean;    // 管理の画面を開いているか(0032)
  canAdmin: boolean;     // 管理の画面の盾を出すか(管理者か、サーバーの PC の上から開いたブラウザ)
  onToggleAdmin(): void; // 歯車の隣の盾で、管理の画面を開く / 閉じる
  open: boolean; // 一覧を開いているか
  onToggleOpen(): void;
  width: number; // 開いているときの幅(px)。一覧の右の仕切り(Splitter)で変えられる
  shared: ConversationRef[]; // ほかの人に共有された会話(0018 の 11-2)
  readingId: string | null;  // 読むだけで開いているほかの人の会話
  onOpenShared(id: string): void;
  workspaces: WorkspaceView[]; // この PC のワークスペース(並べ方がワークスペースごとのとき。0038)
  currentWorkspace: string | null; // 開いている会話のワークスペース(一覧を読み直す前に変えたときも、すぐにまとまりを移すため)
  onNewIn(workspace: string): void; // まとまりの「+」:そのワークスペースを選んだ新しい会話の画面に移る
}

export function ConversationList({
  list, currentId, generatingId, waitingId, ended, disabled, onOpen, onNew, onDelete, onRename, renamingId, onStartRename, onEndRename,
  settingsOpen, onToggleSettings, onOpenAccount, adminOpen, canAdmin, onToggleAdmin, open, onToggleOpen, width, shared, readingId, onOpenShared,
  workspaces, currentWorkspace, onNewIn,
}: Props) {
  const [sharedOpen, setSharedOpen] = useState(true);
  // 削除の確認を出している会話。消すと戻せないので、もう一度押してもらう
  const [confirming, setConfirming] = useState<string | null>(null);
  // 消そうとしている会話の worktree の、まだ本体に取り込んでいないファイルの数(0039。消すと worktree ごと消えるので、確認の文に出す)
  const [unapplied, setUnapplied] = useState(0);
  const startConfirm = (c: Conversation) => {
    setConfirming(c.id);
    setUnapplied(0);
    if (c.settings?.worktree) unappliedCount(c.id).then(n => setUnapplied(n));
  };

  const markOf = (id: string): Mark | null =>
    id === generatingId ? (id === waitingId ? "waiting" : "generating") : ended[id] ?? null;
  // 畳んでいるときにハンバーガーに出す印。消した会話が ended に残っていても出さないよう、一覧にある会話だけを見る
  const marks = list.map(c => markOf(c.id));
  const menuMark = open ? null : MARK_ORDER.find(m => marks.includes(m)) ?? null;

  const menu = (
    <button
      className="icon-btn menu-btn"
      title={(open ? "会話の一覧を閉じる" : "会話の一覧を開く") + (menuMark ? `(${MARK_BADGE_LABEL[menuMark]})` : "")}
      onClick={onToggleOpen}
    >
      <LuMenu />
      {menuMark && <span className={"dot badge " + menuMark} />}
    </button>
  );
  // 並べ方(0038)。ワークスペースごとのときだけ、まとまりを作る
  const [grouping, setGrouping] = useListGrouping();
  const groups = useMemo(
    () => grouping === "workspace"
      ? groupByWorkspace(list, workspaces, currentId !== null && currentWorkspace !== null ? { id: currentId, workspace: currentWorkspace } : undefined)
      : [],
    [grouping, list, workspaces, currentId, currentWorkspace],
  );
  const [closed, setClosed] = useClosedGroups();
  useRevealGroups(groups, currentId, generatingId, renamingId, currentWorkspace, closed, setClosed);

  const settingsCls = settingsOpen ? "settings active" : "settings";
  // 管理の画面の入口(盾)。管理できる人にだけ、歯車の隣に出す(0032 の決定事項 5)
  const adminBtn = canAdmin && (
    <button className={"icon-btn admin" + (adminOpen ? " active" : "")} title="管理" onClick={onToggleAdmin}><LuShield /></button>
  );

  if (!open) {
    return (
      <nav className="conversations collapsed" onDoubleClick={toggleMaximiseOnDoubleClick}>
        {menu}
        <button className="icon-btn" title="新しい会話" disabled={disabled} onClick={onNew}><LuPlus /></button>
        <span className="spacer" />
        {me.login && <span className="admin-notice icon" title={ADMIN_NOTICE}><LuEye /></span>}
        {adminBtn}
        <button className={"icon-btn " + settingsCls} title="設定" onClick={onToggleSettings}><LuSettings /></button>
      </nav>
    );
  }

  // row は会話の1行(名前・印・削除)。新しい順でも、ワークスペースごとのまとまりの中でも同じもの
  const row = (c: Conversation) => {
    const title = c.title || "新しい会話";
    const generating = c.id === generatingId;
    const mark = markOf(c.id);
    const cls = ["conversation", c.id === currentId && "current", generating && "generating"].filter(Boolean).join(" ");
    if (renamingId === c.id) {
      return (
        <li key={c.id} className={cls + " editing"}>
          <TitleInput
            initial={c.title}
            onDone={title => {
              onEndRename();
              if (title !== null && title.trim() && title.trim() !== c.title) onRename(c.id, title);
            }}
          />
        </li>
      );
    }
    if (confirming === c.id) {
      return (
        <li key={c.id} className={cls + " confirming"}>
          <span className="confirm-text" title={unapplied > 0 ? "この会話の worktree とブランチも消える" : undefined}>
            {unapplied > 0 ? `取り込んでいない差分(${unapplied} ファイル)も消えます。削除しますか?` : "削除しますか?"}
          </span>
          <button className="danger" onClick={() => { setConfirming(null); onDelete(c.id); }}>削除</button>
          <button onClick={() => setConfirming(null)}>やめる</button>
        </li>
      );
    }
    return (
      <li key={c.id} className={cls}>
        <button
          className="title"
          title={`${title}(ダブルクリックか F2 で名前を変更)`}
          disabled={disabled}
          onClick={() => onOpen(c.id)}
          onDoubleClick={() => onStartRename(c.id)}
        >
          {title}
          {c.share && <LuShare2 className="share-mark" title={c.share === "all" ? "全員に共有している" : "指定した人に共有している"} />}
        </button>
        {generating && mark ? (
          // 送信している間(生成中・確認待ち)は削除できないので、削除の場所に印を出す(2026-10-31、ユーザーの指示)
          <span className="running-mark" title={MARK_LABEL[mark] + "(終わるまで削除できません)"}>
            <span className={"dot " + mark} />
          </span>
        ) : (
          // 終わったあとの印(終わった・エラー)も同じ場所に出し、マウスを乗せたときだけ × に替える(削除はできるので)
          <button
            className={"delete" + (mark ? " has-mark" : "")}
            title={mark ? `削除(${MARK_LABEL[mark]})` : "削除"}
            disabled={disabled}
            onClick={() => startConfirm(c)}
          >
            {mark && <span className={"dot " + mark} />}
            <span className="x">×</span>
          </button>
        )}
      </li>
    );
  };

  // groupHead はワークスペースごとのときのまとまりの見出し。押すと開け閉めする。右の「+」でそのワークスペースの新しい会話を始める。
  // 畳んでいるときは、中の会話のいちばん気にしてほしい印を出す(ハンバーガーの印と同じ考え方)
  const groupHead = (g: ConversationGroup) => {
    const isClosed = closed.has(g.key);
    const groupMark = isClosed ? MARK_ORDER.find(m => g.list.some(c => markOf(c.id) === m)) ?? null : null;
    return (
      <li key={"group:" + g.key} className={"conv-group-head" + (g.key === "missing" || g.key === "none" ? " other" : "")}>
        <button
          className="conv-group-toggle"
          title={g.title}
          aria-expanded={!isClosed}
          onClick={() => setClosed(g.key, !isClosed)}
        >
          {isClosed ? <LuChevronRight /> : <LuChevronDown />}
          <span className="conv-group-name">{g.label}</span>
          <span className="conv-group-count">{g.list.length}</span>
          {groupMark && <span className={"dot " + groupMark} title={MARK_BADGE_LABEL[groupMark]} />}
        </button>
        {g.workspace && (
          <button className="conv-group-new" title={`「${g.label}」で新しい会話`} disabled={disabled} onClick={() => onNewIn(g.workspace)}>
            <LuPlus />
          </button>
        )}
      </li>
    );
  };

  return (
    <nav className="conversations" style={{ width }}>
      <div className="nav-top" onDoubleClick={toggleMaximiseOnDoubleClick}>
        {menu}
        {/* 並べ方はワークスペースでまとめるためのもの。ブラウザの会話はワークスペースを使わないので出さない(0038) */}
        {!isBrowser && <GroupingMenu value={grouping} onChange={setGrouping} />}
      </div>
      <button className="new" title="新しい会話" disabled={disabled} onClick={onNew}><LuPlus /> 新規</button>
      <ul className={grouping === "workspace" ? "grouped" : undefined}>
        {grouping === "workspace"
          ? groups.flatMap(g => [groupHead(g), ...(closed.has(g.key) ? [] : g.list.map(row))])
          : list.map(row)}
      </ul>
      {shared.length > 0 && (
        <div className="shared-list">
          <button className="shared-head" onClick={() => setSharedOpen(o => !o)} aria-expanded={sharedOpen}>
            {sharedOpen ? <LuChevronDown /> : <LuChevronRight />} 共有された会話
          </button>
          {sharedOpen && <SharedList shared={shared} readingId={readingId} onOpenShared={onOpenShared} />}
        </div>
      )}
      {me.login && <div className="admin-notice row" title={ADMIN_NOTICE}><LuEye /> 管理者が読めます</div>}
      <div className="footer">
        <AppName />
        <UserMenu onOpenAccount={onOpenAccount} />
        {adminBtn}
        <button className={"icon-btn " + settingsCls} title="設定" onClick={onToggleSettings}><LuSettings /></button>
      </div>
    </nav>
  );
}

// GroupingMenu は、一覧の並べ方を選ぶメニュー(0038)。ボタンは今の並べ方のアイコン。押すと下に並べ方の一覧を出す
// (2つだけでも切り替えのボタンにしないのは、プロジェクトごとの表示などを足せるようにするため)
function GroupingMenu({ value, onChange }: { value: ListGrouping; onChange(g: ListGrouping): void }) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useMenuDismiss(open, () => setOpen(false), box);
  const label = LIST_GROUPINGS.find(g => g.value === value)?.label ?? "";
  return (
    <div className="grouping-menu" ref={box}>
      <button className="icon-btn" title={`会話の並べ方: ${label}`} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(o => !o)}>
        {value === "workspace" ? <LuFolderTree /> : <LuList />}
      </button>
      {open && (
        <div className="split-menu" role="menu">
          {LIST_GROUPINGS.map(g => (
            <button key={g.value} role="menuitemradio" aria-checked={g.value === value} onClick={() => { setOpen(false); onChange(g.value); }}>
              <span className="grouping-check">{g.value === value && <LuCheck />}</span>
              {g.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// useRevealGroups は、開いた会話・生成を始めた会話のまとまりを開く(畳んだまとまりの中に隠れないように。0038)。
// 開くのは、会話を開いた・生成を始めた・名前を変え始めた(F2)・開いている会話のワークスペースを変えたときの1回だけ。そのあとで畳めば、畳んだままにする。
// 作ったばかりの会話は一覧を読み直すまでまとまりに入っていないので、見つかるまで覚えておく
function useRevealGroups(
  groups: ConversationGroup[], currentId: string | null, generatingId: string | null, renamingId: string | null, currentWorkspace: string | null,
  closed: ReadonlySet<string>, setClosed: (key: string, close: boolean) => void,
) {
  const pending = useRef(new Set<string>());
  useEffect(() => {
    if (currentId) pending.current.add(currentId);
  }, [currentId, currentWorkspace]);
  useEffect(() => {
    if (generatingId) pending.current.add(generatingId);
  }, [generatingId]);
  useEffect(() => {
    if (renamingId) pending.current.add(renamingId);
  }, [renamingId]);
  // 描くたびに見る(探すのは覚えている会話だけなので軽い)。並べ方が新しい順の間は groups が空なので、ワークスペースごとにしたときに開く
  useEffect(() => {
    if (pending.current.size === 0) return;
    for (const g of groups) {
      const found = g.list.filter(c => pending.current.has(c.id));
      if (found.length === 0) continue;
      for (const c of found) pending.current.delete(c.id);
      if (closed.has(g.key)) setClosed(g.key, false);
    }
  });
}

// SHARED_PAGE は、共有された会話を一度に出す数(0031 の決定事項 17)
const SHARED_PAGE = 20;

// SharedList は、ほかの人に共有された会話。「自分に共有」(指定した人・グループで共有されたもの)を上、「全員に共有」を下に分け、
// 持ち主で絞る・タイトルで探す(0031 の決定事項 17)。それぞれ新しい 20 件から出し、「もっと見る」で足す
function SharedList({ shared, readingId, onOpenShared }: { shared: ConversationRef[]; readingId: string | null; onOpenShared(id: string): void }) {
  const [query, setQuery] = useState("");
  const [owner, setOwner] = useState("");
  // 持ち主はログインの名前で分け、見せるのは「表示名(ログインの名前)」
  const owners = [...new Map(shared.map(c => [c.owner_name, showName(c.owner_name, c.owner_display)])).entries()].sort((a, b) => a[1].localeCompare(b[1]));
  const q = query.trim().toLowerCase();
  const match = (c: ConversationRef) => (owner === "" || c.owner_name === owner) && (q === "" || (c.title || "新しい会話").toLowerCase().includes(q));
  const toMe = shared.filter(c => c.share === "users" && match(c));
  const toAll = shared.filter(c => c.share !== "users" && match(c));
  return (
    <>
      <div className="shared-filter">
        <input type="text" value={query} placeholder="タイトルで探す…" aria-label="共有された会話をタイトルで探す" onChange={e => setQuery(e.target.value)} />
        <select value={owner} aria-label="持ち主で絞る" onChange={e => setOwner(e.target.value)}>
          <option value="">全員</option>
          {owners.map(([name, label]) => <option key={name} value={name}>{label}</option>)}
        </select>
      </div>
      <SharedGroup label="自分に共有" list={toMe} readingId={readingId} onOpenShared={onOpenShared} />
      <SharedGroup label="全員に共有" list={toAll} readingId={readingId} onOpenShared={onOpenShared} />
    </>
  );
}

function SharedGroup({ label, list, readingId, onOpenShared }: { label: string; list: ConversationRef[]; readingId: string | null; onOpenShared(id: string): void }) {
  const [limit, setLimit] = useState(SHARED_PAGE);
  if (list.length === 0) return null;
  return (
    <>
      <div className="shared-sub">{label}({list.length})</div>
      <ul>
        {list.slice(0, limit).map(c => (
          <li key={c.id} className={"conversation" + (c.id === readingId ? " current" : "")}>
            <button className="title" title={`${c.title || "新しい会話"}(${showName(c.owner_name, c.owner_display)} の会話。読むだけ)`} onClick={() => onOpenShared(c.id)}>
              {c.title || "新しい会話"}
              <span className="shared-owner">{showName(c.owner_name, c.owner_display, true)}</span>
            </button>
          </li>
        ))}
      </ul>
      {list.length > limit && <button className="shared-more" onClick={() => setLimit(l => l + SHARED_PAGE)}>もっと見る(残り {list.length - limit} 件)</button>}
    </>
  );
}

// Mark は会話の印の種類(CSS の .dot.<種類>)。どれも行の右の削除の場所に出す。送信している間(生成中・確認待ち)は削除できないので印だけ、
// 終わったあと(終わった・エラー)は印を出し、マウスを乗せたときだけ削除の × に替える。
type Mark = "waiting" | "generating" | Ended;

// MARK_ORDER は、畳んだ一覧のハンバーガーに1つだけ出すときに、どれを先に出すか(気にしてほしい順)
const MARK_ORDER: Mark[] = ["waiting", "error", "generating", "done"];

const MARK_LABEL: Record<Mark, string> = {
  waiting: "ツールの実行の確認待ち",
  error: "エラーで終わった(開くとエラーが出ます)",
  generating: "返事を生成中",
  done: "返事が終わった",
};

// MARK_BADGE_LABEL は、ハンバーガーの印にマウスを乗せたときの説明(どの会話かは一覧を開くと分かる)
const MARK_BADGE_LABEL: Record<Mark, string> = {
  waiting: "ツールの実行の確認を待っている会話があります",
  error: "エラーで終わった会話があります",
  generating: "返事を生成中の会話があります",
  done: "返事が終わった会話があります",
};

// ADMIN_NOTICE は「管理者が読めます」の吹き出し(0018 の決定事項 1・23)
const ADMIN_NOTICE = "会話(ツールの結果も)は管理者が読め、読んだことは記録に残ります";

// AppName はアプリの名前とバージョン(Go の _cmd/aibow/version)。
function AppName() {
  const [version, setVersion] = useState("");
  useEffect(() => {
    ChatService.Version().then(setVersion, () => setVersion(""));
  }, []);
  return <span className="app-name">AIBow{version && <span className="app-version">{version}</span>}</span>;
}

// UserMenu は、ログインしている人の名前(表示名)。押すと上に「アカウント」(設定の画面の「アカウント」のタブ)と「ログアウト」を出す。
// ブラウザでも、画面のアプリが aibow-server にログインしてつないでいるときも同じ形(0031 の決定事項 44・46)。
// ログアウトしたら読み直し、ログインの画面になる。settings.json に手で書いた API キーでつないでいるときは、ログアウトを出さない。
// ログインが要らない形・アプリの中で動かすときは出さない。
function UserMenu({ onOpenAccount }: { onOpenAccount(): void }) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useMenuDismiss(open, () => setOpen(false), box);
  if (!signedIn()) return null;
  return (
    <div className="user-menu" ref={box}>
      <button className="user-btn" title={`${showName(me.name, me.display_name)} としてログインしています`} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(o => !o)}>
        <LuUser /><span>{showName(me.name, me.display_name, true)}</span>
      </button>
      {open && (
        <div className="split-menu up" role="menu">
          <button role="menuitem" onClick={() => { setOpen(false); onOpenAccount(); }}>アカウント</button>
          {canLogout() && <button role="menuitem" onClick={() => { setOpen(false); signOut(); }}>ログアウト</button>}
        </div>
      )}
    </div>
  );
}

// 名前の入力欄。Enter かフォーカスが外れたら確定、Esc で取り消し(onDone に null)
function TitleInput({ initial, onDone }: { initial: string; onDone(title: string | null): void }) {
  const [text, setText] = useState(initial);
  const input = useRef<HTMLInputElement>(null);
  const composing = useRef(false); // IME で変換中か
  const done = useRef(false); // Enter / Esc のあとの blur で、もう一度確定しないように

  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);

  const finish = (title: string | null) => {
    if (done.current) return;
    done.current = true;
    onDone(title);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    // IME で変換中の Enter は確定なので、名前の確定にしない(Composer と同じ確かめ方)
    if (composing.current || e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (e.key === "Enter") finish(text);
    if (e.key === "Escape") {
      // 取り消すだけにする。重ねて出した一覧を Esc で閉じる仕組み(App.tsx)まで届けない
      e.stopPropagation();
      finish(null);
    }
  };

  return (
    <input
      ref={input}
      type="text"
      className="title-input"
      value={text}
      onChange={e => setText(e.target.value)}
      onKeyDown={onKeyDown}
      onCompositionStart={() => (composing.current = true)}
      onCompositionEnd={() => (composing.current = false)}
      onBlur={() => finish(text)}
    />
  );
}
