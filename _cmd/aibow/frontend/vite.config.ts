import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// aibow-server を開発で動かすとき(go run ./_cmd/dev server。0021)は、スクリプトが AIBOW_BROWSER_DEV に aibow-server のアドレスを入れて vite を起動する。
// そのときだけ、この開発用サーバーがブラウザの画面を配る:
//   - /aibow/ の呼び出し(画面用の口・ログイン・SSE・管理者の口)を aibow-server に渡す(proxy)
//   - index.html に、ブラウザの印を足す(本番では aibow-server が足すもの。_cmd/aibow-server の browser.go の browserMarker と同じ)
// aibow-server には開発のためのコードを入れない(決定事項 5)。wails3 dev の画面(AIBOW_BROWSER_DEV が無い)には効かない。
const browserDev = process.env.AIBOW_BROWSER_DEV;

function browserMarker(): Plugin {
  return {
    name: "aibow-browser-marker",
    transformIndexHtml: html => html.replace("<head>", '<head>\n    <meta name="aibow-browser" content="1">'),
  };
}

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
    // 自己署名の証明書の aibow-server にも渡せるよう、証明書は確かめない(開発の PC の中だけで使うため)
    proxy: browserDev ? { "/aibow": { target: browserDev, secure: false } } : undefined,
  },
  plugins: [react(), wails("./bindings"), ...(browserDev ? [browserMarker()] : [])],
  build: {
    // ビルド結果は、デスクトップのアプリと aibow-server の両方が埋め込む共有のパッケージ(リポジトリのルートの web/)に出す(0016)。
    // 隣の web/dist/index.html(ビルドしていないときのページ)は消さないよう、その下の app/ に出す。
    // 出力先が frontend の外なので、前のビルド結果を消すには emptyOutDir を書く必要がある
    outDir: "../../../web/dist/app",
    emptyOutDir: true,
  },
});
