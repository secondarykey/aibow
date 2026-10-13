// 入力欄の上(作業フォルダの隣): この会話で使う MCP のサーバー(会話ごとの設定)。
//
// ツールの説明は毎回プロンプトに入るので、全部のサーバーを載せると会話の上限を食い尽くす(Blender だけで 26 個)。
// そこで会話ごとに使うサーバーを選ぶ。新しい会話の既定は「使わない」。
// 選んでいないときはプラグのアイコンだけ、選んだらサーバーの名前を枠付きで出す。押すとサーバーの一覧が開き、オン・オフできる。
// settings.json にサーバーが1つも無ければ、何も出さない。
import { useEffect, useRef, useState } from "react";
import { LuCheck, LuPlug } from "react-icons/lu";
import type { ServerInfo } from "../../bindings/aibow/mcpclient";

interface Props {
  servers: ServerInfo[]; // settings.json に書いたサーバー
  selected: string[];    // この会話で使うサーバー
  workDir: string;       // 作業フォルダ(${workdir} を使うサーバーは、決めていないと使えない)
  onChange(selected: string[]): void;
}

export function McpBar({ servers, selected, workDir, onChange }: Props) {
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

  if (servers.length === 0) return null;
  // settings.json から消したサーバーの名前が会話に残っていても、出さない
  const using = servers.filter(s => selected.includes(s.name));
  const toggle = (name: string) =>
    onChange(selected.includes(name) ? selected.filter(n => n !== name) : [...selected, name]);
  const title = using.length === 0 ? "この会話で使う MCP のサーバーを選ぶ" : "この会話で使う MCP のサーバー";

  return (
    <div className={"mcp-bar" + (using.length ? " set" : "")} ref={ref}>
      <button className="mcp" type="button" onClick={() => setOpen(o => !o)} title={title}>
        <LuPlug />
        {using.length > 0 && <span className="mcp-names">{using.map(s => s.name).join(", ")}</span>}
      </button>
      {open && (
        <div className="mcp-menu">
          {servers.map(s => {
            const needsDir = s.uses_work_dir && !workDir;
            return (
              <button
                key={s.name}
                type="button"
                className={"mcp-item" + (needsDir ? " needs-dir" : "")}
                onClick={() => toggle(s.name)}
                title={needsDir ? "作業フォルダを決めると使えます(このサーバーは作業フォルダで起動します)" : s.http ? "URL でつなぐサーバー" : "aibow が起動するサーバー"}
              >
                <span className="mcp-check">{selected.includes(s.name) && <LuCheck />}</span>
                {s.name}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
