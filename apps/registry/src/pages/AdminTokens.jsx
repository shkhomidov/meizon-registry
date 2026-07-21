import { useEffect, useState } from 'react'
import { Ticket, Plus, Copy } from 'lucide-react'
import { api } from '../lib/api.js'
import { RegionMultiSelect } from '../components/RegionSelect.jsx'
import {
  Page, Card, Button, Badge, Table, THead, TR, TH, TD, Dialog, Field, Input, EmptyState,
} from '../components/ui.jsx'

export default function AdminTokens() {
  const [tokens, setTokens] = useState(null)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)

  async function load() {
    try { setTokens(await api.adminTokens()) } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  return (
    <Page
      title="Distribution tokens"
      subtitle="Per-instance bearer tokens for GRC instances to pull the distribution API. The secret is shown once at issuance; only its digest is stored."
      action={<Button icon={Plus} onClick={() => setCreating(true)}>Issue token</Button>}
    >
      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}
      <Card icon={Ticket} title="Tokens">
        {tokens && tokens.length === 0 ? (
          <EmptyState icon={Ticket} title="No tokens" hint="Issue a token to let a GRC instance sync." />
        ) : (
          <Table>
            <THead><TR><TH>Name</TH><TH>Regions</TH><TH>Status</TH><TH>Created</TH><TH>Last used</TH></TR></THead>
            <tbody>
              {tokens?.map((t) => (
                <TR key={t.id}>
                  <TD className="text-text">{t.name}</TD>
                  <TD className="text-muted">{t.regions?.join(', ') || 'all'}</TD>
                  <TD>{t.revoked ? <Badge tone="red">revoked</Badge> : <Badge tone="green">active</Badge>}</TD>
                  <TD className="text-muted">{new Date(t.createdAt).toLocaleString()}</TD>
                  <TD className="text-muted">{t.lastUsedAt ? new Date(t.lastUsedAt).toLocaleString() : '—'}</TD>
                </TR>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      {creating && <TokenDialog onClose={() => setCreating(false)} onDone={() => { setCreating(false); load() }} />}
    </Page>
  )
}

function TokenDialog({ onClose, onDone }) {
  const [form, setForm] = useState({ name: '', regions: [] })
  const [issued, setIssued] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })

  async function submit() {
    setError(''); setBusy(true)
    try {
      const res = await api.adminIssueToken({ name: form.name, regions: form.regions })
      setIssued(res.token)
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  if (issued) {
    return (
      <Dialog title="Token issued" onClose={() => { onClose(); onDone() }}
        footer={<Button onClick={() => { onClose(); onDone() }}>Done</Button>}>
        <div className="text-[12.5px] text-st-amber">Copy this now — it will not be shown again.</div>
        <div className="flex items-center gap-2 bg-surface border border-border rounded-md px-3 py-2">
          <code className="font-mono text-[12px] text-text break-all flex-1">{issued}</code>
          <button onClick={() => navigator.clipboard?.writeText(issued)} className="text-muted hover:text-sage shrink-0"><Copy size={15} /></button>
        </div>
      </Dialog>
    )
  }

  return (
    <Dialog title="Issue distribution token" onClose={onClose}
      footer={<><Button variant="ghost" onClick={onClose}>Cancel</Button><Button busy={busy} onClick={submit}>Issue</Button></>}>
      <Field label="Instance name"><Input value={form.name} onChange={set('name')} placeholder="grc-eu-prod" /></Field>
      <Field label="Region scope" hint="leave empty for all regions"><RegionMultiSelect value={form.regions} onChange={(v) => setForm({ ...form, regions: v })} /></Field>
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}
