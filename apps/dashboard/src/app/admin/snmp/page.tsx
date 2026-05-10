'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Sheet, ConfirmDialog } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Plus, AlertTriangle } from 'lucide-react'
import { snmpApi, type SNMPTarget } from '@/lib/api'

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

export default function SNMPPage() {
  const [targets, setTargets] = useState<SNMPTarget[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<SNMPTarget | null>(null)
  const [form, setForm] = useState({
    name: '',
    host: '',
    port: 161,
    v3_username: '',
    v3_auth_protocol: 'SHA256' as 'SHA' | 'SHA256' | 'SHA512',
    v3_auth_pass: '',
    v3_priv_protocol: 'AES256' as 'AES' | 'AES256',
    v3_priv_pass: '',
    oids: '1.3.6.1.2.1.1.1.0',
    poll_interval_seconds: 300,
  })

  const load = async () => {
    setLoading(true)
    try {
      setTargets(await snmpApi.list())
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
    if (!form.name || !form.host || !form.v3_username || !form.v3_auth_pass || !form.v3_priv_pass) {
      toast.error('Fill name/host/v3 credentials')
      return
    }
    setCreating(true)
    try {
      await snmpApi.create({
        ...form,
        oids: form.oids.split(',').map((s) => s.trim()).filter(Boolean),
      })
      toast.success('Target added')
      setShowCreate(false)
      await load()
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || 'Create failed'
      toast.error(msg)
    } finally {
      setCreating(false)
    }
  }

  const remove = async (t: SNMPTarget) => {
    try {
      await snmpApi.remove(t.id)
      setTargets((p) => p.filter((x) => x.id !== t.id))
      setConfirmDelete(null)
    } catch {
      toast.error('Delete failed')
    }
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Network"
          title="SNMPv3 targets"
          description="authPriv level only. v1/v2c communities are not supported on principle."
          actions={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              New target
            </Button>
          }
        />

        <div className="mb-5 flex items-start gap-2 text-[12px] text-amber-200/85 bg-amber-500/[0.04] border border-amber-500/15 rounded-lg px-3 py-2">
          <AlertTriangle className="w-3.5 h-3.5 mt-0.5 text-amber-400 shrink-0" />
          <span>
            Configuration UI only. Polling worker not yet wired — operators can declare targets but no SNMP traffic flows
            yet.
          </span>
        </div>

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : targets.length === 0 ? (
          <EmptyState title="No SNMP targets configured." />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {targets.map((t) => (
              <li key={t.id} className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02]">
                <div className="flex-1 min-w-0">
                  <p className="text-[13.5px] font-medium text-white">{t.name}</p>
                  <p className="text-[11.5px] text-white/55 mt-1 font-mono">
                    {t.host}:{t.port} · user {t.v3_username}
                  </p>
                  <p className="text-[11px] text-white/35 mt-0.5">
                    auth {t.v3_auth_protocol} · priv {t.v3_priv_protocol} · poll {t.poll_interval_seconds}s
                  </p>
                  <p className="text-[11px] text-white/35 mt-1 font-mono break-all">{t.oids}</p>
                  {t.last_error && <p className="text-[11.5px] text-rose-300/85 mt-1">{t.last_error}</p>}
                </div>
                <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(t)}>
                  Delete
                </Button>
              </li>
            ))}
          </ul>
        )}

        <Sheet
          open={showCreate}
          onClose={() => setShowCreate(false)}
          title="New SNMPv3 target"
          width="lg"
          footer={
            <>
              <Button variant="ghost" size="sm" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button size="sm" onClick={create} disabled={creating}>
                {creating ? 'Adding…' : 'Add target'}
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className={labelCls}>Name</label>
                <input className={`w-full ${inputCls}`} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
              </div>
              <div>
                <label className={labelCls}>Host / IP</label>
                <input className={`w-full ${inputCls} font-mono`} value={form.host} onChange={(e) => setForm({ ...form, host: e.target.value })} />
              </div>
              <div>
                <label className={labelCls}>Port</label>
                <input type="number" className={`w-full ${inputCls}`} min={1} max={65535} value={form.port} onChange={(e) => setForm({ ...form, port: parseInt(e.target.value) || 161 })} />
              </div>
              <div>
                <label className={labelCls}>Poll interval (sec)</label>
                <input type="number" className={`w-full ${inputCls}`} min={30} value={form.poll_interval_seconds} onChange={(e) => setForm({ ...form, poll_interval_seconds: parseInt(e.target.value) || 300 })} />
              </div>
            </div>

            <div>
              <label className={labelCls}>v3 Username</label>
              <input className={`w-full ${inputCls}`} value={form.v3_username} onChange={(e) => setForm({ ...form, v3_username: e.target.value })} />
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className={labelCls}>Auth protocol</label>
                <select
                  value={form.v3_auth_protocol}
                  onChange={(e) => setForm({ ...form, v3_auth_protocol: e.target.value as 'SHA' | 'SHA256' | 'SHA512' })}
                  className={`w-full ${inputCls}`}
                >
                  <option value="SHA">SHA</option>
                  <option value="SHA256">SHA256</option>
                  <option value="SHA512">SHA512</option>
                </select>
              </div>
              <div>
                <label className={labelCls}>Auth password</label>
                <input type="password" className={`w-full ${inputCls}`} value={form.v3_auth_pass} onChange={(e) => setForm({ ...form, v3_auth_pass: e.target.value })} />
              </div>
              <div>
                <label className={labelCls}>Priv protocol</label>
                <select
                  value={form.v3_priv_protocol}
                  onChange={(e) => setForm({ ...form, v3_priv_protocol: e.target.value as 'AES' | 'AES256' })}
                  className={`w-full ${inputCls}`}
                >
                  <option value="AES">AES</option>
                  <option value="AES256">AES256</option>
                </select>
              </div>
              <div>
                <label className={labelCls}>Priv password</label>
                <input type="password" className={`w-full ${inputCls}`} value={form.v3_priv_pass} onChange={(e) => setForm({ ...form, v3_priv_pass: e.target.value })} />
              </div>
            </div>

            <div>
              <label className={labelCls}>OIDs (comma-separated)</label>
              <textarea
                rows={2}
                className={`w-full ${inputCls} font-mono`}
                value={form.oids}
                onChange={(e) => setForm({ ...form, oids: e.target.value })}
              />
            </div>
          </div>
        </Sheet>

        <ConfirmDialog
          open={!!confirmDelete}
          onClose={() => setConfirmDelete(null)}
          onConfirm={() => confirmDelete && void remove(confirmDelete)}
          title="Delete target?"
          description={`Stops collection from ${confirmDelete?.name || ''}.`}
          confirmLabel="Delete"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
