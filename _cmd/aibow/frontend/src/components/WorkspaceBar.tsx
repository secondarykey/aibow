// 入力欄の上: ワークスペース(会話ごとの設定。0035)。ファイルのツール・コマンドのツールは、ワークスペースのフォルダの中だけを扱う。
//
// 新しい会話の既定には入らないので、会話ごとに毎回選ぶ(前の会話のフォルダを引き継いで、別のフォルダを触らせないため)。
// どのフォルダを触らせているかがいつも見えるよう、設定の画面ではなく入力欄の上に出す。
// 決めていないときはフォルダのアイコンだけ、決めたら名前と ×。押すとワークスペースの一覧が上に開き、
// 最後の「フォルダから新しく作る」で、フォルダを選ぶダイアログ(Go の ChatService.ChooseWorkDir)から1つ作って選ぶ。
// 会話の ID のワークスペースがこの PC に無い(別の PC で選んだ・消した)ときは、そのことを出して選び直せるようにする。
import { useEffect, useRef, useState } from "react";
import { LuCheck, LuFolder, LuFolderPlus, LuTriangleAlert, LuX } from "react-icons/lu";
import type { WorkspaceView } from "../../bindings/github.com/secondarykey/aibow/ui";
import { errorMessage } from "../errors";
import { createFromFolder, mainFolder } from "../workspaces";

interface Props {
  workspaces: WorkspaceView[];
  selected: string; // 会話のワークスペースの ID。空なら決めていない
  onChange(id: string): void;
  onCreated(): void; // 新しく作った(一覧を読み直す)
  onError(text: string): void;
}

export function WorkspaceBar({ workspaces, selected, onChange, onCreated, onError }: Props) {
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

  const current = workspaces.find(w => w.id === selected);
  const missing = selected !== "" && !current;
  const create = () => {
    setOpen(false);
    createFromFolder().then(
      id => {
        if (!id) return; // 取り消した
        onCreated();
        onChange(id);
      },
      err => onError(errorMessage(err)),
    );
  };
  const title = current ? current.folders?.map(f => f.path).join("\n") ?? current.name
    : missing ? "別の PC で選んだか、消したワークスペース。この PC ではフォルダのツールを使えない" : "ワークスペースを選ぶ";

  return (
    <div className={"workdir-bar" + (selected ? " set" : "") + (missing ? " missing" : "")} ref={ref}>
      <button className="workdir" type="button" onClick={() => setOpen(o => !o)} title={title}>
        {missing ? <LuTriangleAlert /> : <LuFolder />}
        {current && <span className="workdir-name">{current.name}</span>}
        {missing && <span className="workdir-name">この PC にはこのワークスペースがありません</span>}
      </button>
      {selected && (
        <button className="workdir-clear" type="button" onClick={() => onChange("")} title="ワークスペースを外す">
          <LuX />
        </button>
      )}
      {open && (
        <div className="conv-tools-menu workspace-menu">
          {workspaces.map(w => (
            <button key={w.id} type="button" className="conv-tools-item" onClick={() => { setOpen(false); onChange(w.id); }} title={w.folders?.map(f => f.path).join("\n")}>
              <span className="conv-tools-check">{w.id === selected && <LuCheck />}</span>
              <span className="workspace-menu-name">{w.name}</span>
              {mainFolder(w)?.missing && <LuTriangleAlert className="workspace-menu-warn" title="メインのフォルダがこの PC に見つからない" />}
            </button>
          ))}
          {workspaces.length > 0 && <div className="workspace-menu-sep" />}
          <button type="button" className="conv-tools-item" onClick={create} title="フォルダを選んで、そのフォルダ1つのワークスペースを作る">
            <span className="conv-tools-check"><LuFolderPlus /></span>
            フォルダから新しく作る
          </button>
        </div>
      )}
    </div>
  );
}

// FolderBar は、ブラウザの会話の資料のフォルダ(aibow-server。0024 の決定事項 26)。アプリの作業フォルダと同じ場所に、名前だけを押せない形で出す。
// フォルダは管理者が配るプロファイルで決めるので、ここでは選べない。
export function FolderBar({ name }: { name: string }) {
  return (
    <div className="workdir-bar set">
      <span className="workdir folder-fixed" title="資料のフォルダ(aibow-server の PC。管理者が配ったプロファイルで決まる)。この会話のツールは、このフォルダの中を読み書きする">
        <LuFolder />
        <span className="workdir-name">{name}</span>
      </span>
    </div>
  );
}
