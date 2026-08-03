import { useEffect, useState } from 'react'
import { Building2, Plus, Check, Ban } from 'lucide-react'
import { api } from '../lib/api.js'
import {
  Page, Card, Button, Badge, Table, THead, TR, TH, TD, Dialog, Field, Input, EmptyState,
} from '../components/ui.jsx'

// Organizations that sync keyless: once a superadmin approves an org, it pulls
// every published public framework from its authenticated session — no token.
const STATUS_TONE = { pending: 'amber', approved: 'green', suspended: 'red' }

export default function AdminOrganizations() {
  const [orgs, setOrgs] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const [creating, setCreating] = useState(false)

  async function load() {
    try { setOrgs(await api.adminOrganizations()) } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  async function act(tenant, fn) {
    setBusy(tenant); setError('')
    try { await fn(tenant); await load() } catch (e) { setError(e.message) } finally { setBusy('') }
  }

  const pending = orgs?.filter((o) => o.status === 'pending').length || 0

  return (
    <Page
      title="Organizations"
      subtitle="Consuming organizations sync keyless: approve one and it pulls every published public framework from its own session, instantly, with no token to issue."
      action={<Button icon={Plus} onClick={() => setCreating(true)}>Add organization</Button>}
    >
      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}
      <Card icon={Building2} title={`Organizations${pending ? ` · ${pending} awaiting approval` : ''}`}>
        {orgs && orgs.length === 0 ? (
          <EmptyState icon={Building2} title="No organizations"
            hint="Add an organization; it starts pending until you approve it." />
        ) : (
          <Table>
            <THead><TR><TH>Name</TH><TH>Status</TH><TH>Approved by</TH><TH>Created</TH><TH></TH></TR></THead>
            <tbody>
              {orgs?.map((o) => (
                <TR key={o.tenantId}>
                  <TD className="text-text">{o.name}</TD>
                  <TD><Badge tone={STATUS_TONE[o.status] || 'grey'}>{o.status}</Badge></TD>
                  <TD className="text-muted">{o.approvedAt ? new Date(o.approvedAt).toLocaleDateString() : '—'}</TD>
                  <TD className="text-muted">{new Date(o.createdAt).toLocaleDateString()}</TD>
                  <TD className="text-right whitespace-nowrap">
                    {o.status !== 'approved' && (
                      <Button size="sm" icon={Check} busy={busy === o.tenantId}
                        onClick={() => act(o.tenantId, api.adminApproveOrganization)}>Approve</Button>
                    )}
                    {o.status === 'approved' && (
                      <Button size="sm" variant="ghost" icon={Ban} busy={busy === o.tenantId}
                        onClick={() => act(o.tenantId, api.adminSuspendOrganization)}>Suspend</Button>
                    )}
                  </TD>
                </TR>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      {creating && <OrgDialog onClose={() => setCreating(false)} onDone={() => { setCreating(false); load() }} />}
    </Page>
  )
}

function OrgDialog({ onClose, onDone }) {
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    setError(''); setBusy(true)
    try { await api.adminRegisterOrganization(name); onClose(); onDone() }
    catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  return (
    <Dialog title="Add organization" onClose={onClose}
      footer={<><Button variant="ghost" onClick={onClose}>Cancel</Button><Button busy={busy} onClick={submit}>Add</Button></>}>
      <Field label="Organization name"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Acme GRC" /></Field>
      <div className="text-[12px] text-muted">It starts pending. Approve it here to grant instant, keyless sync.</div>
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}
