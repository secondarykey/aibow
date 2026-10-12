// 入力欄の下の右端: メーター(モデルに渡す文字列のトークン数 / 会話の上限)
//
// 数えているのは、次の送信でモデルに渡す文字列(チャットテンプレートを当てたもの。要約を使うなら要約込み)。
// 送信で足す新しい発言の分は、まだ入っていない。
// 上限を超えていたら、次の送信で compact が働く(none なら送信が止まる)。
import type { Usage } from "../../bindings/aibow";

// 橙にする割合。ここを超えたら、もうすぐ compact が働く
const WARN_RATIO = 0.9;

export function Meter({ usage }: { usage: Usage | null }) {
  if (!usage) {
    return <span className="meter" title="トークン数はモデルで数えるので、モデルを読み込むと出ます">– / –</span>;
  }
  const ratio = usage.limit > 0 ? usage.tokens / usage.limit : 0;
  const level = ratio > 1 ? "over" : ratio > WARN_RATIO ? "warn" : "";
  const title = ratio > 1
    ? "会話の上限を超えています。次の送信で compact が働きます(none の会話では送信が止まります)"
    : "次の送信でモデルに渡す文字列のトークン数 / 会話の上限";
  return (
    <span className={`meter ${level}`} title={title}>
      <span className="meter-bar"><span style={{ width: `${Math.min(ratio, 1) * 100}%` }} /></span>
      {usage.tokens.toLocaleString()} / {usage.limit.toLocaleString()}
    </span>
  );
}
