import { useEffect, useState } from 'react'
import { ScrollText } from 'lucide-react'
import { api } from '../lib/api.js'
import { Page, Card, Badge, Table, THead, TR, TH, TD, EmptyState } from '../components/ui.jsx'

const ACTION_TONE = (a) =>
  a.includes('publish') ? 'green' :
  a.includes('approve') ? 'blue' :
  a.includes('reject') ? 'red' :
  a.includes('key') || a.includes('token') || a.includes('role') || a.includes('superadmin') ? 'amber' : 'grey'

export default function Audit() {
  const [entries, setEntries] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api.audit().then(setEntries).catch((e) => setError(e.message))
  }, [])

  return (
    <Page title="Audit log" subtitle="Immutable record of governance actions: role changes, approvals, publishes, key generation, token issuance and downloads.">
      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}
      <Card icon={ScrollText} title="Recent activity">
        {entries && entries.length === 0 ? (
          <EmptyState icon={ScrollText} title="No activity yet" />
        ) : (
          <Table>
            <THead><TR><TH>When</TH><TH>Action</TH><TH>Detail</TH></TR></THead>
            <tbody>
              {entries?.map((e, i) => (
                <TR key={i}>
                  <TD className="text-muted whitespace-nowrap">{new Date(e.at).toLocaleString()}</TD>
                  <TD><Badge tone={ACTION_TONE(e.action)}>{e.action}</Badge></TD>
                  <TD className="text-text2 font-mono text-[11.5px]">{e.detail || '—'}</TD>
                </TR>
              ))}
            </tbody>
          </Table>
        )}
      </Card>
    </Page>
  )
}
