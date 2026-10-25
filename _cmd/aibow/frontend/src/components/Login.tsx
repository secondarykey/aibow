// ログインの画面(ブラウザで、aibow-server がログインを要る設定のとき。0017 の 10-1)。
// 画面の中央に、名前・パスワード・ログインのボタンだけを置く(2026-10-23、ユーザーと決めた)。
// デスクトップのアプリと、ログインが要らない形の aibow-server では出さない(LoginGate がそのまま中身を描く)。
// ログインできる管理者がまだいなければ(初めての起動など)、サーバーの PC で管理者を作るよう出す(0020 の決定事項 2)。
// 招待・再設定のリンク(URL の #reset=<合言葉>。0022)で開いたときは、ログインの代わりにパスワードを決める画面を出す。
import { useEffect, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { linkUser, login, me, onUnauthorized, applyLink } from "../backend";
import { errorMessage } from "../errors";

// LoginGate は、ログインが要るのにしていなければログインの画面を、していれば children(アプリの画面)を描く。
// 使っている途中で期限が切れた(呼び出しが 401 で断られた)ときもログインの画面に戻し、ログインしたら画面を読み直す
// (知らせのつながり(SSE)や会話の一覧を、ログインした人のもので作り直すため)。
export function LoginGate({ children }: { children: ReactNode }) {
  const [needed, setNeeded] = useState(me.login && me.name === "" && !me.viaKey);
  const [expired, setExpired] = useState(false);
  useEffect(() => onUnauthorized(() => setExpired(true)), []);

  // リンクで開いたときは、ログインしていてもパスワードを決める画面にする(合言葉は # の後ろなので、サーバーには届いていない)
  const [reset] = useState(() => new URLSearchParams(location.hash.slice(1)).get("reset"));
  if (reset !== null && me.login) {
    // 決めたら(またはやめたら)URL から合言葉を消し、ログインした人の画面で読み直す
    const leave = () => {
      history.replaceState(null, "", location.pathname + location.search);
      location.reload();
    };
    return <SetPassword token={reset} onDone={leave} onCancel={leave} />;
  }

  if (needed || expired) {
    return <Login onDone={() => (expired ? location.reload() : setNeeded(false))} />;
  }
  return <>{children}</>;
}

// SetPassword は、招待・再設定のリンクで本人がパスワードを決める画面(0022)。決めたら、そのままログインしている
function SetPassword({ token, onDone, onCancel }: { token: string; onDone(): void; onCancel(): void }) {
  const [name, setName] = useState<string | null>(null);
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    linkUser(token).then(setName, err => setError(errorMessage(err)));
  }, [token]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (password !== again) {
      setError("2つのパスワードが違います");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await applyLink(token, password);
      onDone();
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  return (
    <div className="login">
      <form className="login-box" onSubmit={submit}>
        <h1 className="login-title">AIBow</h1>
        {name !== null && <div className="login-note">{name} のパスワードを決めます</div>}
        {name !== null && (
          <>
            <input
              type="password" placeholder="新しいパスワード(12 文字以上)" aria-label="新しいパスワード" autoComplete="new-password" autoFocus
              value={password} onChange={e => setPassword(e.target.value)}
            />
            <input
              type="password" placeholder="もう一度" aria-label="新しいパスワード(もう一度)" autoComplete="new-password"
              value={again} onChange={e => setAgain(e.target.value)}
            />
            <button type="submit" className="primary" disabled={busy || password === "" || again === ""}>決める</button>
          </>
        )}
        {error && <div className="login-error" role="alert">{error}</div>}
        {name === null && error && <button type="button" onClick={onCancel}>ログインの画面へ</button>}
      </form>
    </div>
  );
}

function Login({ onDone }: { onDone(): void }) {
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await login(name, password);
      onDone();
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  return (
    <div className="login">
      <form className="login-box" onSubmit={submit}>
        <h1 className="login-title">AIBow</h1>
        <input
          type="text" placeholder="名前" aria-label="名前" autoComplete="username" autoFocus
          value={name} onChange={e => setName(e.target.value)}
        />
        <input
          type="password" placeholder="パスワード" aria-label="パスワード" autoComplete="current-password"
          value={password} onChange={e => setPassword(e.target.value)}
        />
        <button type="submit" className="primary" disabled={busy || name.trim() === "" || password === ""}>ログイン</button>
        {error && <div className="login-error" role="alert">{error}</div>}
        {me.setup && (
          <div className="login-note">
            管理者がいません。サーバーの PC で次を打ってください
            <code>aibow-server user add &lt;名前&gt;</code>
            <code>aibow-server user admin &lt;名前&gt;</code>
          </div>
        )}
      </form>
    </div>
  );
}
