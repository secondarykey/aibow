// 使用量(0033)。aibow-server の生成1回ごとの記録を、まとめて表で見せる(まず表だけ。グラフは見てから決める。決めたこと 17)。
//
//	UsageAdmin … 管理の画面の「使用量」のタブ。期間(今日・7日・30日)と利用者ごとの表。人を押すと、その人のモデルごと・日ごとの表
//	MyUsage    … 設定の画面の「アカウント」のタブ。自分の今日・7日・30日
//
// 主に見せるのは回数・GPU の秒数・生成トークン数(決定事項 2)。ローカルLLMは料金が無く、限られているのは GPU の席なので、
// GPU の秒数(プロンプト + 生成 + 画像のエンコード)がいちばん大事な数字。ほかの数字はバルーン(title)で出す。
// だれの分を見られるかは Go の口が決める(全員の分は管理者だけ。画面は出し分けるだけ)。
import { useEffect, useState } from "react";
import { admin, myUsage, type MyUsage as Mine, type UsageDetail, type UsageTotal } from "../backend";
import { showName } from "../directory";
import { errorMessage } from "../errors";

const PERIODS = [
  { days: 1, label: "今日" },
  { days: 7, label: "7日" },
  { days: 30, label: "30日" },
];

// formatSec は、GPU の秒数を読みやすくする(1分より短ければ秒、1時間より短ければ分と秒、それより長ければ時間と分)
function formatSec(sec: number): string {
  if (sec < 60) return `${sec.toFixed(1)} 秒`;
  const s = Math.round(sec);
  if (s < 3600) return `${Math.floor(s / 60)} 分 ${s % 60} 秒`;
  return `${Math.floor(s / 3600)} 時間 ${Math.floor((s % 3600) / 60)} 分`;
}

// details は、表に出さない数字(バルーンに出す)
function details(t: UsageTotal): string {
  const lines = [
    `要約 ${t.summaries} 回(回数のうち)`,
    `プロンプト ${t.prompt_tokens.toLocaleString()} トークン(KV キャッシュの使い回し ${t.reused_tokens.toLocaleString()})`,
  ];
  if (t.image_tokens > 0) lines.push(`画像 ${t.image_tokens.toLocaleString()} トークン`);
  lines.push(`順番待ち ${formatSec(t.wait_sec)}`);
  return lines.join("\n");
}

// Head は表の見出し(名前の列の見出しと、数字の3列)
function Head({ first }: { first: string }) {
  return (
    <>
      <span className="model-table-head">{first}</span>
      <span className="model-table-head num" title="生成の回数(返事・ツールのループの1回・要約のどれも1回)">回数</span>
      <span className="model-table-head num" title="GPU を使った秒数(プロンプトの読み込み + 生成 + 画像のエンコード。モデルの読み込みは入れない)">GPU の秒数</span>
      <span className="model-table-head num" title="生成したトークン数(思考 + 答え)">生成トークン</span>
    </>
  );
}

// Numbers は、1行の数字の3列
function Numbers({ t }: { t: UsageTotal }) {
  return (
    <>
      <span className="num">{t.count.toLocaleString()}</span>
      <span className="num">{formatSec(t.gpu_sec)}</span>
      <span className="num">{t.gen_tokens.toLocaleString()}</span>
    </>
  );
}

function Periods({ days, onChange }: { days: number; onChange(days: number): void }) {
  return (
    <div className="segmented usage-periods" role="group">
      {PERIODS.map(p => (
        <button key={p.days} type="button" className={days === p.days ? "current" : ""} aria-pressed={days === p.days} onClick={() => onChange(p.days)}>
          {p.label}
        </button>
      ))}
    </div>
  );
}

// personOf は、利用者の行の名前(表示名があれば表示名(名前)。だれでもない生成は、作りの抜けなのでそう出す)
function personOf(t: UsageTotal): string {
  if (t.key === "") return "(だれでもない)";
  return showName(t.name || t.key, t.display_name);
}

export function UsageAdmin({ onError }: { onError(text: string): void }) {
  const [days, setDays] = useState(7);
  const [rows, setRows] = useState<UsageTotal[] | null>(null);
  const [selected, setSelected] = useState<string | null>(null); // 押した人の key(利用者の ID)
  useEffect(() => {
    let live = true;
    admin.usage(days).then(r => live && setRows(r), err => onError(errorMessage(err)));
    return () => { live = false; };
  }, [days]);
  const person = rows?.find(r => r.key === selected) ?? null;
  return (
    <>
      <h2 title="aibow-server の生成1回ごとの記録をまとめたもの。会話の中身は記録しない">使用量</h2>
      <Periods days={days} onChange={setDays} />
      {rows && (
        <div className="usage-table">
          <Head first="利用者" />
          {rows.map(t => (
            <button
              key={t.key}
              type="button"
              className={"model-row user-row" + (t.key === selected ? " current" : "")}
              aria-expanded={t.key === selected}
              title={details(t)}
              onClick={() => setSelected(s => (s === t.key ? null : t.key))}
            >
              <span className="model-name">
                {personOf(t)}
                {t.deleted && <span className="origin-chip" title="消された利用者(記録は残す)">消した人</span>}
              </span>
              <Numbers t={t} />
            </button>
          ))}
        </div>
      )}
      {rows?.length === 0 && <p className="hint">この期間の生成はありません</p>}
      {person && <UsageOf key={person.key + days} person={person} days={days} onError={onError} />}
    </>
  );
}

// UsageOf は、押した人の内訳(モデルごと・日ごと)
function UsageOf({ person, days, onError }: { person: UsageTotal; days: number; onError(text: string): void }) {
  const [d, setD] = useState<UsageDetail | null>(null);
  useEffect(() => {
    admin.usageOf(person.key, days).then(setD, err => onError(errorMessage(err)));
  }, []);
  return (
    <div className="user-detail">
      <div className="user-detail-head">
        <h2 className="section">{personOf(person)}</h2>
      </div>
      {d && (
        <>
          <h3 className="user-sub">モデルごと</h3>
          <div className="usage-table">
            <Head first="モデル" />
            {d.models.map(t => (
              <div key={t.key} className="model-row" title={details(t)}>
                <span className="model-name">{t.key || "(分からない)"}</span>
                <Numbers t={t} />
              </div>
            ))}
          </div>
          <h3 className="user-sub">日ごと</h3>
          <div className="usage-table">
            <Head first="日" />
            {d.days.map(t => (
              <div key={t.key} className="model-row" title={details(t)}>
                <span className="model-name">{t.key}</span>
                <Numbers t={t} />
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

// MyUsage は、自分の今日・7日・30日(アカウントのタブ)
export function MyUsage({ onError }: { onError(text: string): void }) {
  const [u, setU] = useState<Mine | null>(null);
  useEffect(() => {
    myUsage().then(setU, err => onError(errorMessage(err)));
  }, []);
  if (!u) return null;
  return (
    <>
      <h2 className="section" title="aibow-server で、自分が生成に使った分">使用量</h2>
      <div className="usage-table">
        <Head first="期間" />
        {[{ label: "今日", t: u.today }, { label: "7日", t: u.week }, { label: "30日", t: u.month }].map(({ label, t }) => (
          <div key={label} className="model-row" title={details(t)}>
            <span className="model-name">{label}</span>
            <Numbers t={t} />
          </div>
        ))}
      </div>
    </>
  );
}
