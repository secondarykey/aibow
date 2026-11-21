// ログのレベルを選ぶ・今日のログファイルの場所を出す(0052)。レベルは起動したあとに変えるだけで、保存しない(起動し直すと、起動したときのレベルに戻る)。
//
// 使うところは2つで、変わるプロセスが違う:
//
//	設定の画面の「基本設定」(デスクトップのアプリだけ)… アプリのログ(Go の ChatService.LogLevel / SetLogLevel。ブラウザからは呼べない)
//	管理の画面の「ログ」(ブラウザの管理者だけ)      … aibow-server のログ(Go の ui.Service.ServerLogLevel / SetServerLogLevel)
//
// どちらも Go が今のレベルを持っているので、開くたびに読み直す(画面は覚えない)。
//
// 下に今日のログファイルのパスを出し、コピーできるようにする(開くボタンは置かない。Win+R やエクスプローラーに貼って開く。0052 の決定事項 21)。
// ファイルは日ごとに切り替わるので、これも開くたびに読み直す。
import { useEffect, useState } from "react";
import { errorMessage } from "../errors";
import { CopyButton } from "./CopyButton";

// 名前は Go の log.LevelNames と同じ(細かい順)。説明はマウスを乗せたときに出す
const LEVELS: { name: string; label: string; title: string }[] = [
  { name: "trace", label: "Trace", title: "いちばん細かい。Debug より細かい記録も出す" },
  { name: "debug", label: "Debug", title: "調べるための細かい記録も出す(MCP のやり取りの中身など)" },
  { name: "info", label: "Info", title: "開発中・学習で観察するもの(生成の統計・ツールの実行・compact など)も出す" },
  { name: "notice", label: "Notice", title: "いつも残したいもの(起動した版・読み込んだモデル・変えた設定など)と、注意とエラー" },
  { name: "warn", label: "Warn", title: "注意とエラーだけ" },
  { name: "error", label: "Error", title: "エラーだけ" },
];

export function LogSettings({ id, label, title, load, save, loadFile, onError }: {
  id: string;
  label: string;                        // 見出しの言葉(「ログのレベル」)
  title: string;                        // 見出しに乗せたときの説明(どのプロセスのログか・起動し直すと戻る)
  load(): Promise<string>;              // 今のレベル
  save(name: string): Promise<string>;  // 変えて、変えたあとのレベルを返す
  loadFile(): Promise<string>;          // 今日のログファイルの絶対パス。ファイルに書いていなければ空
  onError(text: string): void;
}) {
  const [level, setLevel] = useState("");
  const [busy, setBusy] = useState(false);
  const [file, setFile] = useState<string | null>(null); // null は読んでいる途中

  useEffect(() => {
    load().then(setLevel).catch(err => onError(`ログのレベルを読めませんでした: ${errorMessage(err)}`));
    loadFile().then(setFile).catch(err => onError(`ログファイルの場所を読めませんでした: ${errorMessage(err)}`));
  }, []);

  const change = (name: string) => {
    setBusy(true);
    save(name)
      .then(setLevel)
      .catch(err => onError(`ログのレベルを変えられませんでした: ${errorMessage(err)}`))
      .finally(() => setBusy(false));
  };

  const current = LEVELS.find(l => l.name === level);
  return (
    <>
      <label htmlFor={id} title={title}>{label}</label>
      <select id={id} value={level} disabled={busy || level === ""} title={current?.title} onChange={e => change(e.target.value)}>
        {level === "" && <option value="">…</option>}
        {LEVELS.map(l => <option key={l.name} value={l.name} title={l.title}>{l.label}</option>)}
      </select>

      <label title="今日のログファイル。前の日のもの(.log.gz)と異常終了の記録も同じフォルダにある">ログファイル</label>
      {file === "" ? (
        <p className="hint">ログはファイルに残っていません</p>
      ) : file !== null && (
        <div className="place-row">
          <span className="place-path" title={file}>{file}</span>
          <CopyButton text={file} className="place-remove" />
        </div>
      )}
    </>
  );
}
