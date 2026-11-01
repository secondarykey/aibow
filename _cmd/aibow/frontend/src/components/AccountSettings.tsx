// 設定の画面の「アカウント」のタブ(0031 の決定事項 44〜47)。ログインしているときだけ出す
// (ブラウザでログインしたとき・画面のアプリが aibow-server にログインしてつないでいるとき)。
//
//	ログインの名前  bob(管理者)
//	表示名          [佐藤 花子(営業)]
//	メールアドレス  [sato@example.com]
//	使用量          今日・7日・30日の回数・GPU の秒数・生成トークン(0033)
//	パスワード      [今の] [新しい] [もう一度] [変える]
//	[ログアウト]
//
// 以前は、表示名・メールアドレス・パスワードはブラウザの「基本設定」に、アプリのログアウトは「サーバー」のタブにあり、
// 場所がばらばらだった(2026-11-01、ユーザーの指摘)。自分のアカウントのものはここに集め、ブラウザとアプリで同じ形にする。
// 左下の名前のメニュー(ConversationList の UserMenu)の「アカウント」からも開く。
import { useState } from "react";
import { changePassword, logout, me, setDisplayName, setEmail } from "../backend";
import { showName } from "../directory";
import { errorMessage } from "../errors";
import { MyUsage } from "./UsageSettings";
import { DisplayNameInput, EmailInput } from "./UserSettings";

// signedIn は、ログインしているか(アカウントのタブと左下の名前のメニューを出すか)
export function signedIn(): boolean {
  return me.login && me.name !== "";
}

// canLogout は、ログアウトできるか(ブラウザのログインと、アプリが画面でログインしたとき。settings.json に手で書いた API キーではしない)
export function canLogout(): boolean {
  return signedIn() && (!me.viaKey || !!me.canLogout);
}

// signOut は、ログアウトして画面を読み直す(ログインの画面になる。ブラウザもアプリも同じ)。
// 失敗しても読み直す(ブラウザは cookie が残っていれば、読み直した画面がそのまま使える)
export async function signOut() {
  await logout().catch(() => undefined);
  location.reload();
}

export function AccountSettings({ onError }: { onError(text: string): void }) {
  const [display, setDisplay] = useState(me.display_name ?? "");
  const [email, setMail] = useState(me.email ?? "");
  return (
    <>
      <h2>アカウント</h2>
      <div className="account-rows">
        <span className="user-field-label">ログインの名前</span>
        <span className="account-name">
          {me.name}
          {me.admin && <span className="origin-chip">管理者</span>}
        </span>
        <span className="user-field-label" title="共有の相手を選ぶとき・共有された会話の持ち主として、ほかの人に見える名前。空ならログインの名前">表示名</span>
        <DisplayNameInput
          value={display}
          placeholder={me.name}
          onSave={name => {
            setDisplayName(name).then(m => setDisplay(m.display_name ?? ""), err => onError(errorMessage(err)));
          }}
        />
        <span className="user-field-label" title="管理者と自分にだけ見える。今は持つだけで、メールは送らない">メールアドレス</span>
        <EmailInput
          value={email}
          onSave={value => setEmail(value).then(m => { setMail(m.email ?? ""); return true; }, err => { onError(errorMessage(err)); return false; })}
        />
      </div>
      <MyUsage onError={onError} />
      <PasswordForm />
      {canLogout() && (
        <>
          <h2 className="section">ログアウト</h2>
          <button type="button" onClick={signOut} title={showName(me.name, me.display_name) + " からログアウトする(ログインの画面になる)"}>ログアウト</button>
        </>
      )}
    </>
  );
}

// PasswordForm は、ログインしている本人がパスワードを変える(0022 の決定事項 4)。変えたら、ほかのログイン中の画面とアプリは切れる。
// 間違いはフォームの下に出す(設定の画面のエラーの欄に積むと、変えられたあとも残って紛らわしいため)
function PasswordForm() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (next !== again) {
      setError("2つの新しいパスワードが違います");
      return;
    }
    setBusy(true);
    try {
      await changePassword(current, next);
      setCurrent(""); setNext(""); setAgain("");
      setDone(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form className="password-form" onSubmit={submit}>
      <h2 className="section" title="変えると、ほかのログイン中の画面とアプリはログインし直しになる(この画面はそのまま)">パスワード</h2>
      <label htmlFor="pw-current">今のパスワード</label>
      <input id="pw-current" type="password" autoComplete="current-password" value={current} onChange={e => { setCurrent(e.target.value); setDone(false); }} />
      <label htmlFor="pw-next">新しいパスワード(12 文字以上)</label>
      <input id="pw-next" type="password" autoComplete="new-password" value={next} onChange={e => { setNext(e.target.value); setDone(false); }} />
      <label htmlFor="pw-again">新しいパスワード(もう一度)</label>
      <input id="pw-again" type="password" autoComplete="new-password" value={again} onChange={e => { setAgain(e.target.value); setDone(false); }} />
      <button type="submit" className="apply" disabled={busy || !current || !next || !again}>パスワードを変える</button>
      {error && <p className="login-error" role="alert">{error}</p>}
      {done && <p className="hint">パスワードを変えました</p>}
    </form>
  );
}
