// 設定の画面の「サーバー」のタブ(0019)。デスクトップのアプリだけに出す(ブラウザは aibow-server そのものの画面なので)。
// ログインの画面の設定(ログインの前。LoginSettings.tsx)にも同じものを出す(0047 の決定事項 11)。
//
// 今のモード(アプリモード / サーバーモードなら aibow-server に誰としてつないでいるか)を出し、画面から aibow-server につなぐ・戻す。
// ログアウトは、ブラウザと同じく左下の名前のメニューと「アカウント」のタブで行う(その場でログインの画面になる。account.go。0031 の決定事項 44)。
// ここの「アプリモードに戻す」は、つなぐのをやめる操作(起動し直すと切り替わる)。
//
//	つなぎ先を打つ → 接続(CheckServer)── 自己署名の証明書なら、フィンガープリントを見せて人が確かめる(信頼したら、そのまま続ける)
//	              → 保存(ConnectServer)── 起動し直すと切り替わり、ログインが要る aibow-server ならログインの画面になる
//
// ログインはここでしない(名前とパスワードの欄を置かない)。つなぐ(どこのサーバーか)はその PC の人が決めることなのでここで、
// ログイン(誰か)は、ブラウザと同じログインの画面の1つで行う(0047 の決定事項 1)。
//
// 信頼できたつなぎ先は、つながなくても Go が覚える(connection.json の known)。タブを開き直したとき・ログアウトしたあとは、
// そのつなぎ先を入れておくので、「接続」で信頼し直さずに進める(2026-10-29、ユーザーと決めた)。
//
// アプリは自分で起動し直さない(2026-10-26、ユーザーと決め直した)。起動し直すまでは前のモードのまま動くので、
// 変えたら、その場で「起動し直してください」の画面(RestartGate)に移す(modeChanged。0051 の決定事項 3)。その画面の設定から開いたこのタブでは、
// 「起動し直すと切り替わります」を出し続ける(Go の ServerConnection の pending)。
// つなぎ先を settings.json か環境変数に手で書いてあれば、そちらが強いので、ここでは表示だけ(決定事項 5)。
import { useCallback, useEffect, useState } from "react";
import { LuInfo, LuShieldCheck } from "react-icons/lu";
import { ChatService } from "../backend";
import type { ServerConnection } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { Probe } from "../../bindings/github.com/secondarykey/aibow/api/client";
import { errorMessage } from "../errors";
import { modeChanged } from "../modeChange";

export function ServerSettings({ disabled, onError, onChanged }: { disabled: boolean; onError(text: string): void; onChanged?(): void }) {
  const [conn, setConn] = useState<ServerConnection | null>(null);
  const [url, setUrl] = useState("");
  const [probe, setProbe] = useState<Probe | null>(null);
  const [note, setNote] = useState(""); // 「今つないでいます」(何も変えなかったとき)
  const [busy, setBusy] = useState(false);

  // reload は、つなぐ・戻すの操作のあとに今のモードを読み直し、画面全体にも知らせる(モードが変われば「起動し直してください」の画面になる)
  const reload = useCallback(async () => {
    const c = await ChatService.ServerConnection();
    setConn(c);
    onChanged?.();
    modeChanged(c);
  }, [onChanged]);
  useEffect(() => {
    ChatService.ServerConnection().then(c => {
      setConn(c);
      // 画面でつないだときは今のつなぎ先を、つないでいなくても信頼したつなぎ先が残っていればそれを入れておく
      // (信頼だけして閉じた・アプリモードに戻した。「接続」で信頼し直さずに進める)
      if (c.source === "screen") setUrl(c.url);
      else if (!c.source && c.knownUrl) setUrl(c.knownUrl);
    }, err => onError(errorMessage(err)));
  }, [onError]);

  // run は Go を呼ぶ1回。呼んでいる間はボタンを押せなくする
  const run = useCallback(async (f: () => Promise<void>) => {
    setBusy(true);
    setNote("");
    try {
      await f();
    } catch (err) {
      onError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }, [onError]);

  if (!conn) return null;
  const handWritten = conn.source === "settings" || conn.source === "env";
  const off = disabled || busy;

  // save は、確かめて信頼できたつなぎ先を保存する(トークンはもらわない。ログインは起動し直したあとのログインの画面で)
  const save = async (p: Probe) => {
    const fingerprint = p.https && !p.systemTrusted ? p.fingerprint ?? "" : "";
    const r = await ChatService.ConnectServer({ url: p.url, fingerprint });
    setProbe(null);
    if (r.same) setNote(`今この aibow-server(${r.url})につないでいます`);
    await reload();
  };
  // connect は「接続」:確かめ、信頼できればそのまま保存する。自己署名の証明書でまだ信頼していなければ、フィンガープリントを見せて待つ
  const connect = () => run(async () => {
    const p = await ChatService.CheckServer(url.trim(), "");
    if (p.trusted) await save(p);
    else setProbe(p);
  });
  // フィンガープリントを信頼して、もう一度確かめ、そのまま保存する(信頼のボタンが「接続」の続き。0047 の決定事項 4)
  const trust = (fp: string) => run(async () => {
    const p = await ChatService.CheckServer(url.trim(), fp);
    if (p.trusted) await save(p);
    else setProbe(p);
  });
  const disconnect = () => run(async () => {
    await ChatService.DisconnectServer();
    await reload();
  });

  return (
    <>
      <h2 className="section">今のモード</h2>
      {conn.pending && <p className="server-status" role="status">{pendingText(conn)}</p>}
      {note && <p className="server-status" role="status">{note}</p>}
      <CurrentRows conn={conn} />
      {conn.plain && <p className="hint">ログイン(アプリ用のトークン)を暗号化せずに保存しています(この OS では暗号化できません)</p>}
      {handWritten && (
        <p className="hint">
          つなぎ先は {conn.source === "settings" ? "settings.json" : "環境変数"}(<code>{conn.sourcePath}</code>)に手で書いてあるので、画面では変えられません
        </p>
      )}

      {!handWritten && (
        <>
          <h2 className="section">
            {conn.mode === "server" ? "つなぎ先を変える" : "aibow-server につなぐ"}
            <LuInfo
              className="settings-info"
              title="つなぐと、生成と会話の保存を aibow-server に頼みます(手元の会話は移しません)。保存して、aibow を起動し直すと切り替わります。ログインは、起動し直したあとのログインの画面で行います"
              aria-label="aibow-server につなぐ"
            />
          </h2>
          <div className="place-add">
            <input
              type="text"
              value={url}
              placeholder="http://127.0.0.1:8089"
              title="aibow-server が起動のときに出す「画面のアプリのつなぎ先」"
              disabled={off}
              onChange={e => { setUrl(e.target.value); setProbe(null); setNote(""); }}
              onKeyDown={e => { if (e.key === "Enter" && url.trim() && !off) connect(); }}
            />
            <button disabled={off || !url.trim()} onClick={connect}>接続</button>
          </div>
          {probe && !probe.trusted && <Fingerprint fp={probe.fingerprint ?? ""} disabled={off} onTrust={trust} />}

          {(conn.pending === "server" || (conn.pending === "" && conn.source === "screen")) && (
            <>
              <h2 className="section">アプリモードへの切り替え</h2>
              <button
                disabled={off}
                onClick={disconnect}
                title="aibow-server との接続を解除し(アプリ用のトークンも削除する)、アプリモード(このアプリの中で llama.cpp を動かす)に切り替えます。起動し直すと切り替わり、会話は手元の DB のものに戻ります"
              >
                アプリモードに切り替え
              </button>
            </>
          )}
        </>
      )}
    </>
  );
}

// pendingText は、保存したがまだ効いていない(起動し直すと切り替わる)ことのお知らせ。ログインの画面でも同じ文を出す
export function pendingText(conn: ServerConnection): string {
  if (conn.pending === "direct") return "保存しました。aibow を起動し直すと、アプリモードに戻ります";
  const base = `保存しました。aibow を起動し直すと、aibow-server(${conn.pendingUrl})につなぎます`;
  return conn.pendingLogin ? base + "。起動し直したら、ログインの画面でログインしてください" : base;
}

// CurrentRows は今のモードの表(表示だけ)。
function CurrentRows({ conn }: { conn: ServerConnection }) {
  const rows: [string, string][] = [];
  if (conn.mode === "direct") {
    rows.push(["モード", "アプリモード(この PC でモデルを動かす)"]);
  } else {
    rows.push(["モード", "サーバーモード(aibow-server につなぐ)"], ["つなぎ先", conn.url]);
    if (conn.user) {
      const exp = conn.expiresAt ? new Date(conn.expiresAt).toLocaleString() : "";
      rows.push(["名前", conn.user], ["ログインの期限", conn.expired ? `${exp}(切れています)` : exp]);
    }
    if (conn.fingerprint) rows.push(["証明書のフィンガープリント", conn.fingerprint]);
  }
  return (
    <div className="host-settings">
      {rows.map(([k, v]) => (
        <div key={k} className={"host-setting" + (k === "ログインの期限" && conn.expired ? " expired" : "")}>
          <span className="host-key">{k}</span>
          <span className="host-value" title={v}>{v}</span>
        </div>
      ))}
    </div>
  );
}

// Fingerprint は、まだ信頼していない自己署名の証明書のフィンガープリント。aibow-server が出すフィンガープリントと同じかを人が比べてから信頼する
function Fingerprint({ fp, disabled, onTrust }: { fp: string; disabled: boolean; onTrust(fp: string): void }) {
  return (
    <div className="fingerprint-box">
      <span className="secret-label">証明書のフィンガープリント(SHA-256)</span>
      <code className="fingerprint-value">{fp}</code>
      <button
        disabled={disabled}
        onClick={() => onTrust(fp)}
        title="aibow-server が起動のとき(ログイン)と aibow-server cert のときに出すフィンガープリントと同じなら、信頼して保存します。違うなら、ほかのサーバーがなりすましているかもしれないので、信頼しないでください"
      >
        <LuShieldCheck /> この証明書を信頼する
      </button>
    </div>
  );
}
