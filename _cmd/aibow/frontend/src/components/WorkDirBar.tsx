// 入力欄の上: 作業フォルダ(会話ごとの設定)。ファイルのツール(list_files / read_file)は、このフォルダの中だけを扱う。
//
// 新しい会話の既定には入らないので、会話ごとに毎回選ぶ(前の会話のフォルダを引き継いで、別のフォルダを触らせないため)。
// どのフォルダを触らせているかがいつも見えるよう、設定の画面ではなく入力欄の上に出す。
// フォルダを選ぶダイアログは Go の側で出す(ChatService.ChooseWorkDir)。
import { LuFolder, LuX } from "react-icons/lu";
import { ChatService } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
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

// baseName はパスの最後の名前(D:\Work\example → example)。ドライブそのもの(D:\)ならそのまま
function baseName(path: string): string {
  const parts = path.split(/[\\/]+/).filter(Boolean);
  return parts.length > 1 ? parts[parts.length - 1] : path;
}
