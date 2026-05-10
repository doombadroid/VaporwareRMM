'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Pill, Code } from '@/components/ui/status'
import { Sheet, ConfirmDialog } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'
import { webhooksApi, type Webhook } from '@/lib/api'

const KNOWN_EVENTS = [
  'device.online',
  'device.offline',
  'alert.created',
  'alert.resolved',
  'ticket.created',
  'ticket.updated',
  'patch.installed',
]

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

export default function WebhooksPage() {
  const [hooks, setHooks] = useState<Webhook[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<Webhook | null>(null)
  const [form, setForm] = useState({ url: '', secret: '', events: '', enabled: true })

  const load = async () => {
    setLoading(true)
    try {
      setHooks(await webhooksApi.list())
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
    if (!form.url || !form.events) {
      toast.error('URL + events required')
      return
    }
    setCreating(true)
    try {
      await webhooksApi.create(form)
      toast.success('Webhook created')
      setForm({ url: '', secret: '', events: '', enabled: true })
      setShowCreate(false)
      await load()
    } catch {
      toast.error('Failed to create')
    } finally {
      setCreating(false)
    }
  }

  const remove = async (h: Webhook) => {
    try {
      await webhooksApi.remove(h.id)
      setHooks((p) => p.filter((x) => x.id !== h.id))
      setConfirmDelete(null)
    } catch {
      toast.error('Failed to delete')
    }
  }

  const toggleEvent = (ev: string) => {
    const list = form.events.split(',').map((s) => s.trim()).filter(Boolean)
    if (list.includes(ev)) {
      setForm({ ...form, events: list.filter((e) => e !== ev).join(',') })
    } else {
      setForm({ ...form, events: [...list, ev].join(',') })
    }
  }

  const formEvents = form.events.split(',').map((s) => s.trim()).filter(Boolean)

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Automation"
          title="Webhooks"
          description="Outbound HTTP notifications for events. Signed with HMAC when a secret is set."
          actions={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              New webhook
            </Button>
          }
        />

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : hooks.length === 0 ? (
          <EmptyState
            title="No webhooks configured."
            hint="Create one to fan out events to Slack, PagerDuty, or your own service."
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {hooks.map((h) => (
              <li key={h.id} className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02]">
                <div className="flex-1 min-w-0">
                  <Code>{h.url}</Code>
                  <div className="flex flex-wrap gap-1 mt-2">
                    {h.events.split(',').map((ev) => ev.trim()).filter(Boolean).map((ev) => (
                      <span
                        key={ev}
                        className="px-2 py-0.5 rounded bg-white/[0.04] border border-white/[0.06] text-[10.5px] text-white/65 font-mono"
                      >
                        {ev}
                      </span>
                    ))}
                  </div>
                  <p className="text-[11px] text-white/35 mt-2">
                    {h.enabled ? <Pill tone="success">enabled</Pill> : <Pill tone="muted">disabled</Pill>}
                    {h.secret && <span className="ml-2">secret set</span>}
                    <span className="ml-2">created {new Date(h.created_at * 1000).toLocaleDateString()}</span>
                  </p>
                </div>
                <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(h)}>
                  Delete
                </Button>
              </li>
            ))}
          </ul>
        )}

        <Sheet
          open={showCreate}
          onClose={() => setShowCreate(false)}
          title="New webhook"
          description="Public HTTPS endpoints only. RFC1918 addresses are rejected."
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
              <label className={labelCls}>URL</label>
              <input
                type="url"
                placeholder="https://hooks.example.com/abc"
                value={form.url}
                onChange={(e) => setForm({ ...form, url: e.target.value })}
                className={`w-full ${inputCls} font-mono`}
              />
            </div>
            <div>
              <label className={labelCls}>HMAC secret (optional)</label>
              <input
                type="password"
                value={form.secret}
                onChange={(e) => setForm({ ...form, secret: e.target.value })}
                className={`w-full ${inputCls} font-mono`}
              />
            </div>
            <div>
              <label className={labelCls}>Events</label>
              <div className="flex flex-wrap gap-1.5 mb-2">
                {KNOWN_EVENTS.map((ev) => {
                  const active = formEvents.includes(ev)
                  return (
                    <button
                      key={ev}
                      type="button"
                      onClick={() => toggleEvent(ev)}
                      className={`px-2 py-1 rounded text-[11px] font-mono transition-colors ${
                        active
                          ? 'bg-white/[0.08] text-white border border-white/[0.12]'
                          : 'bg-white/[0.02] text-white/55 border border-white/[0.05] hover:text-white/85'
                      }`}
                    >
                      {ev}
                    </button>
                  )
                })}
              </div>
              <input
                type="text"
                value={form.events}
                onChange={(e) => setForm({ ...form, events: e.target.value })}
                className={`w-full ${inputCls} font-mono`}
                placeholder="comma-separated event names"
              />
            </div>
            <label className="flex items-center gap-2 text-[12.5px] text-white/85 cursor-pointer">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
                className="rounded bg-white/[0.04] border-white/[0.12]"
              />
              Enabled
            </label>
          </div>
        </Sheet>

        <ConfirmDialog
          open={!!confirmDelete}
          onClose={() => setConfirmDelete(null)}
          onConfirm={() => confirmDelete && void remove(confirmDelete)}
          title="Delete webhook?"
          description="Stops outbound deliveries for this URL."
          confirmLabel="Delete"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
