'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { certMonitorsApi, type CertMonitor } from '@/lib/api'

const statusClass: Record<string, string> = {
  ok: 'bg-emerald-500/15 border-emerald-500/40 text-emerald-300',
  expired: 'bg-red-500/15 border-red-500/40 text-red-300',
  error: 'bg-amber-500/15 border-amber-500/40 text-amber-300',
}

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

  const load = async () => {
    setLoading(true)
    try { setMonitors(await certMonitorsApi.list()) }
    catch { toast.error('Failed to load (admin only?)') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

  const create = async () => {
    if (!form.url) { toast.error('URL required'); return }
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
    } finally { setCreating(false) }
  }

  const remove = async (m: CertMonitor) => {
    if (!confirm(`Delete ${m.url}?`)) return
    try { await certMonitorsApi.remove(m.id); setMonitors((p) => p.filter((x) => x.id !== m.id)) }
    catch { toast.error('Delete failed') }
  }

  const recheck = async (m: CertMonitor) => {
    setCheckingId(m.id)
    try { await certMonitorsApi.check(m.id); toast.success('Probed'); await load() }
    catch { toast.error('Probe failed') }
    finally { setCheckingId('') }
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-4xl space-y-6">
          <div className="flex items-center justify-between">
            <h1 className="text-2xl font-bold">Certificate monitors</h1>
            <Button onClick={() => setShowCreate((s) => !s)}>{showCreate ? 'Cancel' : 'New monitor'}</Button>
          </div>
          {showCreate && (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="space-y-3 py-4">
                <input
                  type="text"
                  placeholder="https://example.com or example.com:8443"
                  value={form.url}
                  onChange={(e) => setForm({ ...form, url: e.target.value })}
                  className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm font-mono"
                />
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-xs text-slate-400 mb-1">Alert threshold (days)</label>
                    <input
                      type="number"
                      min={1}
                      max={365}
                      value={form.alert_threshold_days}
                      onChange={(e) => setForm({ ...form, alert_threshold_days: parseInt(e.target.value) || 14 })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
                    />
                  </div>
                  <div className="flex items-end">
                    <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={form.internal_allowed}
                        onChange={(e) => setForm({ ...form, internal_allowed: e.target.checked })}
                        className="rounded border-slate-600 bg-slate-800"
                      />
                      Allow internal IP (RFC1918)
                    </label>
                  </div>
                </div>
                <div className="flex justify-end">
                  <Button onClick={create} disabled={creating}>{creating ? 'Adding…' : 'Add'}</Button>
                </div>
              </CardContent>
            </Card>
          )}
          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">Loading…</CardContent></Card>
          ) : monitors.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">No monitors. Add a URL to track its TLS expiry.</CardContent></Card>
          ) : (
            <div className="grid gap-3">
              {monitors.map((m) => {
                const d = daysUntil(m.last_expiry_at)
                return (
                  <Card key={m.id} className="bg-slate-900/60 border-slate-800/50">
                    <CardContent className="py-4 flex items-start justify-between gap-3">
                      <div className="flex-1 min-w-0">
                        <p className="font-mono text-sm text-white truncate">{m.url}</p>
                        <div className="flex items-center gap-2 flex-wrap mt-2 text-xs">
                          {m.last_status && (
                            <span className={`px-2 py-0.5 rounded border ${statusClass[m.last_status] ?? statusClass.error}`}>{m.last_status}</span>
                          )}
                          {d !== null && (
                            <span className="text-slate-400">
                              {d > 0 ? `${d} days left` : `expired ${Math.abs(d)} days ago`}
                            </span>
                          )}
                          <span className="text-slate-500">threshold {m.alert_threshold_days}d</span>
                          {m.internal_allowed && <span className="text-amber-400">internal allowed</span>}
                        </div>
                        {m.last_error && <p className="text-xs text-rose-400 mt-1 break-all">{m.last_error}</p>}
                      </div>
                      <div className="flex flex-col gap-1 shrink-0">
                        <Button size="sm" variant="ghost" disabled={checkingId === m.id} onClick={() => recheck(m)}>
                          {checkingId === m.id ? 'Probing…' : 'Re-check'}
                        </Button>
                        <Button size="sm" variant="outline" className="border-red-500/30 text-red-400 hover:bg-red-500/10" onClick={() => remove(m)}>Delete</Button>
                      </div>
                    </CardContent>
                  </Card>
                )
              })}
            </div>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
