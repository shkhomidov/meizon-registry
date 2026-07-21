// AppShell: fixed left sidebar (grouped nav + submenu, collapsible) + top bar
// (context chip, theme toggle, sign out) + padded content area.
import { useState } from 'react'
import { NavLink, useLocation, useNavigate } from 'react-router-dom'
import {
  LibraryBig, Users, KeyRound, Ticket, ScrollText, ShieldCheck, GitCompareArrows,
  Settings2, Sun, Moon, PanelLeftClose, PanelLeftOpen, ChevronDown, LogOut, Activity } from 'lucide-react'
import { useSession } from '../context/SessionContext.jsx'
import { useTheme } from '../hooks/useTheme.js'
import { Avatar } from './ui.jsx'
import Logo from './Logo.jsx'

function cx(...p) { return p.filter(Boolean).join(' ') }

export function AppShell({ children }) {
  // Collapsed to an icon-only rail by default; the choice is remembered.
  const [collapsed, setCollapsed] = useState(() => {
    const v = typeof localStorage !== 'undefined' ? localStorage.getItem('sidebar_collapsed') : null
    return v === null ? true : v === 'true'
  })
  const toggle = () => setCollapsed((c) => {
    const next = !c
    try { localStorage.setItem('sidebar_collapsed', String(next)) } catch { /* ignore */ }
    return next
  })
  return (
    <div className="h-screen flex overflow-hidden">
      <Sidebar collapsed={collapsed} onToggle={toggle} />
      <div className="flex-1 flex flex-col min-w-0">
        <Topbar />
        <main className="flex-1 overflow-y-auto px-[30px] py-[26px]">{children}</main>
      </div>
    </div>
  )
}

function Sidebar({ collapsed, onToggle }) {
  const { viewer } = useSession()
  const isSuper = viewer?.role === 'superadmin'

  return (
    <aside className={cx('shrink-0 bg-card border-r border-border flex flex-col transition-[width] duration-200', collapsed ? 'w-16' : 'w-[236px]')}>
      <div className={cx('border-b border-border', collapsed ? 'flex flex-col items-center gap-1.5 py-2.5' : 'flex items-center justify-between h-14 px-3')}>
        <div className="flex items-center gap-2">
          <div className="w-7 h-7 rounded-lg flex items-center justify-center" style={{ background: 'color-mix(in srgb, var(--b-sage) 15%, transparent)' }}>
            <Logo size={17} />
          </div>
          {!collapsed && <span className="font-mono text-[13px] font-medium tracking-tight">meizon<span className="text-muted">/registry</span></span>}
        </div>
        <button onClick={onToggle} className="text-muted hover:text-text p-1 rounded-md hover:bg-surface" title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}>
          {collapsed ? <PanelLeftOpen size={17} /> : <PanelLeftClose size={17} />}
        </button>
      </div>

      <nav className="flex-1 overflow-y-auto py-3 px-2 space-y-4">
        <Group label="Compliance" collapsed={collapsed}>
          <NavRow to="/" end icon={LibraryBig} label="Frameworks" collapsed={collapsed} />
          <NavRow to="/coverage" icon={GitCompareArrows} label="Coverage" collapsed={collapsed} />
          <NavRow to="/jobs" icon={Activity} label="Jobs" collapsed={collapsed} />
        </Group>

        {isSuper && (
          <Group label="Governance" collapsed={collapsed}>
            <SubMenu label="Admin" icon={ShieldCheck} collapsed={collapsed} paths={['/admin']}>
              <NavRow to="/admin/users" icon={Users} label="Users & roles" collapsed={collapsed} child />
              <NavRow to="/admin/keys" icon={KeyRound} label="Signing keys" collapsed={collapsed} child />
              <NavRow to="/admin/tokens" icon={Ticket} label="Distribution tokens" collapsed={collapsed} child />
              <NavRow to="/admin/settings" icon={Settings2} label="Settings" collapsed={collapsed} child />
            </SubMenu>
            <NavRow to="/audit" icon={ScrollText} label="Audit log" collapsed={collapsed} />
          </Group>
        )}
      </nav>

      <div className="border-t border-border p-3 flex items-center gap-2.5">
        <Avatar name={viewer?.email} />
        {!collapsed && (
          <div className="min-w-0">
            <div className="text-[12.5px] font-medium truncate">{viewer?.email}</div>
            <div className="text-[11px] text-muted truncate">{viewer?.role || 'no role'}{viewer?.regions?.length ? ` · ${viewer.regions.join(', ')}` : ''}</div>
          </div>
        )}
      </div>
    </aside>
  )
}

function Group({ label, collapsed, children }) {
  return (
    <div className="space-y-0.5">
      {!collapsed && <div className="font-mono text-[10px] tracking-[0.1em] uppercase text-subtle px-2.5 pb-1.5">{label}</div>}
      {children}
    </div>
  )
}

function NavRow({ to, end, icon: Icon, label, collapsed, child }) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) => cx(
        'flex items-center gap-3 px-2.5 py-2 rounded-md font-medium transition-colors',
        child ? 'text-[12.5px]' : 'text-[13.5px]',
        isActive ? 'bg-surface text-text' : 'text-muted hover:bg-surface hover:text-text',
        collapsed && 'justify-center',
      )}
    >
      {({ isActive }) => (
        <>
          <Icon size={child ? 15 : 17} className={cx('shrink-0', isActive && 'text-sage')} />
          {!collapsed && <span className="truncate">{label}</span>}
        </>
      )}
    </NavLink>
  )
}

function SubMenu({ label, icon: Icon, collapsed, paths, children }) {
  const location = useLocation()
  const hasActiveChild = paths.some((p) => location.pathname.startsWith(p))
  const [open, setOpen] = useState(hasActiveChild)

  if (collapsed) {
    return <div className="space-y-0.5">{children}</div>
  }

  return (
    <div>
      <button
        onClick={() => setOpen((o) => !o)}
        className={cx('w-full flex items-center gap-3 px-2.5 py-2 rounded-md text-[13.5px] font-medium transition-colors',
          hasActiveChild ? 'text-text' : 'text-muted hover:bg-surface hover:text-text')}
      >
        <Icon size={17} className={cx('shrink-0', hasActiveChild && 'text-sage')} />
        <span className="flex-1 text-left">{label}</span>
        <ChevronDown size={15} className={cx('transition-transform', open && 'rotate-180')} />
      </button>
      {open && <div className="ml-[26px] pl-2.5 border-l border-border mt-0.5 space-y-0.5">{children}</div>}
    </div>
  )
}

function Topbar() {
  const { viewer, signOut } = useSession()
  const { theme, toggle } = useTheme()
  const navigate = useNavigate()

  return (
    <header className="h-14 shrink-0 border-b border-border flex items-center justify-between px-6 bg-card">
      <div className="flex items-center gap-2">
        <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface px-2.5 py-1 text-[12px]">
          <Logo size={14} />
          <span className="text-text2">Framework Registry</span>
        </span>
      </div>
      <div className="flex items-center gap-1.5">
        <span className="hidden sm:inline-flex items-center gap-1.5 text-[11px] font-mono uppercase tracking-[0.08em] text-muted mr-1">
          <span className="w-1.5 h-1.5 rounded-full bg-sage animate-pulse2" /> live
        </span>
        <button onClick={toggle} className="text-muted hover:text-text p-2 rounded-md hover:bg-surface" title="Toggle theme">
          {theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
        </button>
        <button onClick={async () => { await signOut(); navigate('/signin') }} className="text-muted hover:text-text p-2 rounded-md hover:bg-surface" title="Sign out">
          <LogOut size={16} />
        </button>
      </div>
    </header>
  )
}
