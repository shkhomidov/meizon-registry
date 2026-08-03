import { Routes, Route, Navigate } from 'react-router-dom'
import { useSession } from './context/SessionContext.jsx'
import { AppShell } from './components/layout.jsx'
import ErrorBoundary from './components/ErrorBoundary.jsx'
import { Loader2 } from 'lucide-react'
import SignIn from './pages/SignIn.jsx'
import Frameworks from './pages/Frameworks.jsx'
import FrameworkDetail from './pages/FrameworkDetail.jsx'
import GenerateFramework from './pages/GenerateFramework.jsx'
import Coverage from './pages/Coverage.jsx'
import Jobs from './pages/Jobs.jsx'
import Audit from './pages/Audit.jsx'
import AdminUsers from './pages/AdminUsers.jsx'
import AdminKeys from './pages/AdminKeys.jsx'
import AdminTokens from './pages/AdminTokens.jsx'
import AdminOrganizations from './pages/AdminOrganizations.jsx'
import AdminSettings from './pages/AdminSettings.jsx'

export default function App() {
  const { status, viewer } = useSession()

  if (status === 'loading') {
    return (
      <div className="h-screen flex items-center justify-center text-muted">
        <Loader2 size={20} className="animate-spin" />
      </div>
    )
  }

  if (status === 'anonymous') {
    return (
      <Routes>
        <Route path="/signin" element={<SignIn />} />
        <Route path="*" element={<Navigate to="/signin" replace />} />
      </Routes>
    )
  }

  const isSuper = viewer?.role === 'superadmin'

  return (
    <AppShell>
      <ErrorBoundary label="This page hit an error">
      <Routes>
        <Route path="/" element={<Frameworks />} />
        <Route path="/frameworks/generate" element={<GenerateFramework />} />
        <Route path="/frameworks/:ref/new-version" element={<GenerateFramework />} />
        <Route path="/frameworks/:ref" element={<FrameworkDetail />} />
        <Route path="/coverage" element={<Coverage />} />
        <Route path="/jobs" element={<Jobs />} />
        {isSuper && <Route path="/audit" element={<Audit />} />}
        {isSuper && <Route path="/admin/users" element={<AdminUsers />} />}
        {isSuper && <Route path="/admin/keys" element={<AdminKeys />} />}
        {isSuper && <Route path="/admin/tokens" element={<AdminTokens />} />}
        {isSuper && <Route path="/admin/organizations" element={<AdminOrganizations />} />}
        {isSuper && <Route path="/admin/settings" element={<AdminSettings />} />}
        <Route path="/signin" element={<Navigate to="/" replace />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      </ErrorBoundary>
    </AppShell>
  )
}
