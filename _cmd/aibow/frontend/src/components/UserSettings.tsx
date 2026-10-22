// 設定の画面の「利用者」のタブ(0018 の決定事項 7。11-0)。ブラウザの管理者だけに出す。
//
// 作る・パスワードと API キーの作り直し・消すができる(aibow-server の user コマンドと同じ処理。_cmd/aibow-server の admin.go)。
// パスワードと API キーは、作ったときの返事でだけ受け取り、画面に一度だけ出す(保存されるのはハッシュだけで、あとからは見られない)。
// 管理者の印を付ける / 外すのはコマンドだけ(0018 の決定事項 6)なので、ここでは印を見せるだけにする。
import { useCallback, useEffect, useState } from "react";
import { LuKeyRound, LuShieldCheck, LuTrash2, LuX } from "react-icons/lu";
import { admin, me, type AdminUser } from "../backend";
import { errorMessage } from "../errors";
import { CopyButton } from "./CopyButton";

// Secret は、一度だけ出すパスワードか API キー
interface Secret {
  name: string;
  label: string; // パスワード / API キー
  value: string;
}

export function UserSettings({ onError }: { onError(text: string): void }) {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [secret, setSecret] = useState<Secret | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null); // 消すかを聞いている人

  const reload = useCallback(async () => {
    try {
      setUsers(await admin.users());
    } catch (err) {
      onError(errorMessage(err));
    }
  }, [onError]);
  useEffect(() => { reload(); }, [reload]);

  // run は操作を1つ行い、一覧を取り直す。パスワードか API キーが返れば一度だけ出す
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
    const r = await admin.create(name);
    return { name: r.name, label: "パスワード", value: r.password };
  });
  const resetPassword = (name: string) => run(async () => {
    const r = await admin.resetPassword(name);
    return { name: r.name, label: "パスワード", value: r.password };
  });
  const renewKey = (name: string) => run(async () => {
    const r = await admin.renewKey(name);
    return { name: r.name, label: "API キー", value: r.key };
  });
  const remove = (name: string) => run(async () => {
    await admin.remove(name);
    setConfirming(null);
    if (secret?.name === name) setSecret(null);
    return null;
  });

  return (
    <>
      <h2 title="管理者の印は、aibow-server の PC で aibow-server user admin / unadmin で付け外しする">利用者</h2>
      {secret && <SecretBox secret={secret} onClose={() => setSecret(null)} />}
      <div className="user-table">
        <span className="model-table-head">名前</span>
        <span className="model-table-head">作った日</span>
        <span className="model-table-head" />
        {users.map(u => (
          <div key={u.name} className="model-row">
            <span className="model-name" title={u.name}>
              {u.name}
              {u.admin && <LuShieldCheck className="user-admin" title="管理者" aria-label="管理者" />}
              {!u.password && <span className="origin-chip" title="パスワードが無いので、ログインできない">パスワードなし</span>}
              {u.key && <LuKeyRound className="user-key" title="API キーあり" aria-label="API キーあり" />}
              {u.local && <span className="origin-chip" title="ログインなしで使うときの会話の持ち主">ログインなし</span>}
            </span>
            <span className="user-date">{u.created_at.slice(0, 10)}</span>
            {confirming === u.name
              ? (
                <span className="user-actions">
                  <span className="confirm-text" title="その人の会話・プロファイル・設定も消える">削除しますか?</span>
                  <button className="danger" onClick={() => remove(u.name)}>削除</button>
                  <button onClick={() => setConfirming(null)}>やめる</button>
                </span>
              )
              : (
                <span className="user-actions">
                  <button onClick={() => resetPassword(u.name)} title="新しいパスワードを作る(その人のログイン中の画面はログインし直しになる)">パスワード</button>
                  <button onClick={() => renewKey(u.name)} title="新しい API キーを作る(前のキーは使えなくなる)">API キー</button>
                  <button
                    className="place-remove"
                    disabled={u.local || u.name === me.name}
                    onClick={() => setConfirming(u.name)}
                    title={u.local ? "ログインなしの形の利用者は消せない" : u.name === me.name ? "自分は消せない" : "消す"}
                    aria-label="消す"
                  >
                    <LuTrash2 />
                  </button>
                </span>
              )}
          </div>
        ))}
      </div>
      <AddUser onAdd={create} />
    </>
  );
}

// SecretBox は、作ったパスワードか API キーを一度だけ出す(閉じたら、もう見られない)
function SecretBox({ secret, onClose }: { secret: Secret; onClose(): void }) {
  return (
    <div className="secret-box" title="今だけ表示する。閉じたらもう見られない(忘れたら作り直す)">
      <span className="secret-label">{secret.name} の{secret.label}</span>
      <code className="secret-value">{secret.value}</code>
      <CopyButton text={secret.value} className="place-remove" />
      <button className="place-remove" onClick={onClose} title="閉じる" aria-label="閉じる"><LuX /></button>
    </div>
  );
}

function AddUser({ onAdd }: { onAdd(name: string): Promise<boolean> }) {
  const [name, setName] = useState("");
  const add = async () => {
    if (await onAdd(name.trim())) setName("");
  };
  return (
    <div className="place-add">
      <input
        type="text"
        value={name}
        placeholder="名前"
        title="ログインの名前(1〜32 文字。空白は使えない)。パスワードは作ったときに一度だけ出す"
        onChange={e => setName(e.target.value)}
        onKeyDown={e => { if (e.key === "Enter" && name.trim()) add(); }}
      />
      <button disabled={!name.trim()} onClick={add}>作る</button>
    </div>
  );
}
