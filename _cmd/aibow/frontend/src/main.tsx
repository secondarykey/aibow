// ウィンドウのドラッグとリサイズ(Frameless)は @wailsio/runtime がページ全体のマウス操作を見て行う。
// 確実に読み込まれるよう、入口で Go を呼ぶ係(@wailsio/runtime を読み込む)を import しておく
import './backend'
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './style.css'
import { initTheme } from './theme'

// 最初の描画の前に明るさを決める(Go から設定が届くまで OS の明るさで描くと、ちらつくため)
initTheme()

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
