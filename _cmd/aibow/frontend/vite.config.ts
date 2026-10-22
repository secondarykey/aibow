import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), wails("./bindings")],
  build: {
    // ビルド結果は、デスクトップのアプリと aibow-server の両方が埋め込む共有のパッケージ(リポジトリのルートの web/)に出す(0016)。
    // 隣の web/dist/index.html(ビルドしていないときのページ)は消さないよう、その下の app/ に出す。
    // 出力先が frontend の外なので、前のビルド結果を消すには emptyOutDir を書く必要がある
    outDir: "../../../web/dist/app",
    emptyOutDir: true,
  },
});
