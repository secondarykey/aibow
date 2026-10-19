// profileRules.ts: プロファイルの決まり(固定・使えない。0014)を画面で使うための小さな部品。
//
// 決まりを判定するのは Go(rules.go)で、保存のときにも Go が断る。画面は Go が返したプロファイルを見て、
// 押せなくして理由を添えるだけにする(同じ判断を書き直さない、と同じ考え方。名前の一覧だけを持つ)。
import type { ConversationSettings, Profile, ProfileValues } from "../bindings/github.com/secondarykey/aibow";

// 固定できる項目の名前(Go の Fix… と同じ)
export const FIX = {
  model: "model",
  system: "system",
  compact: "compact",
  temperature: "temperature",
  presencePenalty: "presence_penalty",
  think: "think",
  historyTokens: "history_tokens",
  replyTokens: "reply_tokens",
  toolGroups: "tool_groups",
} as const;

// Lock は、設定の入力欄に渡す決まりの見せ方。
//   onToggle があれば、プロファイルを編集している(項目ごとに固定の印を付け外しできる)。
//   無ければ、会話の設定(固定した項目は押せなくして「プロファイルで固定」と添える)。
export interface Lock {
  fixed: string[];
  profile: string; // プロファイルの名前(理由に出す)
  onToggle?(item: string): void;
}

// lockOf は、会話のプロファイルから、会話の設定の画面に渡す Lock を作る。プロファイルを持たない会話なら undefined。
export function lockOf(p: Profile | null | undefined): Lock | undefined {
  if (!p) return undefined;
  return { fixed: p.rules.fixed ?? [], profile: p.name };
}

// locked は、会話の設定で、この項目を押せなくするか(プロファイルで固定)。プロファイルの編集では押せる
export function locked(lock: Lock | undefined, item: string): boolean {
  return !!lock && !lock.onToggle && lock.fixed.includes(item);
}

export function fixedTitle(lock: Lock): string {
  return `プロファイル「${lock.profile}」で固定されているので、この会話では変えられません`;
}

// offGroup は、グループ(組み込みなら空、MCP ならサーバーの名前)がプロファイルで使えないか。
export function offGroup(p: Profile | null | undefined, group: string): boolean {
  if (!p) return false;
  return group === "" ? !!p.rules.off?.builtin : (p.rules.off?.servers ?? []).includes(group);
}

// asSettings は、プロファイルの値を会話の設定の入力欄に渡す形にする(作業フォルダ・会話だけのツールの扱いは無い)。
export function asSettings(v: ProfileValues): ConversationSettings {
  return { ...v, mcp_servers: v.mcp_servers ?? [], work_dir: "" } as ConversationSettings;
}

// valuesPatch は、会話の設定の入力欄が返した変更から、プロファイルの値の項目だけを取り出す。
export function valuesPatch(patch: Partial<ConversationSettings>): Partial<ProfileValues> {
  const { work_dir: _w, tool_policies: _t, ...rest } = patch;
  return rest as Partial<ProfileValues>;
}
