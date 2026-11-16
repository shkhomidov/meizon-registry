import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { SessionProvider } from './context/SessionContext.jsx'
import App from './App.jsx'
import './index.css'

// The router's basename comes from Vite's own base, so the two cannot
// disagree. import.meta.env.BASE_URL is the value passed to defineConfig at
// build time; React Router wants it without the trailing slash, and an empty
// string for a root deployment rather than '/'.
const basename = import.meta.env.BASE_URL.replace(/\/$/, '')

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <BrowserRouter basename={basename}>
      <SessionProvider>
        <App />
      </SessionProvider>
    </BrowserRouter>
  </React.StrictMode>,
)
