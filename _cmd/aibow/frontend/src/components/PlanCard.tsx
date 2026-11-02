// 作業の予定のカード(update_plan。0037 の段3)。ほかのツールのような「引数と結果」ではなく、予定をチェックリストとして出す。
// 予定はモデルの呼び出しの引数(items)にあるので、それを読んで描く。結果の文(「予定を覚えました(…)」)は出さない。
// 読めない呼び出し(書式の崩れ・エラー・MCP のサーバーの同じ名前のツール)は、ふつうのツールのカードで出す(planOf が null)。
import { LuCircle, LuCircleCheck, LuCircleDot, LuListChecks } from "react-icons/lu";
import type { ToolView } from "../transcript";

export interface PlanItem {
  text: string;
  status: "pending" | "in_progress" | "done";
}

const STATUSES = ["pending", "in_progress", "done"];

// planOf は、カードが update_plan で、予定として読めるならその項目を返す。読めなければ null。
export function planOf(tool: ToolView): PlanItem[] | null {
  if (tool.name !== "update_plan" || tool.server || tool.raw || tool.error || tool.confirm) return null;
  const arg = tool.args.find(a => a.name === "items");
  if (!arg) return null;
  try {
    // 値は JSON の配列の文字(Go の tool.Arg の持ち方)
    const v: unknown = JSON.parse(arg.value);
    if (!Array.isArray(v)) return null;
    const items: PlanItem[] = [];
    for (const x of v) {
      if (typeof x !== "object" || x === null) return null;
      const { text, status } = x as Record<string, unknown>;
      if (typeof text !== "string" || typeof status !== "string" || !STATUSES.includes(status)) return null;
      items.push({ text, status: status as PlanItem["status"] });
    }
    return items;
  } catch {
    return null;
  }
}

const ICONS = { pending: LuCircle, in_progress: LuCircleDot, done: LuCircleCheck };
const LABELS = { pending: "まだ", in_progress: "取りかかっている", done: "済み" };

export function PlanCard({ items }: { items: PlanItem[] }) {
  const done = items.filter(it => it.status === "done").length;
  return (
    <div className="plan-card">
      <div className="plan-head">
        <LuListChecks className="tool-icon" />
        <span title="update_plan: 作業の予定をチェックリストとして書く">予定</span>
        <span className="plan-count">{items.length === 0 ? "空にしました" : `${done} / ${items.length} 済み`}</span>
      </div>
      {items.length > 0 && (
        <ul className="plan-items">
          {items.map((it, i) => {
            const Icon = ICONS[it.status];
            return (
              <li key={i} className={`plan-item ${it.status}`}>
                <Icon className="plan-mark" title={LABELS[it.status]} />
                <span>{it.text}</span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
