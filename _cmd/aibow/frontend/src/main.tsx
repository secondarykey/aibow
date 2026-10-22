import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './style.css'
import { initTheme } from './theme'
import { initBackend } from './backend'

// 最初の描画の前に明るさを決める(Go から設定が届くまで OS の明るさで描くと、ちらつくため)
initTheme()

// Go を呼ぶ係の中身(デスクトップは Wails、ブラウザは fetch と SSE)を読み込んでから描く。
// デスクトップでは、ここで @wailsio/runtime が読み込まれ、ウィンドウのドラッグとリサイズ(Frameless)が効くようになる
initBackend().then(() => {
  ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  )
})
