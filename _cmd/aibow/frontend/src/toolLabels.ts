// 組み込みのツール(aibow の中で動くツール)の日本語の説明。モデルに見せる説明は英語。
// ツールのカードでは名前にマウスを乗せると出し、設定の画面の「ツール」では名前の横に出す。
export const TOOL_LABELS: Record<string, string> = {
  get_current_time: "現在の日時を調べる",
  calculate: "式を計算する",
  list_files: "作業フォルダの中のファイルを並べる",
  read_file: "作業フォルダの中のファイルを読む",
  write_file: "作業フォルダの中のファイルを丸ごと書く",
  edit_file: "作業フォルダの中のファイルの一部を置き換える",
  run_command: "作業フォルダでコマンドを実行する",
};
