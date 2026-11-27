// workspaces.ts: その PC のワークスペース(名前の付いたフォルダの組。Go の aibow.Workspaces・workspaces.json。0035)。
//
// 入力欄の上の選ぶ部品(WorkspaceBar)と、設定の画面の「ワークスペース」が同じ一覧を使う。
// パスは PC のものなので、会話はワークスペースの ID だけを持つ。ID が一覧に無ければ「この PC にはこのワークスペースがありません」。
// ブラウザ(aibow-server)の会話では使わない(サーバーのワークスペース。0024)。
import { useCallback, useEffect, useState } from "react";
import { ChatService, isBrowser } from "./backend";
import { errorMessage } from "./errors";
import type { FolderView, WorkspaceList, WorkspaceView } from "../bindings/github.com/secondarykey/aibow/ui";

const EMPTY: WorkspaceList = { workspaces: [], path: "" };

// useWorkspaces は、ワークスペースの一覧。開いたときに取り、reload で取り直す(設定の画面で変えたあと・作ったあと)
export function useWorkspaces(onError: (text: string) => void): [WorkspaceList, () => void] {
  const [list, setList] = useState<WorkspaceList>(EMPTY);
  const reload = useCallback(() => {
    if (isBrowser) return;
    ChatService.Workspaces().then(
      l => setList({ ...l, workspaces: l.workspaces ?? [] }),
      err => onError(`ワークスペースの一覧を取得できません: ${errorMessage(err)}`),
    );
  }, [onError]);
  useEffect(reload, [reload]);
  return [list, reload];
}

// mainFolder は、ワークスペースのメインのフォルダ(run_command のカレント・${workdir} の MCP のサーバーに渡すフォルダ)
export function mainFolder(ws: WorkspaceView | undefined): FolderView | undefined {
  return ws?.folders?.find(f => f.main);
}

// createFromFolder は、フォルダを選ぶダイアログを出し、選んだフォルダ1つのワークスペースを作る。取り消したら null。
// reuse なら、同じフォルダ1つのワークスペースがあればそれを返す(existing。入力欄の上。呼んだ側がトーストで知らせる)。
// reuse でなければ、同じフォルダでも新しく作る(設定の画面。名前が重なれば「名前 (2)」。0049)
export async function createFromFolder(reuse: boolean): Promise<{ id: string; name: string; existing: boolean } | null> {
  const dir = await ChatService.ChooseWorkDir("");
  if (!dir) return null;
  const r = await ChatService.CreateWorkspace(dir, reuse);
  return { id: r.workspace.id, name: r.workspace.name, existing: r.existing };
}
