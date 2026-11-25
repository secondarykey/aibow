// 入力欄の上: ワークスペース(会話ごとの設定。0035)。ファイルのツール・コマンドのツールは、ワークスペースのフォルダの中だけを扱う。
//
// 新しい会話の既定には入らないので、会話ごとに毎回選ぶ(前の会話のフォルダを引き継いで、別のフォルダを触らせないため)。
// どのフォルダを触らせているかがいつも見えるよう、設定の画面ではなく入力欄の上に出す。
// 決めていないときはフォルダのアイコンだけ、決めたら名前。押すとワークスペースの一覧が上に開き、
// 「フォルダから新しく作る」で、フォルダを選ぶダイアログ(Go の ChatService.ChooseWorkDir)から1つ作って選ぶ。
// 同じフォルダ1つのワークスペースが既にあれば、作らずにそれを選び、そのことをトーストで知らせる(0049 の決定事項 2)。
// 外すのは一覧の最後の「なし」(以前は名前の右の ×。隣の worktree の切り替えを押すつもりで外してしまうので、一覧に移した。2026-11-04、ユーザーの指示)。
// 会話の ID のワークスペースがこの PC に無い(別の PC で選んだ・消した)ときは、そのことを出して選び直せるようにする。
import { useEffect, useRef, useState } from "react";
import { LuCheck, LuFolder, LuFolderPlus, LuInfo, LuTriangleAlert } from "react-icons/lu";
import type { WorkspaceView } from "../../bindings/github.com/secondarykey/aibow/ui";
import { errorMessage } from "../errors";
import { createFromFolder, mainFolder } from "../workspaces";

interface Props {
  workspaces: WorkspaceView[];
  selected: string; // 会話のワークスペースの ID。空なら決めていない
  onChange(id: string): void;
  onCreated(): void; // 新しく作った(一覧を読み直す)
  onNotice(text: string): void; // 既にあるワークスペースを選んだときのお知らせ(トースト)
  onError(text: string): void;
}

export function WorkspaceBar({ workspaces, selected, onChange, onCreated, onNotice, onError }: Props) {
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
    createFromFolder(true).then(
      r => {
        if (!r) return; // 取り消した
        if (r.existing) onNotice(`既にあるワークスペース「${r.name}」を選びました`);
        else onCreated();
        onChange(r.id);
      },
      err => onError(errorMessage(err)),
    );
  };
  const title = current ? current.folders?.map(f => f.path).join("\n") ?? current.name
    : missing ? "別の PC で選んだか、削除したワークスペース。この PC ではフォルダのツールを使えない" : "ワークスペースを選ぶ";

  return (
    <div className={"workdir-bar" + (selected ? " set" : "") + (missing ? " missing" : "")} ref={ref}>
      <button className="workdir" type="button" onClick={() => setOpen(o => !o)} title={title}>
        {missing ? <LuTriangleAlert /> : <LuFolder />}
        {current && <span className="workdir-name">{current.name}</span>}
        {missing && <span className="workdir-name">この PC にはこのワークスペースがありません</span>}
      </button>
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
          <button type="button" className="conv-tools-item" onClick={create} title="フォルダを選んで、そのフォルダ1つのワークスペースを作成する(同じフォルダのものがあれば、それを選ぶ)">
            <span className="conv-tools-check"><LuFolderPlus /></span>
            フォルダから作成
          </button>
          <button type="button" className="conv-tools-item" onClick={() => { setOpen(false); if (selected) onChange(""); }} title="ワークスペースを外す">
            <span className="conv-tools-check">{!selected && <LuCheck />}</span>
            なし
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
      <span className="workdir folder-fixed" title="資料のフォルダ(aibow-server の PC。管理者が配布したプロファイルで決まる)。この会話のツールは、このフォルダの中を読み書きする">
        <LuFolder />
        <span className="workdir-name">{name}</span>
      </span>
    </div>
  );
}

// BrowserOnlyFolderNote は、アプリで開いた会話のプロファイルに資料のフォルダがあるときの知らせ(0064)。
// 会話はサーバーモードのアプリとブラウザで共有されるが、ツールが動くのは開いた側の PC(アプリなら手元のワークスペース)。
// ブラウザで資料のフォルダを使っていた会話をアプリで続けると、同じフォルダのつもりで手元のファイルを読み書きしかねないので、
// そのフォルダを使おうとする入力欄の上(ワークスペースの隣)で知らせる。選べるものではないので押せない形にする。
export function BrowserOnlyFolderNote({ name }: { name: string }) {
  return (
    <span
      className="folder-browser-only"
      title="この会話のツールはこの PC で動くので、aibow-server の資料のフォルダは読み書きできません。資料のフォルダを使うときは、ブラウザで aibow-server を開いてこの会話を続けてください"
    >
      <LuInfo className="icon" />
      <span className="folder-browser-only-text">資料のフォルダ『{name}』はブラウザでだけ使えます(ここでは手元のフォルダ)</span>
    </span>
  );
}
