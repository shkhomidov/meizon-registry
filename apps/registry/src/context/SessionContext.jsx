import { createContext, useContext, useEffect, useState, useCallback } from 'react'
import { api, AuthError } from '../lib/api.js'

const SessionContext = createContext(null)

export function SessionProvider({ children }) {
  const [status, setStatus] = useState('loading') // loading | anonymous | ready
  const [viewer, setViewer] = useState(null)

  const refresh = useCallback(async () => {
    try {
      const v = await api.viewer()
      setViewer(v)
      setStatus('ready')
    } catch (e) {
      setViewer(null)
      setStatus('anonymous')
    }
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  const signIn = useCallback(async (email, password) => {
    const v = await api.signIn(email, password)
    setViewer(v)
    setStatus('ready')
  }, [])

  const signOut = useCallback(async () => {
    try {
      await api.signOut()
    } catch (_) {}
    setViewer(null)
    setStatus('anonymous')
  }, [])

  return (
    <SessionContext.Provider value={{ status, viewer, signIn, signOut, refresh }}>
      {children}
    </SessionContext.Provider>
  )
}

export function useSession() {
  return useContext(SessionContext)
}

// Role helpers mirroring the backend policy.
export function canApprove(viewer) {
  return viewer && (viewer.role === 'moderator' || viewer.role === 'superadmin')
}
export function canAuthor(viewer) {
  return viewer && (viewer.role === 'auditor' || viewer.role === 'moderator' || viewer.role === 'superadmin')
}

export { AuthError }
