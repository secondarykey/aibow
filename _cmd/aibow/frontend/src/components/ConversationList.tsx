// 左側: 会話の一覧(更新の新しい順)。新規作成・切り替え・名前の変更・削除。
// 名前の変更はダブルクリックか F2(F2 は App.tsx が受けて renamingId を決める)。
// 会話の設定の歯車は、一覧を畳んでいても開けるよう、入力欄の下(メーターの右)に置く(App.tsx)。
// 下にアプリの名前とバージョン、右に設定の画面の開閉ボタン(アイコンだけ)。管理できる人には、その左に管理の画面の盾(0032)。
// デスクトップのアプリがサーバーモードのときは、バージョンの後ろにサーバーのアイコン(説明はつなぎ先。つながらないときは警告の色。0056)。
// ウィンドウの上から下までの列。上のハンバーガーで一覧を畳める。畳んだときもアイコンだけの細い列を残し、
// ハンバーガー・新規・設定はそこに集める。上の空いているところはタイトルバーと同じくドラッグでウィンドウを動かせる。
// 新規(+)は一覧の一番上の行に、検索ボックスと並べて置く(スクロールしても隠れないよう、一覧の外に固定する。0055 で文字を外してアイコンだけにした)
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
// 共有(0018 の 11-2・0055):共有した会話は名前の右に共有のアイコン。ほかの人に共有された会話も同じ一覧に更新の新しい順で混ぜ、
// 名前の右に色違いの共有のアイコンと持ち主の名前を出す(押すと読むだけで開く。名前の変更・削除・印は出さない)。
// 混ぜた列と探す・分けるのは conversationGroups.ts(ListItem)。ワークスペースごとの並べ方では、共有された会話は「ワークスペースなし」に入る。
// ログインが要る aibow-server につないでいるときは、一番下の名前の横に「管理者が読めます」を常に出す(決定事項 23。畳んだときは目のアイコンだけ)
//
// 探す(0055):「+」(新規)の横の検索ボックスで、自分の会話をタイトルで、共有された会話をタイトルと持ち主の名前で絞る(画面の中だけ)。
// 一覧を畳んだ細い列には出さない。
//
// 非アクティブ(0055):行にマウスを乗せると、削除の左に「非アクティブにする」(目を閉じたアイコン)。非アクティブの会話は、
// 一覧の一番下の「非アクティブ(n)」の折りたたみに入る(最初は閉じている。開け閉めは PC ごとに覚える)。中の行は「アクティブに戻す」(目)。
// 印は付けた人の一覧にだけ効く(共有された会話に付けても、持ち主やほかの相手の一覧は変わらない)。開いた会話が非アクティブなら折りたたみを開き、
// 検索が非アクティブの会話に当たったときも開いて出す
import { useEffect, useMemo, useRef, useState } from "react";
import { ChatService, isBrowser, me } from "../backend";
import { canLogout, signedIn, signOut } from "./AccountSettings";
import { sharedTooltip, showName } from "../directory";
import type { KeyboardEvent } from "react";
import { LuCheck, LuChevronDown, LuChevronRight, LuEye, LuEyeOff, LuFolderTree, LuList, LuMenu, LuPlus, LuSearch, LuServer, LuServerOff, LuSettings, LuShare2, LuShield, LuTrash2, LuUser } from "react-icons/lu";
import { useMenuDismiss } from "./SplitButton";
import { toggleMaximiseOnDoubleClick } from "./TitleBar";
import type { Conversation, ConversationRef } from "../../bindings/github.com/secondarykey/aibow";
import type { WorkspaceView } from "../../bindings/github.com/secondarykey/aibow/ui";
import { groupByWorkspace, itemTitle, LIST_GROUPINGS, matchItem, mergeList, useClosedGroups, useInactiveOpen, useListGrouping, useListQuery, useShowEmptyWorkspaces } from "../conversationGroups";
import { unappliedCount } from "../worktree";
import type { ConversationGroup, ListGrouping, ListItem } from "../conversationGroups";
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
  canAdmin: boolean;     // 管理の画面の盾を出すか(ログインした管理者か、ログインなしの形のブラウザ。0046)
  onToggleAdmin(): void; // 歯車の隣の盾で、管理の画面を開く / 閉じる
  open: boolean; // 一覧を開いているか
  onToggleOpen(): void;
  width: number; // 開いているときの幅(px)。一覧の右の仕切り(Splitter)で変えられる
  shared: ConversationRef[]; // ほかの人に共有された会話(0018 の 11-2。自分の会話と同じ一覧に混ぜる。0055)
  readingId: string | null;  // 読むだけで開いているほかの人の会話
  onOpenShared(id: string): void;
  onSetInactive(id: string, shared: boolean, inactive: boolean): void; // 会話を非アクティブにする / 戻す(0055)
  workspaces: WorkspaceView[]; // この PC のワークスペース(並べ方がワークスペースごとのとき。0038)
  currentWorkspace: string | null; // 開いている会話のワークスペース(一覧を読み直す前に変えたときも、すぐにまとまりを移すため)
  onNewIn(workspace: string): void; // まとまりの「+」:そのワークスペースを選んだ新しい会話の画面に移る(「ワークスペースなし」は "" で、必ず外す)
  server: string;      // サーバーモードのときの aibow-server のつなぎ先(アプリモードとブラウザでは空。useModels)
  serverError: string; // aibow-server につながらない理由(つながっていれば空)
}

export function ConversationList({
  list, currentId, generatingId, waitingId, ended, disabled, onOpen, onNew, onDelete, onRename, renamingId, onStartRename, onEndRename,
  settingsOpen, onToggleSettings, onOpenAccount, adminOpen, canAdmin, onToggleAdmin, open, onToggleOpen, width, shared, readingId, onOpenShared,
  onSetInactive, workspaces, currentWorkspace, onNewIn, server, serverError,
}: Props) {
  // 削除の確認を出している会話。消すと戻せないので、もう一度押してもらう
  const [confirming, setConfirming] = useState<string | null>(null);
  // 消そうとしている会話の worktree の、まだ本体に取り込んでいないファイルの数(0039。消すと worktree ごと消えるので、確認の文に出す)
  const [unapplied, setUnapplied] = useState(0);
  const startConfirm = (c: Conversation) => {
    setConfirming(c.id);
    setUnapplied(0);
    if (c.settings?.worktree) unappliedCount(c.id).then(n => setUnapplied(n));
  };
  // 読むだけで開いている間は、その行を選んでいる見た目にする(後ろに残っている自分の会話は選んでいる見た目にしない)
  const selectedId = readingId ?? currentId;

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

  // 自分の会話と共有された会話を1列にし(0055)、検索ボックスの文字で絞って、アクティブと非アクティブに分ける
  const [query, setQuery] = useListQuery();
  const q = query.trim().toLowerCase();
  const items = useMemo(() => mergeList(list, shared), [list, shared]);
  const matched = useMemo(() => items.filter(it => matchItem(it, q)), [items, q]);
  const active = useMemo(() => matched.filter(it => !it.inactive), [matched]);
  const inactive = useMemo(() => matched.filter(it => it.inactive), [matched]);

  // 並べ方(0038)。ワークスペースごとのときだけ、まとまりを作る。非アクティブの会話はまとまりから抜き、下の折りたたみに入れる(決定事項 18)。
  // 会話の無いワークスペースは、切り替えたときだけ出す。探している間は、当たった会話が埋もれないよう切り替えによらず出さない(0062)
  const [grouping, setGrouping] = useListGrouping();
  const [showEmpty, setShowEmpty] = useShowEmptyWorkspaces();
  const groups = useMemo(
    () => grouping === "workspace"
      ? groupByWorkspace(
        active, workspaces, showEmpty && q === "",
        currentId !== null && currentWorkspace !== null ? { id: currentId, workspace: currentWorkspace } : undefined,
      )
      : [],
    [grouping, active, workspaces, showEmpty, q, currentId, currentWorkspace],
  );
  const [closed, setClosed] = useClosedGroups();
  useRevealGroups(groups, selectedId, generatingId, renamingId, currentWorkspace, closed, setClosed);

  // 非アクティブの折りたたみ。探している間は、当たった会話があれば開いて出す(探して見つからない、をなくす。決定事項 20)。
  // 探している間に閉じても、覚えている開け閉めは変えない(探す文字を変えると、また開く)
  const [inactiveOpen, setInactiveOpen] = useInactiveOpen();
  const [searchClosed, setSearchClosed] = useState(false);
  useEffect(() => setSearchClosed(false), [q]);
  const searching = q !== "" && inactive.length > 0;
  const showInactive = searching ? !searchClosed : inactiveOpen;
  const toggleInactive = () => (searching ? setSearchClosed(showInactive) : setInactiveOpen(!inactiveOpen));
  const revealInactive = useRevealInactive(items, selectedId, renamingId, setInactiveOpen);
  const setItemInactive = (it: ListItem, value: boolean) => {
    if (it.id === selectedId) revealInactive(it.id); // 開いている会話を非アクティブにしたら、折りたたみを開いてその行を見せる(決定事項 19)
    onSetInactive(it.id, it.kind === "shared", value);
  };

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

  // inactiveBtn は、行の右の「非アクティブにする」/「アクティブに戻す」(0055)。確かめは出さない(すぐ戻せるので)
  const inactiveBtn = (it: ListItem, isDisabled: boolean) => (
    <button
      className="inactive-toggle"
      title={it.inactive ? "アクティブに戻す" : "非アクティブにする"}
      disabled={isDisabled}
      onClick={() => setItemInactive(it, !it.inactive)}
    >
      {it.inactive ? <LuEye /> : <LuEyeOff />}
    </button>
  );

  // ownRow は自分の会話の1行(名前・印・非アクティブ・削除)。新しい順でも、ワークスペースごとのまとまりの中でも、非アクティブの中でも同じもの
  const ownRow = (it: ListItem & { kind: "own" }) => {
    const c = it.c;
    const title = itemTitle(it);
    const generating = c.id === generatingId;
    const mark = markOf(c.id);
    const cls = ["conversation", c.id === selectedId && "current", generating && "generating"].filter(Boolean).join(" ");
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
          <span className="confirm-text" title={unapplied > 0 ? "この会話の worktree とブランチも削除される" : undefined}>
            {unapplied > 0 ? `削除しますか?(取り込んでいない差分 ${unapplied} ファイルも削除されます)` : "削除しますか?"}
          </span>
          <button className="danger" onClick={() => { setConfirming(null); onDelete(c.id); }}>削除</button>
          <button onClick={() => setConfirming(null)}>キャンセル</button>
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
        {inactiveBtn(it, disabled)}
        {generating && mark ? (
          // 送信している間(生成中・確認待ち)は削除できないので、削除の場所に印を出す(2026-10-31、ユーザーの指示)
          <span className="running-mark" title={MARK_LABEL[mark] + "(終わるまで削除できません)"}>
            <span className={"dot " + mark} />
          </span>
        ) : (
          // 終わったあとの印(終わった・エラー)も同じ場所に出し、マウスを乗せたときだけゴミ箱に替える(削除はできるので)。
          // 「×」は閉じる・一覧から外すだけに使うので、削除はゴミ箱(0050 の決定事項 7)
          <button
            className={"delete" + (mark ? " has-mark" : "")}
            title={mark ? `削除(${MARK_LABEL[mark]})` : "削除"}
            disabled={disabled}
            onClick={() => startConfirm(c)}
          >
            {mark && <span className={"dot " + mark} />}
            <LuTrash2 className="x" />
          </button>
        )}
      </li>
    );
  };

  // sharedRow はほかの人に共有された会話の1行(名前・受け取った側の共有のアイコン・持ち主の名前・非アクティブ)。
  // 自分の会話ではないので、名前の変更・削除・生成中などの印は出さない(0055 の決定事項 11)
  const sharedRow = (it: ListItem & { kind: "shared" }) => {
    const r = it.r;
    const title = itemTitle(it);
    return (
      <li key={r.id} className={"conversation shared" + (r.id === selectedId ? " current" : "")}>
        <button className="title" title={sharedTooltip(title, r)} onClick={() => onOpenShared(r.id)}>
          <span className="title-text">{title}</span>
          <LuShare2 className="share-mark received" />
          <span className="shared-owner">{showName(r.owner_name, r.owner_display, true)}</span>
        </button>
        {inactiveBtn(it, false)}
      </li>
    );
  };
  const row = (it: ListItem) => (it.kind === "own" ? ownRow(it) : sharedRow(it));

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
        {/* 「ワークスペースなし」にも出す(押すとワークスペースを外した新しい会話の画面。0051 の決定事項 2)。
            「見つからない」には出さない(無いワークスペースでは始められない) */}
        {(g.workspace || g.key === "none") && (
          <button
            className="conv-group-new"
            title={g.workspace ? `「${g.label}」で新しい会話` : "ワークスペースなしで新しい会話"}
            disabled={disabled}
            onClick={() => onNewIn(g.workspace)}
          >
            <LuPlus />
          </button>
        )}
      </li>
    );
  };

  // inactiveHead は一覧の一番下の「非アクティブ(n)」の見出し(押すと開け閉め)。閉じているときは、中の会話のいちばん気にしてほしい印を出す
  const inactiveHead = () => {
    const headMark = showInactive ? null : MARK_ORDER.find(m => inactive.some(c => markOf(c.id) === m)) ?? null;
    return (
      <li key="inactive" className="conv-group-head other inactive-head">
        <button className="conv-group-toggle" title="非アクティブにした会話" aria-expanded={showInactive} onClick={toggleInactive}>
          {showInactive ? <LuChevronDown /> : <LuChevronRight />}
          <span className="conv-group-name">非アクティブ</span>
          <span className="conv-group-count">{inactive.length}</span>
          {headMark && <span className={"dot " + headMark} title={MARK_BADGE_LABEL[headMark]} />}
        </button>
      </li>
    );
  };

  const onSearchKey = (e: KeyboardEvent<HTMLInputElement>) => {
    // Esc で探す文字を消す。消す文字があるときは、重ねて出した一覧を Esc で閉じる仕組み(App.tsx)まで届けない
    if (e.key === "Escape" && query !== "") {
      e.stopPropagation();
      setQuery("");
    }
  };

  return (
    <nav className="conversations" style={{ width }}>
      <div className="nav-top" onDoubleClick={toggleMaximiseOnDoubleClick}>
        {menu}
        {/* 並べ方はワークスペースでまとめるためのもの。ブラウザの会話はワークスペースを使わないので出さない(0038) */}
        {!isBrowser && <GroupingMenu value={grouping} onChange={setGrouping} showEmpty={showEmpty} onShowEmpty={setShowEmpty} />}
      </div>
      {/* 一覧の一番上の行:新規(アイコンだけ)と検索ボックス(0055 の未決事項 3) */}
      <div className="new-row">
        <button className="new" title="新しい会話" disabled={disabled} onClick={onNew}><LuPlus /></button>
        <label className="list-search" title="自分の会話はタイトル、共有された会話はタイトルと持ち主の名前で探す">
          <LuSearch />
          <input type="search" value={query} placeholder="会話を探す" aria-label="会話を探す" onChange={e => setQuery(e.target.value)} onKeyDown={onSearchKey} />
        </label>
      </div>
      <ul className={grouping === "workspace" ? "grouped" : undefined}>
        {grouping === "workspace"
          ? groups.flatMap(g => [groupHead(g), ...(closed.has(g.key) ? [] : g.list.map(row))])
          : active.map(row)}
        {inactive.length > 0 && [inactiveHead(), ...(showInactive ? inactive.map(row) : [])]}
        {q !== "" && matched.length === 0 && <li className="list-empty">「{query.trim()}」に当たる会話はありません</li>}
      </ul>
      {me.login && <div className="admin-notice row" title={ADMIN_NOTICE}><LuEye /> 管理者が読めます</div>}
      <div className="footer">
        <AppName server={server} serverError={serverError} />
        <UserMenu onOpenAccount={onOpenAccount} />
        {adminBtn}
        <button className={"icon-btn " + settingsCls} title="設定" onClick={onToggleSettings}><LuSettings /></button>
      </div>
    </nav>
  );
}

// GroupingMenu は、一覧の並べ方を選ぶメニュー(0038)。ボタンは今の並べ方のアイコン。押すと下に並べ方の一覧を出す
// (2つだけでも切り替えのボタンにしないのは、プロジェクトごとの表示などを足せるようにするため)。
// ワークスペースごとのときは、区切りの下に「会話の無いワークスペースも出す」の切り替えを出す(0062。新しい順では意味が無いので出さない)。
// 切り替えはメニューを閉じない(押した結果が一覧に出るのを見ながら、続けて戻せるように)
function GroupingMenu({ value, onChange, showEmpty, onShowEmpty }: {
  value: ListGrouping; onChange(g: ListGrouping): void; showEmpty: boolean; onShowEmpty(show: boolean): void;
}) {
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
          {value === "workspace" && (
            <>
              <div className="menu-sep" role="separator" />
              <button role="menuitemcheckbox" aria-checked={showEmpty} onClick={() => onShowEmpty(!showEmpty)}>
                <span className="grouping-check">{showEmpty && <LuCheck />}</span>
                会話の無いワークスペースも出す
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}

// useRevealGroups は、開いた会話・生成を始めた会話のまとまりを開く(畳んだまとまりの中に隠れないように。0038)。
// 開くのは、会話を開いた(読むだけで開いたも)・生成を始めた・名前を変え始めた(F2)・開いている会話のワークスペースを変えたときの1回だけ。
// そのあとで畳めば、畳んだままにする。作ったばかりの会話は一覧を読み直すまでまとまりに入っていないので、見つかるまで覚えておく
function useRevealGroups(
  groups: ConversationGroup[], selectedId: string | null, generatingId: string | null, renamingId: string | null, currentWorkspace: string | null,
  closed: ReadonlySet<string>, setClosed: (key: string, close: boolean) => void,
) {
  const pending = useRef(new Set<string>());
  useEffect(() => {
    if (selectedId) pending.current.add(selectedId);
  }, [selectedId, currentWorkspace]);
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

// useRevealInactive は、開いた会話が非アクティブなら「非アクティブ」の折りたたみを開く(0055 の決定事項 19。useRevealGroups と同じ考え方)。
// 開くのは、会話を開いた・名前を変え始めた・開いている会話を非アクティブにした(返す関数)ときの1回だけ。
// 一覧を読み直すまで印が変わらないので、非アクティブかアクティブかが分かるまで覚えておく
function useRevealInactive(items: ListItem[], selectedId: string | null, renamingId: string | null, setOpen: (open: boolean) => void) {
  const pending = useRef(new Map<string, boolean>()); // 会話の ID → 非アクティブになるのを待つか(付けたばかりなら true)
  useEffect(() => {
    if (selectedId) pending.current.set(selectedId, false);
  }, [selectedId]);
  useEffect(() => {
    if (renamingId) pending.current.set(renamingId, false);
  }, [renamingId]);
  useEffect(() => {
    if (pending.current.size === 0) return;
    for (const it of items) {
      const waiting = pending.current.get(it.id);
      if (waiting === undefined) continue;
      if (it.inactive) {
        pending.current.delete(it.id);
        setOpen(true);
      } else if (!waiting) {
        pending.current.delete(it.id); // アクティブの会話を開いた。開かなくてよい
      }
    }
  });
  return (id: string) => { pending.current.set(id, true); };
}

// Mark は会話の印の種類(CSS の .dot.<種類>)。どれも行の右の削除の場所に出す。送信している間(生成中・確認待ち)は削除できないので印だけ、
// 終わったあと(終わった・エラー)は印を出し、マウスを乗せたときだけ削除のゴミ箱に替える。
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
// サーバーモードのときは、後ろに aibow-server のアイコン(0056)。状態は useModels が Models() を取り直したとき(ウィンドウに戻ったときなど)に変わる。
// つながらないときは形(LuServerOff)と色(警告)の両方を変える(色だけだと見分けにくい人がいるため)。理由の全文はエラーのカードに出ているので、説明には入れない。
// サーバーのバージョンは、0054 で aibow-server の版を受け取るようになったら説明に足す
function AppName({ server, serverError }: { server: string; serverError: string }) {
  const [version, setVersion] = useState("");
  useEffect(() => {
    ChatService.Version().then(setVersion, () => setVersion(""));
  }, []);
  const showServer = !isBrowser && server !== "";
  return (
    <span className="app-name">
      AIBow{version && <span className="app-version">{version}</span>}
      {showServer && (serverError
        ? <span className="app-server down" title={`aibow-server(${server})につながりません`}><LuServerOff /></span>
        : <span className="app-server" title={`aibow-server: ${server}`}><LuServer /></span>)}
    </span>
  );
}

// UserMenu は、ログインしている人の名前(表示名)。押すと上に「アカウント」(設定の画面の「アカウント」のタブ)と「ログアウト」を出す。
// ブラウザでも、画面のアプリが aibow-server にログインしてつないでいるときも同じ形(0031 の決定事項 44・46)。
// ログアウトしたら読み直し、ログインの画面になる。settings.json に手で書いた API キーでつないでいるときは、ログアウトを出さない。
// ログインが要らない形・アプリモードのときは出さない。
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
