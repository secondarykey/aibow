// 入力欄の下の右端: メーター(モデルに渡す文字列のトークン数 / 会話の上限)
//
// 数えているのは、次の送信でモデルに渡す文字列(チャットテンプレートを当てたもの。要約を使うなら要約込み)。
// 送信で足す新しい発言の分は、まだ入っていない。
// 上限を超えていたら、次の送信で compact が働く(none なら送信が止まる)。
//
// summary の会話では、トークン数(「n / 上限」の n)を押すと今すぐ要約する(onCompact)。
// 要約は「n を減らす」操作なので、別のボタンを並べずに数そのものを押せるようにしている。
// 数を両側から矢印で挟み(→| n |←)、縮める操作だと分かるようにする。
import { LuArrowLeftToLine, LuArrowRightToLine } from "react-icons/lu";
import type { Usage } from "../../bindings/aibow";

// 橙にする割合。ここを超えたら、もうすぐ compact が働く
const WARN_RATIO = 0.9;

interface Props {
  usage: Usage | null;
  onCompact?: () => void;    // 今すぐ要約(summary の会話だけ渡す)
  compactDisabled?: boolean; // 生成中・読み込み中は押せない
}

export function Meter({ usage, onCompact, compactDisabled }: Props) {
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
      {onCompact
        ? (
          <button className="meter-tokens" disabled={compactDisabled} onClick={onCompact} title="要約">
            <LuArrowRightToLine />{usage.tokens.toLocaleString()}<LuArrowLeftToLine />
          </button>
        )
        : usage.tokens.toLocaleString()}
      {" / "}{usage.limit.toLocaleString()}
    </span>
  );
}
