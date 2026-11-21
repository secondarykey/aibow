// ScreenErrors は、設定・管理・会話の設定の画面の上に出すエラー(「直前の操作の結果」。0043)。
// エラーは画面の一番上に出すが、押したボタン(会話の設定の「適用」など)は下のほうにあることが多い。
// スクロールしていると、出たエラーが見えないまま「エラーが出ない」ように見え、会話に戻って初めて気づくことになった
// (A9:会話の上限に 1,000,000 を入れて「適用」。2026-11-20、ユーザーの指摘)。
// そこで、新しいエラーが出たら、そのエラーが見えるところまでスクロールする(block: "nearest" なので、見えていれば動かない)。
import { useEffect, useRef } from "react";

export function ScreenErrors({ errors }: { errors: { id: number; text: string }[] }) {
  const last = useRef<HTMLDivElement>(null);
  const lastId = errors.length > 0 ? errors[errors.length - 1].id : null;
  useEffect(() => {
    if (lastId !== null) last.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [lastId]);
  return (
    <>
      {errors.map(e => (
        <div key={e.id} ref={e.id === lastId ? last : undefined} className="msg error">{e.text}</div>
      ))}
    </>
  );
}
