'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Pill, severityTone, Code } from '@/components/ui/status'
import { Sheet, ConfirmDialog } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'
import { alertRulesApi, type AlertRule } from '@/lib/api'

const KNOWN_EVENTS = ['device.offline', 'device.online', 'cpu.high', 'memory.high', 'disk.full', 'security', 'custom']
const SEVERITY_OPTIONS = ['low', 'medium', 'high', 'critical']

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

export default function AlertRulesPage() {
  const [rules, setRules] = useState<AlertRule[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<AlertRule | null>(null)
  const [form, setForm] = useState({
    name: '',
    event_type: 'device.offline',
    severity: 'medium',
    email_recipients: '',
    webhook_url: '',
  })

  const load = async () => {
    setLoading(true)
    try {
      setRules(await alertRulesApi.list())
    } catch {
      toast.error('Failed to load')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const create = async () => {
    if (!form.name) {
      toast.error('Name is required')
      return
    }
    setCreating(true)
    try {
      await alertRulesApi.create(form)
      toast.success('Rule created')
      setForm({ name: '', event_type: 'device.offline', severity: 'medium', email_recipients: '', webhook_url: '' })
      setShowCreate(false)
      await load()
    } catch {
      toast.error('Failed to create')
    } finally {
      setCreating(false)
    }
  }

  const remove = async (r: AlertRule) => {
    try {
      await alertRulesApi.remove(r.id)
      setRules((p) => p.filter((x) => x.id !== r.id))
      setConfirmDelete(null)
    } catch {
      toast.error('Failed to delete')
    }
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Automation"
          title="Alert rules"
          description="Translate events into emails, webhooks, and ticket creation."
          actions={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              New rule
            </Button>
          }
        />

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : rules.length === 0 ? (
          <EmptyState
            title="No alert rules configured."
            hint="Create one to wire alerts to email or a webhook."
            action={
              <Button size="sm" onClick={() => setShowCreate(true)}>
                Create first rule
              </Button>
            }
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {rules.map((r) => (
              <li key={r.id} className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02]">
                <div className="flex-1 min-w-0">
                  <p className="text-[13.5px] font-medium text-white">{r.name}</p>
                  <div className="flex flex-wrap items-center gap-1.5 mt-2">
                    <Code>{r.event_type}</Code>
                    <Pill tone={severityTone(r.severity)}>{r.severity}</Pill>
                    {r.enabled ? <Pill tone="success">enabled</Pill> : <Pill tone="muted">disabled</Pill>}
                  </div>
                  {(r.email_recipients || r.webhook_url) && (
                    <p className="text-[11.5px] text-white/40 mt-2">
                      {r.email_recipients && <>email: {r.email_recipients}</>}
                      {r.email_recipients && r.webhook_url && ' · '}
                      {r.webhook_url && (
                        <>
                          webhook: <span className="font-mono text-white/55">{r.webhook_url}</span>
                        </>
                      )}
                    </p>
                  )}
                </div>
                <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(r)}>
                  Delete
                </Button>
              </li>
            ))}
          </ul>
        )}

        <Sheet
          open={showCreate}
          onClose={() => setShowCreate(false)}
          title="New alert rule"
          width="lg"
          footer={
            <>
              <Button variant="ghost" size="sm" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button size="sm" onClick={create} disabled={creating}>
                {creating ? 'Creating…' : 'Create'}
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <div>
              <label className={labelCls}>Name</label>
              <input
                placeholder="e.g. Critical CPU on prod fleet"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                className={`w-full ${inputCls}`}
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className={labelCls}>Event type</label>
                <select
                  value={form.event_type}
                  onChange={(e) => setForm({ ...form, event_type: e.target.value })}
                  className={`w-full ${inputCls}`}
                >
                  {KNOWN_EVENTS.map((ev) => (
                    <option key={ev} value={ev}>{ev}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className={labelCls}>Severity</label>
                <select
                  value={form.severity}
                  onChange={(e) => setForm({ ...form, severity: e.target.value })}
                  className={`w-full ${inputCls}`}
                >
                  {SEVERITY_OPTIONS.map((s) => (
                    <option key={s} value={s}>{s}</option>
                  ))}
                </select>
              </div>
            </div>
            <div>
              <label className={labelCls}>Email recipients (comma-separated)</label>
              <input
                value={form.email_recipients}
                onChange={(e) => setForm({ ...form, email_recipients: e.target.value })}
                className={`w-full ${inputCls} font-mono`}
                placeholder="ops@example.com,oncall@example.com"
              />
            </div>
            <div>
              <label className={labelCls}>Webhook URL (optional)</label>
              <input
                type="url"
                value={form.webhook_url}
                onChange={(e) => setForm({ ...form, webhook_url: e.target.value })}
                className={`w-full ${inputCls} font-mono`}
                placeholder="https://hooks.example.com/abc"
              />
            </div>
          </div>
        </Sheet>

        <ConfirmDialog
          open={!!confirmDelete}
          onClose={() => setConfirmDelete(null)}
          onConfirm={() => confirmDelete && void remove(confirmDelete)}
          title="Delete alert rule?"
          description={`Removes "${confirmDelete?.name || ''}".`}
          confirmLabel="Delete"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
