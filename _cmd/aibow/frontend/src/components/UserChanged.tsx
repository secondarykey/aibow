// UserChangedGate は、同じブラウザのほかのタブで別の人がログインしたら、この画面を閉じて知らせる(ブラウザだけ)。
//
// cookie はタブで分け合うので、ほかのタブで bob がログインすると、alice の画面のまま bob として呼ぶことになる
// (名前・管理者のタブ・会話の一覧は alice のまま、送った会話や操作は bob のもの。2026-11-20、確認表の A10 で見つかった)。
// 断るのは Go の口なので、bob が alice の権限で動くことは無いが、誰の画面かが分からなくなるのが危ない。
// 画面を作り直すより、読み込み直してもらう(会話の一覧・知らせのつながり・管理者のタブを、ログインした人のもので作り直す。LoginGate と同じ考え)。
import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { me, onUserChanged } from "../backend";

export function UserChangedGate({ children }: { children: ReactNode }) {
  const [now, setNow] = useState<string | null>(null);
  useEffect(() => onUserChanged(setNow), []);
  if (now === null) return <>{children}</>;
  return (
    <div className="login">
      <div className="login-box">
        <h1 className="login-title">AIBow</h1>
        <div className="login-note" role="alert">
          このブラウザで、別の人({now})がログインしました。この画面は {me.name} のものだったので閉じました。
          読み込み直すと {now} の画面になります。{me.name} で使うときは、読み込み直してからログアウトし、{me.name} でログインし直してください。
        </div>
        <button type="button" className="primary" onClick={() => location.reload()}>読み込み直す</button>
      </div>
    </div>
  );
}
