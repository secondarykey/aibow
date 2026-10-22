import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './style.css'
import { initTheme } from './theme'
import { initBackend, session } from './backend'
import { LoginGate } from './components/Login'

// 最初の描画の前に明るさを決める(Go から設定が届くまで OS の明るさで描くと、ちらつくため)
initTheme()

// Go を呼ぶ係の中身(デスクトップは Wails、ブラウザは fetch と SSE)を読み込んでから描く。
// デスクトップでは、ここで @wailsio/runtime が読み込まれ、ウィンドウのドラッグとリサイズ(Frameless)が効くようになる。
// ブラウザでは、描く前にログインが要るかを聞く(要るのにしていなければ、アプリの代わりにログインの画面を描く。0017 の 10-1)。
// 聞けなかったとき(aibow-server が止まっている)は、ログインが要らない形として描き、呼び出しのエラーで伝える
initBackend().then(async () => {
  await session().catch(() => undefined)
  ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
    <React.StrictMode>
      <LoginGate>
        <App />
      </LoginGate>
    </React.StrictMode>,
  )
})
