// グループの画面(0031 の段4)。
//
//	GroupAdmin   … 管理の画面の「グループ」のタブ(0032)。作る・名前を変える・メンバーと長・消す(決定事項 23・24・30)
//	LeaderGroups … グループの長が、設定の画面の「グループ」のタブで、自分が長のグループのメンバーだけを足す・外す(決定事項 31・32・37)
//
// メンバーを変えると、そのグループに共有された会話・配られたプロファイルが見える人も変わる(決定事項 26・33)。
// 人を足すときは、見えるようになるものの件数を出してから足す。誰が変えられるかは Go の保存先が決める(画面は出し分けるだけ)。
import { useState } from "react";
import { LuCrown, LuX } from "react-icons/lu";
import { ChatService, me } from "../backend";
import { personLabel, type Directory, type Group } from "../directory";
import { errorMessage } from "../errors";
import { TargetSearch } from "./SharePicker";

// GroupMembers は、グループのメンバーの札と、名前で探して足す欄。admin なら札で長を決められる
function GroupMembers({ group, dir, admin, onChanged, onError }: {
  group: Group;
  dir: Directory;
  admin: boolean;
  onChanged(): void;
  onError(text: string): void;
}) {
  const [adding, setAdding] = useState<string | null>(null);   // 足すのを確かめている人
  const [leaving, setLeaving] = useState(false);               // 長が自分を外すのを確かめている
  const run = (f: () => Promise<unknown>) => f().then(onChanged, err => onError(errorMessage(err)));
  const setMember = (user: string, member: boolean) => run(() => ChatService.SetGroupMember(group.id, user, member));
  const shares = group.conversations + group.profiles > 0;
  return (
    <div className="group-members">
      <div className="pick-chips">
        {group.members.length === 0 && <span className="hint">メンバーがいません</span>}
        {group.members.map(m => {
          const leader = group.leaders.includes(m);
          return (
            <span key={m} className={"pick-chip" + (leader ? " leader" : "")} title={personLabel(dir.people.find(p => p.name === m), m) + (leader ? "(長)" : "")}>
              {admin ? (
                <button
                  type="button"
                  className={"pick-leader" + (leader ? " on" : "")}
                  aria-pressed={leader}
                  title={leader ? "長から外す" : "長にする(このグループのメンバーを足す・外せるようになる)"}
                  onClick={() => run(() => ChatService.SetGroupLeader(group.id, m, !leader))}
                >
                  <LuCrown />
                </button>
              ) : leader && <LuCrown className="icon" />}
              <span className="pick-name">{personLabel(dir.people.find(p => p.name === m), m, true)}</span>
              <button
                type="button"
                className="pick-remove"
                aria-label={`${m} を外す`}
                onClick={() => (m === me.name && !admin ? setLeaving(true) : setMember(m, false))}
              >
                <LuX />
              </button>
            </span>
          );
        })}
      </div>
      {leaving && (
        <div className="share-confirm">
          <span>自分を外すと、このグループの長でもなくなります。</span>
          <button type="button" className="danger" onClick={() => { setLeaving(false); setMember(me.name, false); }}>外す</button>
          <button type="button" onClick={() => setLeaving(false)}>やめる</button>
        </div>
      )}
      {adding ? (
        <div className="share-confirm">
          <span>
            {personLabel(dir.people.find(p => p.name === adding), adding, true)} を足します。
            {shares && `この人は、グループに共有された ${group.conversations} 件の会話と ${group.profiles} 件のプロファイルを読めるようになります。`}
          </span>
          <button type="button" className="primary" onClick={() => { const u = adding; setAdding(null); setMember(u, true); }}>足す</button>
          <button type="button" onClick={() => setAdding(null)}>やめる</button>
        </div>
      ) : (
        <TargetSearch
          dir={dir}
          self=""
          groups={false}
          chosen={group.members.map(name => ({ kind: "user" as const, name }))}
          placeholder="足す人を名前で探す…"
          onPick={t => setAdding(t.name)}
        />
      )}
    </div>
  );
}

// GroupAdmin は、管理者のグループの管理(「利用者」のタブの「グループ」)
export function GroupAdmin({ dir, reload, onError }: { dir: Directory; reload(): void; onError(text: string): void }) {
  const [selected, setSelected] = useState<string | null>(null);
  const [name, setName] = useState("");
  const create = () => {
    ChatService.CreateGroup(name.trim()).then(g => {
      setName("");
      setSelected(g.id);
      reload();
    }, err => onError(errorMessage(err)));
  };
  const group = dir.groups.find(g => g.id === selected) ?? null;
  return (
    <>
      <div className="place-add">
        <input
          type="text"
          value={name}
          maxLength={64}
          placeholder="新しいグループの名前"
          onChange={e => setName(e.target.value)}
          onKeyDown={e => { if (!e.nativeEvent.isComposing && e.key === "Enter" && name.trim()) create(); }}
        />
        <button disabled={!name.trim()} onClick={create}>作る</button>
      </div>
      <div className="group-table">
        <span className="model-table-head">名前</span>
        <span className="model-table-head">長</span>
        <span className="model-table-head num">人数</span>
        <span className="model-table-head num" title="このグループに共有されている会話・配られているプロファイルの数">共有</span>
        {dir.groups.map(g => (
          <button
            key={g.id}
            type="button"
            className={"model-row user-row" + (g.id === selected ? " current" : "")}
            aria-expanded={g.id === selected}
            onClick={() => setSelected(s => (s === g.id ? null : g.id))}
          >
            <span className="model-name">{g.name}</span>
            <span className="user-date">{g.leaders.map(l => personLabel(dir.people.find(p => p.name === l), l, true)).join("・") || "-"}</span>
            <span className="num">{g.members.length}</span>
            <span className="num">{g.conversations + g.profiles}</span>
          </button>
        ))}
      </div>
      {dir.groups.length === 0 && <p className="hint">グループがありません</p>}
      {group && <GroupDetail key={group.id} group={group} dir={dir} onClose={() => setSelected(null)} onChanged={reload} onError={onError} />}
    </>
  );
}

// GroupDetail は、一覧で押したグループの詳細(名前・メンバーと長・消す)
function GroupDetail({ group, dir, onClose, onChanged, onError }: { group: Group; dir: Directory; onClose(): void; onChanged(): void; onError(text: string): void }) {
  const [name, setName] = useState(group.name);
  const [removing, setRemoving] = useState(false);
  const rename = () => {
    const t = name.trim();
    if (t === "" || t === group.name) {
      setName(group.name);
      return;
    }
    ChatService.RenameGroup(group.id, t).then(onChanged, err => { setName(group.name); onError(errorMessage(err)); });
  };
  const remove = () => ChatService.DeleteGroup(group.id).then(() => { onClose(); onChanged(); }, err => onError(errorMessage(err)));
  return (
    <section className="user-detail">
      <div className="user-detail-head">
        <h2 className="section">{group.name} グループ</h2>
        <span className="spacer" />
        <button className="place-remove" onClick={onClose} title="閉じる" aria-label="閉じる"><LuX /></button>
      </div>
      <div className="user-field">
        <span className="user-field-label">名前</span>
        <input
          type="text"
          className="display-name"
          value={name}
          maxLength={64}
          onChange={e => setName(e.target.value)}
          onBlur={rename}
          onKeyDown={e => {
            if (e.nativeEvent.isComposing) return;
            if (e.key === "Enter") e.currentTarget.blur();
            if (e.key === "Escape") {
              e.stopPropagation();
              setName(group.name);
            }
          }}
        />
      </div>
      <h3 className="user-sub" title="冠を押すと長にする / 外す。長は、このグループのメンバーを足す・外せる">メンバー({group.members.length})</h3>
      <GroupMembers group={group} dir={dir} admin onChanged={onChanged} onError={onError} />
      <div className="user-remove">
        {removing ? (
          <>
            <p className="confirm-text">
              {group.name} グループを消します。{group.conversations} 件の会話と {group.profiles} 件のプロファイルの共有から外れます
              (ほかに相手がいなければ、非公開・配らないになります)。
            </p>
            <div className="user-remove-row">
              <button className="danger" onClick={remove}>消す</button>
              <button onClick={() => setRemoving(false)}>やめる</button>
            </div>
          </>
        ) : (
          <button className="danger-outline" onClick={() => setRemoving(true)}>このグループを消す</button>
        )}
      </div>
    </section>
  );
}

// UserGroups は、利用者の詳細の「グループ」(その人が入っているグループの札と、グループに足す。管理者だけ。決定事項 24)
export function UserGroups({ user, dir, onChanged, onError }: { user: string; dir: Directory; onChanged(): void; onError(text: string): void }) {
  const mine = dir.groups.filter(g => g.members.includes(user));
  const others = dir.groups.filter(g => !g.members.includes(user));
  const set = (g: Group, member: boolean) =>
    ChatService.SetGroupMember(g.id, user, member).then(onChanged, err => onError(errorMessage(err)));
  return (
    <div className="user-groups">
      {mine.map(g => (
        <span key={g.id} className={"pick-chip group" + (g.leaders.includes(user) ? " leader" : "")} title={g.leaders.includes(user) ? "このグループの長" : undefined}>
          {g.leaders.includes(user) && <LuCrown className="icon" />}
          <span className="pick-name">{g.name}</span>
          <button type="button" className="pick-remove" aria-label={`${g.name} から外す`} onClick={() => set(g, false)}><LuX /></button>
        </span>
      ))}
      {others.length > 0 && (
        <select
          value=""
          aria-label="グループに足す"
          onChange={e => {
            const g = others.find(g => g.id === e.target.value);
            if (g) set(g, true);
          }}
        >
          <option value="">+ グループに足す</option>
          {others.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
        </select>
      )}
      {dir.groups.length === 0 && <span className="hint">グループがありません</span>}
    </div>
  );
}

// LeaderGroups は、設定の画面の「グループ」のタブ(長だけに出す。決定事項 32・37)。自分が長のグループのメンバーだけを足す・外す
export function LeaderGroups({ dir, reload, onError }: { dir: Directory; reload(): void; onError(text: string): void }) {
  const mine = dir.groups.filter(g => g.leaders.includes(me.name));
  return (
    <>
      <h2 title="長は、このグループのメンバーを足す・外せる。グループに共有された会話・配られたプロファイルは、メンバーに見える">自分が長のグループ</h2>
      {mine.length === 0 && <p className="hint">長のグループがありません</p>}
      {mine.map(g => (
        <section key={g.id} className="user-detail">
          <h3 className="user-sub">{g.name} グループ(メンバー {g.members.length})</h3>
          <GroupMembers group={g} dir={dir} admin={false} onChanged={reload} onError={onError} />
        </section>
      ))}
    </>
  );
}
