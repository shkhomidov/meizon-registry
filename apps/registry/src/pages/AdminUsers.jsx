import { useEffect, useState } from 'react'
import { Users, UserPlus, Pencil } from 'lucide-react'
import { api } from '../lib/api.js'
import { RegionMultiSelect } from '../components/RegionSelect.jsx'
import {
  Page, Card, Button, Badge, Table, THead, TR, TH, TD, Dialog, Field, Input, Select, EmptyState,
} from '../components/ui.jsx'

const ROLE_TONE = { superadmin: 'red', moderator: 'blue', auditor: 'green' }

export default function AdminUsers() {
  const [users, setUsers] = useState(null)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState(null)

  async function load() {
    try { setUsers(await api.adminUsers()) } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  return (
    <Page
      title="Users & roles"
      subtitle="Manage identities, assign roles and region scope. Auditors author; moderators review and publish; superadmins govern."
      action={<Button icon={UserPlus} onClick={() => setCreating(true)}>New user</Button>}
    >
      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}
      <Card icon={Users} title="Identities">
        {users && users.length === 0 ? (
          <EmptyState icon={Users} title="No users" />
        ) : (
          <Table>
            <THead><TR><TH>Email</TH><TH>Name</TH><TH>Role</TH><TH>Regions</TH><TH>Status</TH><TH></TH></TR></THead>
            <tbody>
              {users?.map((u) => (
                <TR key={u.id}>
                  <TD className="font-mono text-[12px] text-text">{u.email}</TD>
                  <TD className="text-text">{u.fullName}</TD>
                  <TD>{u.role ? <Badge tone={ROLE_TONE[u.role] || 'grey'}>{u.role}</Badge> : <span className="text-muted">—</span>}</TD>
                  <TD className="text-muted">{u.regions?.join(', ') || '—'}</TD>
                  <TD><Badge tone={u.status === 'active' ? 'green' : 'amber'}>{u.status}</Badge></TD>
                  <TD className="text-right">
                    <button onClick={() => setEditing(u)} className="text-muted hover:text-sage" title="Edit role"><Pencil size={15} /></button>
                  </TD>
                </TR>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      {creating && <UserDialog onClose={() => setCreating(false)} onDone={() => { setCreating(false); load() }} />}
      {editing && <RoleDialog user={editing} onClose={() => setEditing(null)} onDone={() => { setEditing(null); load() }} />}
    </Page>
  )
}

function UserDialog({ onClose, onDone }) {
  const [form, setForm] = useState({ email: '', fullName: '', password: '', role: 'auditor', regions: ['GLOBAL'] })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })

  async function submit() {
    setError(''); setBusy(true)
    try {
      await api.adminCreateUser({ email: form.email, fullName: form.fullName, password: form.password, role: form.role, regions: form.regions })
      onDone()
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  return (
    <Dialog title="New user" onClose={onClose}
      footer={<><Button variant="ghost" onClick={onClose}>Cancel</Button><Button busy={busy} onClick={submit}>Create user</Button></>}>
      <div className="grid grid-cols-2 gap-4">
        <Field label="Email"><Input value={form.email} onChange={set('email')} placeholder="user@example.com" /></Field>
        <Field label="Full name"><Input value={form.fullName} onChange={set('fullName')} /></Field>
      </div>
      <Field label="Temporary password"><Input type="password" value={form.password} onChange={set('password')} /></Field>
      <div className="grid grid-cols-2 gap-4">
        <Field label="Role">
          <Select value={form.role} onChange={set('role')}>
            <option value="auditor">auditor</option>
            <option value="moderator">moderator</option>
            <option value="superadmin">superadmin</option>
          </Select>
        </Field>
        <Field label="Regions" hint="continents, blocs or countries"><RegionMultiSelect value={form.regions} onChange={(v) => setForm({ ...form, regions: v })} /></Field>
      </div>
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}

function RoleDialog({ user, onClose, onDone }) {
  const [role, setRole] = useState(user.role || 'auditor')
  const [regions, setRegions] = useState(user.regions || [])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    setError(''); setBusy(true)
    try {
      await api.adminAssignRole({ email: user.email, role, regions })
      onDone()
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  return (
    <Dialog title={`Edit role — ${user.email}`} onClose={onClose}
      footer={<><Button variant="ghost" onClick={onClose}>Cancel</Button><Button busy={busy} onClick={submit}>Save</Button></>}>
      <div className="grid grid-cols-2 gap-4">
        <Field label="Role">
          <Select value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="auditor">auditor</option>
            <option value="moderator">moderator</option>
            <option value="superadmin">superadmin</option>
          </Select>
        </Field>
        <Field label="Regions"><RegionMultiSelect value={regions} onChange={setRegions} /></Field>
      </div>
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}
