// 入力欄の上(作業フォルダの隣): この会話のツール(会話ごとの設定)。組み込みのツールと、MCP のサーバーごとにオン・オフする。
//
// ツールの説明は毎回プロンプトに入るので、全部を載せると会話の上限を食い尽くす(Blender だけで 26 個)。そこで会話ごとに選べるようにする。
// 新しい会話では、設定の画面の「ツール」でチェックしているもの(組み込み・MCP のサーバー)が最初から選ばれている。
// 「ツール」でチェックを外したものも、ここではいつでも選べる(「ツール」のチェックは、新しい会話で最初から選ぶかだけ)。
// レンチのアイコンの横に、この会話で使うものの名前を出す。押すと一覧が上に開き、オン・オフできる。
import { useEffect, useRef, useState } from "react";
import { LuCheck, LuWrench } from "react-icons/lu";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import type { Profile } from "../../bindings/github.com/secondarykey/aibow";
import { FIX, offGroup } from "../profileRules";

// BUILTIN は、組み込みのツールのグループの名前(設定の画面の「ツール」と同じ)
export const BUILTIN = "組み込み";

interface Props {
  builtinOn: boolean;        // この会話で組み込みのツールを使う
  servers: ServerInfo[];     // settings.json の MCP のサーバー
  selected: string[];        // この会話で使う MCP のサーバー
  workDir: string;           // 作業フォルダ(${workdir} を使うサーバーは、決めていないと使えない)
  profile: Profile | null;   // この会話(新しい会話の画面では、選んだ)プロファイル。使えないグループと、グループの固定(0014)
  onBuiltinChange(on: boolean): void;
  onServersChange(selected: string[]): void;
}

export function ToolBar({ builtinOn, servers, selected, workDir, profile, onBuiltinChange, onServersChange }: Props) {
  const fixed = !!profile?.rules.fixed?.includes(FIX.toolGroups);
  // 押せない理由(プロファイルで使えない・固定)。押せるなら空
  const lockOf = (group: string) =>
    offGroup(profile, group) ? `プロファイル「${profile?.name}」で使えない`
      : fixed ? `使うツールのグループはプロファイル「${profile?.name}」で固定されている` : "";
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // 一覧の外を押したら閉じる
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [open]);

  // settings.json から消したサーバーの名前が会話に残っていても、出さない
  const using = [
    ...(builtinOn && !offGroup(profile, "") ? [BUILTIN] : []),
    ...servers.filter(s => selected.includes(s.name) && !offGroup(profile, s.name)).map(s => s.name),
  ];
  const toggle = (name: string) =>
    onServersChange(selected.includes(name) ? selected.filter(n => n !== name) : [...selected, name]);

  return (
    <div className={"conv-tools" + (using.length ? " set" : "")} ref={ref}>
      <button className="conv-tools-btn" type="button" onClick={() => setOpen(o => !o)} title={using.length ? "この会話のツール" : "この会話のツールを選ぶ"}>
        <LuWrench />
        {using.length > 0 && <span className="conv-tools-names">{using.join(", ")}</span>}
      </button>
      {open && (
        <div className="conv-tools-menu">
          <button
            type="button"
            className="conv-tools-item"
            disabled={lockOf("") !== ""}
            onClick={() => onBuiltinChange(!builtinOn)}
            title={lockOf("") || "aibow の中で動くツール(時刻・計算・ファイル・コマンド)"}
          >
            <span className="conv-tools-check">{builtinOn && !offGroup(profile, "") && <LuCheck />}</span>
            {BUILTIN}
          </button>
          {servers.map(s => {
            const needsDir = s.uses_work_dir && !workDir;
            const reason = lockOf(s.name);
            return (
              <button
                key={s.name}
                type="button"
                className={"conv-tools-item" + (needsDir ? " needs-dir" : "")}
                disabled={reason !== ""}
                onClick={() => toggle(s.name)}
                title={reason || (needsDir ? "作業フォルダを決めると使えます(このサーバーは作業フォルダで起動します)" : s.http ? "URL でつなぐ MCP のサーバー" : "aibow が起動する MCP のサーバー")}
              >
                <span className="conv-tools-check">{selected.includes(s.name) && !offGroup(profile, s.name) && <LuCheck />}</span>
                {s.name}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
