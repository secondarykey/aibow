// 入力欄の上: 作業フォルダ(会話ごとの設定)。ファイルのツール(list_files / read_file)は、このフォルダの中だけを扱う。
//
// 新しい会話の既定には入らないので、会話ごとに毎回選ぶ(前の会話のフォルダを引き継いで、別のフォルダを触らせないため)。
// どのフォルダを触らせているかがいつも見えるよう、設定の画面ではなく入力欄の上に出す。
// フォルダを選ぶダイアログは Go の側で出す(ChatService.ChooseWorkDir)。
import { LuFolder, LuX } from "react-icons/lu";
import { ChatService } from "../backend";
import { errorMessage } from "../errors";

interface Props {
  workDir: string; // 空なら決めていない
  onChange(workDir: string): void;
  onError(text: string): void;
}

export function WorkDirBar({ workDir, onChange, onError }: Props) {
  const choose = () => {
    ChatService.ChooseWorkDir(workDir).then(
      dir => dir && onChange(dir), // 取り消したら空が返るので、何もしない
      err => onError(errorMessage(err)),
    );
  };
  const title = workDir || "作業フォルダを選ぶ";

  return (
    <div className={"workdir-bar" + (workDir ? " set" : "")}>
      <button className="workdir" type="button" onClick={choose} title={title}>
        <LuFolder />
        {workDir && <span className="workdir-name">{baseName(workDir)}</span>}
      </button>
      {workDir && (
        <button className="workdir-clear" type="button" onClick={() => onChange("")} title="作業フォルダを外す">
          <LuX />
        </button>
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

// baseName はパスの最後の名前(D:\Work\example → example)。ドライブそのもの(D:\)ならそのまま
function baseName(path: string): string {
  const parts = path.split(/[\\/]+/).filter(Boolean);
  return parts.length > 1 ? parts[parts.length - 1] : path;
}
