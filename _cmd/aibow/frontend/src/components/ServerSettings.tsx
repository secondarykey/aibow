// 設定の画面の「サーバー」のタブ(0019)。デスクトップのアプリだけに出す(ブラウザは aibow-server そのものの画面なので)。
//
// 今の形(アプリの中で動かす / aibow-server に誰としてつないでいるか)を出し、画面から aibow-server につなぐ・戻す。
//
//	つなぎ先を打つ → 確かめる(CheckServer)── 自己署名の証明書なら、指紋を見せて人が確かめる(信頼したら、もう一度確かめる)
//	              → ログインが要れば名前とパスワード → つなぐ(ConnectServer)── 保存して、起動し直すと切り替わる
//
// 信頼できたつなぎ先は、つながなくても Go が覚える(connection.json の known)。タブを開き直したとき・ログアウトしたあとは、
// そのつなぎ先と名前を入れておくので、「確かめる」で信頼し直さずにログインへ進める(2026-10-29、ユーザーと決めた)。
//
// アプリは自分で起動し直さない(2026-10-26、ユーザーと決め直した)。起動し直すまでは前の形のまま動くので、
// 「起動し直すと切り替わります」を、起動し直すまで出し続ける(Go の ServerConnection の pending。タブを開き直しても出る)。
//
// パスワードはアプリ用のトークンをもらうのに使うだけで、Go も画面も覚えない(0019 の決定事項 2)。
// つなぎ先を settings.json か環境変数に手で書いてあれば、そちらが強いので、ここでは表示だけ(決定事項 5)。
import { useCallback, useEffect, useState } from "react";
import { LuInfo, LuShieldCheck } from "react-icons/lu";
import { ChatService } from "../backend";
import type { ServerConnection } from "../../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { Probe } from "../../bindings/github.com/secondarykey/aibow/api/client";
import { errorMessage } from "../errors";

export function ServerSettings({ disabled, onError }: { disabled: boolean; onError(text: string): void }) {
  const [conn, setConn] = useState<ServerConnection | null>(null);
  const [url, setUrl] = useState("");
  const [probe, setProbe] = useState<Probe | null>(null);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);

  const reload = useCallback(() => ChatService.ServerConnection().then(setConn, err => onError(errorMessage(err))), [onError]);
  useEffect(() => {
    ChatService.ServerConnection().then(c => {
      setConn(c);
      // 画面でつないだときは、つなぎ直しやすいよう今の値を入れておく(期限が切れたときにログインし直すため)。
      // つないでいなくても、信頼したつなぎ先が残っていれば入れておく(信頼だけして閉じた・ログアウトした。「確かめる」で信頼し直さずにログインへ進める)
      if (c.source === "screen") {
        setUrl(c.url);
        setName(c.user);
      } else if (!c.source && c.knownUrl) {
        setUrl(c.knownUrl);
        setName(c.knownUser);
      }
    }, err => onError(errorMessage(err)));
  }, [onError]);

  // run は Go を呼ぶ1回。呼んでいる間はボタンを押せなくする
  const run = useCallback(async (f: () => Promise<void>) => {
    setBusy(true);
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

  const check = () => run(async () => setProbe(await ChatService.CheckServer(url.trim(), "")));
  // 指紋を信頼して、もう一度確かめる(ここで初めて aibow-server にリクエストを送る。ログインが要るかが分かる)
  const trust = (fp: string) => run(async () => setProbe(await ChatService.CheckServer(url.trim(), fp)));
  const connect = (p: Probe) => run(async () => {
    const fingerprint = p.https && !p.systemTrusted ? p.fingerprint ?? "" : "";
    await ChatService.ConnectServer({ url: p.url, fingerprint, name: p.login ? name : "", password: p.login ? password : "" });
    setPassword("");
    setProbe(null);
    await reload();
  });
  const disconnect = () => run(async () => {
    await ChatService.DisconnectServer();
    await reload();
  });

  return (
    <>
      <h2 className="section">今の形</h2>
      {conn.pending && (
        <p className="server-status" role="status">
          {conn.pending === "server"
            ? `保存しました。aibow を起動し直すと、aibow-server(${conn.pendingUrl})につなぎます`
            : "保存しました。aibow を起動し直すと、アプリの中で動かす形に戻ります"}
        </p>
      )}
      <CurrentRows conn={conn} />
      {conn.plain && <p className="hint">アプリ用のトークンを包まずに保存しています(この OS では包めません)</p>}
      {handWritten && (
        <p className="hint">
          つなぎ先は {conn.source === "settings" ? "settings.json" : "環境変数"}(<code>{conn.sourcePath}</code>)に手で書いてあるので、画面では変えられません
        </p>
      )}

      {!handWritten && (
        <>
          <h2 className="section">
            {conn.mode === "server" ? "つなぎ直す" : "aibow-server につなぐ"}
            <LuInfo
              className="settings-info"
              title="つなぐと、生成と会話の保存を aibow-server に頼みます(手元の会話は移しません)。つなぎ先を変えると、aibow が起動し直します"
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
              onChange={e => { setUrl(e.target.value); setProbe(null); }}
              onKeyDown={e => { if (e.key === "Enter" && url.trim() && !off) check(); }}
            />
            <button disabled={off || !url.trim()} onClick={check}>確かめる</button>
          </div>
          {probe && !probe.trusted && <Fingerprint fp={probe.fingerprint ?? ""} disabled={off} onTrust={trust} />}
          {probe?.trusted && (
            <form className="server-login" onSubmit={e => { e.preventDefault(); connect(probe); }}>
              {probe.https && !probe.systemTrusted && (
                <p className="hint"><LuShieldCheck aria-hidden="true" /> 証明書の指紋を信頼しました</p>
              )}
              {probe.login && (
                <>
                  <label htmlFor="server-name">名前</label>
                  <input id="server-name" type="text" autoComplete="username" value={name} disabled={off} onChange={e => setName(e.target.value)} />
                  <label htmlFor="server-password" title="アプリ用のトークン(10 日で切れる)をもらうのに使うだけで、覚えません">パスワード</label>
                  <input id="server-password" type="password" autoComplete="current-password" value={password} disabled={off} onChange={e => setPassword(e.target.value)} />
                </>
              )}
              <button type="submit" className="primary apply" disabled={off || (probe.login && (!name.trim() || !password))}>
                {probe.login ? "ログインしてつなぐ" : "つなぐ"}
              </button>
            </form>
          )}

          {(conn.pending === "server" || (conn.pending === "" && conn.source === "screen")) && (
            <>
              <h2 className="section">アプリの中で動かす</h2>
              <button
                disabled={off}
                onClick={disconnect}
                title="aibow-server からログアウトし(アプリ用のトークンを消す)、このアプリの中で llama.cpp を動かす形に戻します。会話は手元の DB のものに戻ります"
              >
                ログアウトして戻す
              </button>
            </>
          )}
        </>
      )}
    </>
  );
}

// CurrentRows は今の形の表(表示だけ)。
function CurrentRows({ conn }: { conn: ServerConnection }) {
  const rows: [string, string][] = [];
  if (conn.mode === "direct") {
    rows.push(["形", "アプリの中で動かす"]);
  } else {
    rows.push(["形", "aibow-server につなぐ"], ["つなぎ先", conn.url]);
    if (conn.user) {
      const exp = conn.expiresAt ? new Date(conn.expiresAt).toLocaleString() : "";
      rows.push(["名前", conn.user], ["ログインの期限", conn.expired ? `${exp}(切れています)` : exp]);
    }
    if (conn.fingerprint) rows.push(["証明書の指紋", conn.fingerprint]);
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

// Fingerprint は、まだ信頼していない自己署名の証明書の指紋。aibow-server が出す指紋と同じかを人が比べてから信頼する
function Fingerprint({ fp, disabled, onTrust }: { fp: string; disabled: boolean; onTrust(fp: string): void }) {
  return (
    <div className="fingerprint-box">
      <span className="secret-label">証明書の指紋(SHA-256)</span>
      <code className="fingerprint-value">{fp}</code>
      <button
        disabled={disabled}
        onClick={() => onTrust(fp)}
        title="aibow-server が起動のとき(ログイン)と aibow-server cert のときに出す指紋と同じなら、信頼してつなぎます。違うなら、ほかのサーバーがなりすましているかもしれないので、信頼しないでください"
      >
        <LuShieldCheck /> この証明書を信頼する
      </button>
    </div>
  );
}
