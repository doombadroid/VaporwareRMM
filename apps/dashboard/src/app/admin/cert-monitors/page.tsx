'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Pill, statusTone, Code } from '@/components/ui/status'
import { Sheet, ConfirmDialog } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'
import { certMonitorsApi, type CertMonitor } from '@/lib/api'

function daysUntil(unixSec?: number): number | null {
  if (!unixSec) return null
  return Math.floor((unixSec - Date.now() / 1000) / 86400)
}

export default function CertMonitorsPage() {
  const [monitors, setMonitors] = useState<CertMonitor[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ url: '', alert_threshold_days: 14, internal_allowed: false })
  const [checkingId, setCheckingId] = useState('')
  const [confirmDelete, setConfirmDelete] = useState<CertMonitor | null>(null)

  const load = async () => {
    setLoading(true)
    try {
      setMonitors(await certMonitorsApi.list())
    } catch {
      toast.error('Failed to load (admin only?)')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const create = async () => {
    if (!form.url) {
      toast.error('URL required')
      return
    }
    setCreating(true)
    try {
      await certMonitorsApi.create(form)
      toast.success('Monitor added')
      setForm({ url: '', alert_threshold_days: 14, internal_allowed: false })
      setShowCreate(false)
      await load()
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || 'Create failed'
      toast.error(msg)
    } finally {
      setCreating(false)
    }
  }

  const remove = async (m: CertMonitor) => {
    try {
      await certMonitorsApi.remove(m.id)
      setMonitors((p) => p.filter((x) => x.id !== m.id))
      setConfirmDelete(null)
    } catch {
      toast.error('Delete failed')
    }
  }

  const recheck = async (m: CertMonitor) => {
    setCheckingId(m.id)
    try {
      await certMonitorsApi.check(m.id)
      toast.success('Probed')
      await load()
    } catch {
      toast.error('Probe failed')
    } finally {
      setCheckingId('')
    }
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Network"
          title="Certificate monitors"
          description="Track TLS expiry on URLs the fleet depends on."
          actions={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              New monitor
            </Button>
          }
        />

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : monitors.length === 0 ? (
          <EmptyState
            title="No monitors yet."
            hint="Add a URL to track its TLS expiry."
            action={
              <Button size="sm" onClick={() => setShowCreate(true)}>
                Add first monitor
              </Button>
            }
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {monitors.map((m) => {
              const d = daysUntil(m.last_expiry_at)
              return (
                <li key={m.id} className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02]">
                  <div className="flex-1 min-w-0">
                    <Code className="text-white/95">{m.url}</Code>
                    <div className="flex items-center gap-2 flex-wrap mt-2 text-[11.5px]">
                      {m.last_status && <Pill tone={statusTone(m.last_status)}>{m.last_status}</Pill>}
                      {d !== null && (
                        <span className={d <= 14 ? 'text-amber-300' : 'text-white/55'}>
                          {d > 0 ? `${d} days left` : `expired ${Math.abs(d)} days ago`}
                        </span>
                      )}
                      <span className="text-white/30">·</span>
                      <span className="text-white/40">threshold {m.alert_threshold_days}d</span>
                      {m.internal_allowed && (
                        <>
                          <span className="text-white/30">·</span>
                          <span className="text-amber-300/80">internal allowed</span>
                        </>
                      )}
                    </div>
                    {m.last_error && <p className="text-[11.5px] text-rose-300/85 mt-1.5 break-all">{m.last_error}</p>}
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={checkingId === m.id}
                      onClick={() => recheck(m)}
                    >
                      {checkingId === m.id ? 'Probing…' : 'Re-check'}
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(m)}>
                      Delete
                    </Button>
                  </div>
                </li>
              )
            })}
          </ul>
        )}

        <Sheet
          open={showCreate}
          onClose={() => setShowCreate(false)}
          title="New certificate monitor"
          description="vaporRMM probes daily and alerts before expiry."
          footer={
            <>
              <Button variant="ghost" size="sm" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button size="sm" onClick={create} disabled={creating}>
                {creating ? 'Adding…' : 'Add monitor'}
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <div>
              <label className="block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5">URL</label>
              <input
                type="text"
                placeholder="https://example.com or example.com:8443"
                value={form.url}
                onChange={(e) => setForm({ ...form, url: e.target.value })}
                className="w-full bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] font-mono text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]"
              />
            </div>
            <div>
              <label className="block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5">
                Alert threshold (days)
              </label>
              <input
                type="number"
                min={1}
                max={365}
                value={form.alert_threshold_days}
                onChange={(e) => setForm({ ...form, alert_threshold_days: parseInt(e.target.value) || 14 })}
                className="w-full bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white focus:outline-none focus:border-white/[0.2]"
              />
            </div>
            <label className="flex items-center gap-2 text-[12.5px] text-white/85 cursor-pointer">
              <input
                type="checkbox"
                checked={form.internal_allowed}
                onChange={(e) => setForm({ ...form, internal_allowed: e.target.checked })}
                className="rounded bg-white/[0.04] border-white/[0.12]"
              />
              Allow internal IP (RFC1918)
            </label>
          </div>
        </Sheet>

        <ConfirmDialog
          open={!!confirmDelete}
          onClose={() => setConfirmDelete(null)}
          onConfirm={() => confirmDelete && void remove(confirmDelete)}
          title="Delete monitor?"
          description={`This stops probing ${confirmDelete?.url || ''}. The action is reversible — you can re-add it.`}
          confirmLabel="Delete"
          tone="danger"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
