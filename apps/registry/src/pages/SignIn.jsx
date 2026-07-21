import { useState } from 'react'
import Logo from '../components/Logo.jsx'
import { useSession } from '../context/SessionContext.jsx'
import { Button, Field, Input } from '../components/ui.jsx'

export default function SignIn() {
  const { signIn } = useSession()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await signIn(email, password)
    } catch (err) {
      setError(err.message || 'sign-in failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center px-4 bg-bg">
      <div className="w-full max-w-sm">
        <div className="flex items-center gap-2 justify-center mb-6">
          <div className="w-9 h-9 rounded-xl flex items-center justify-center" style={{ background: 'color-mix(in srgb, var(--b-sage) 15%, transparent)' }}>
            <Logo size={22} />
          </div>
          <span className="font-mono text-[15px] font-medium tracking-tight">meizon<span className="text-muted">/registry</span></span>
        </div>
        <div className="bg-card border border-border rounded-card p-6 animate-fade">
          <h1 className="text-[17px] font-medium mb-1">Sign in</h1>
          <p className="text-[12.5px] text-muted mb-5">Authoring console for the framework registry.</p>
          <form onSubmit={submit} className="flex flex-col gap-4">
            <Field label="Email"><Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoFocus placeholder="you@example.com" /></Field>
            <Field label="Password"><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="••••••••" /></Field>
            {error && <div className="text-[12.5px] text-st-red">{error}</div>}
            <Button type="submit" busy={busy} className="w-full">Sign in</Button>
          </form>
        </div>
      </div>
    </div>
  )
}
