'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { snmpApi, type SNMPTarget } from '@/lib/api'

export default function SNMPPage() {
  const [targets, setTargets] = useState<SNMPTarget[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
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
    try { setTargets(await snmpApi.list()) }
    catch { toast.error('Failed to load (admin only?)') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

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
    } finally { setCreating(false) }
  }

  const remove = async (t: SNMPTarget) => {
    if (!confirm(`Delete ${t.name}?`)) return
    try { await snmpApi.remove(t.id); setTargets((p) => p.filter((x) => x.id !== t.id)) }
    catch { toast.error('Delete failed') }
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-4xl space-y-6">
          <div className="flex items-center justify-between">
            <h1 className="text-2xl font-bold">SNMPv3 targets</h1>
            <Button onClick={() => setShowCreate((s) => !s)}>{showCreate ? 'Cancel' : 'New target'}</Button>
          </div>
          <Card className="bg-amber-500/5 border border-amber-500/20">
            <CardContent className="py-3 text-xs text-amber-300">
              Configuration UI only. Polling worker not yet wired — operators can declare targets but no SNMP traffic flows until follow-up stage. v3 auth+priv (authPriv level) only.
            </CardContent>
          </Card>
          {showCreate && (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="space-y-3 py-4">
                <div className="grid grid-cols-2 gap-3">
                  <input type="text" placeholder="name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" />
                  <input type="text" placeholder="host or IP" value={form.host} onChange={(e) => setForm({ ...form, host: e.target.value })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm font-mono" />
                  <input type="number" min={1} max={65535} value={form.port} onChange={(e) => setForm({ ...form, port: parseInt(e.target.value) || 161 })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" placeholder="port" />
                  <input type="number" min={30} value={form.poll_interval_seconds} onChange={(e) => setForm({ ...form, poll_interval_seconds: parseInt(e.target.value) || 300 })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" placeholder="poll seconds" />
                </div>
                <input type="text" placeholder="v3 username" value={form.v3_username} onChange={(e) => setForm({ ...form, v3_username: e.target.value })} className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" />
                <div className="grid grid-cols-2 gap-3">
                  <select value={form.v3_auth_protocol} onChange={(e) => setForm({ ...form, v3_auth_protocol: e.target.value as 'SHA' | 'SHA256' | 'SHA512' })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm">
                    <option value="SHA">SHA</option><option value="SHA256">SHA256</option><option value="SHA512">SHA512</option>
                  </select>
                  <input type="password" placeholder="auth password" value={form.v3_auth_pass} onChange={(e) => setForm({ ...form, v3_auth_pass: e.target.value })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" />
                  <select value={form.v3_priv_protocol} onChange={(e) => setForm({ ...form, v3_priv_protocol: e.target.value as 'AES' | 'AES256' })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm">
                    <option value="AES">AES</option><option value="AES256">AES256</option>
                  </select>
                  <input type="password" placeholder="privacy password" value={form.v3_priv_pass} onChange={(e) => setForm({ ...form, v3_priv_pass: e.target.value })} className="bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm" />
                </div>
                <textarea placeholder="OIDs (comma-separated, dotted-decimal)" rows={2} value={form.oids} onChange={(e) => setForm({ ...form, oids: e.target.value })} className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm font-mono" />
                <div className="flex justify-end">
                  <Button onClick={create} disabled={creating}>{creating ? 'Adding…' : 'Add'}</Button>
                </div>
              </CardContent>
            </Card>
          )}
          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">Loading…</CardContent></Card>
          ) : targets.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">No SNMP targets configured.</CardContent></Card>
          ) : (
            <div className="grid gap-3">
              {targets.map((t) => (
                <Card key={t.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardContent className="py-4 flex items-start justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <p className="font-medium text-white">{t.name}</p>
                      <p className="text-xs text-slate-400 mt-1 font-mono">{t.host}:{t.port} · user {t.v3_username}</p>
                      <p className="text-xs text-slate-500 mt-1">
                        auth {t.v3_auth_protocol} · priv {t.v3_priv_protocol} · poll {t.poll_interval_seconds}s
                      </p>
                      <p className="text-xs text-slate-500 mt-1 font-mono break-all">{t.oids}</p>
                      {t.last_error && <p className="text-xs text-rose-400 mt-1">{t.last_error}</p>}
                    </div>
                    <Button size="sm" variant="outline" className="border-red-500/30 text-red-400 hover:bg-red-500/10" onClick={() => remove(t)}>Delete</Button>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
