// ログインの画面(ブラウザで、aibow-server がログインを要る設定のとき。0017 の 10-1)。
// 画面の中央に、名前・パスワード・ログインのボタンだけを置く(2026-10-23、ユーザーと決めた)。
// デスクトップのアプリと、ログインが要らない形の aibow-server では出さない(LoginGate がそのまま中身を描く)。
// ログインできる管理者がまだいなければ(初めての起動など)、サーバーの PC で管理者を作るよう出す(0020 の決定事項 2)。
import { useEffect, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { login, me, onUnauthorized } from "../backend";
import { errorMessage } from "../errors";

// LoginGate は、ログインが要るのにしていなければログインの画面を、していれば children(アプリの画面)を描く。
// 使っている途中で期限が切れた(呼び出しが 401 で断られた)ときもログインの画面に戻し、ログインしたら画面を読み直す
// (知らせのつながり(SSE)や会話の一覧を、ログインした人のもので作り直すため)。
export function LoginGate({ children }: { children: ReactNode }) {
  const [needed, setNeeded] = useState(me.login && me.name === "" && !me.viaKey);
  const [expired, setExpired] = useState(false);
  useEffect(() => onUnauthorized(() => setExpired(true)), []);

  if (needed || expired) {
    return <Login onDone={() => (expired ? location.reload() : setNeeded(false))} />;
  }
  return <>{children}</>;
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
            管理者がいません。サーバーの PC で <code>aibow-server user add &lt;名前&gt;</code> と <code>aibow-server user admin &lt;名前&gt;</code> を打ってください
          </div>
        )}
      </form>
    </div>
  );
}
