'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { alertRulesApi, type AlertRule } from '@/lib/api'

const KNOWN_EVENTS = ['device.offline', 'device.online', 'cpu.high', 'memory.high', 'disk.full', 'security', 'custom']
const SEVERITY_OPTIONS = ['low', 'medium', 'high', 'critical']

export default function AlertRulesPage() {
  const [rules, setRules] = useState<AlertRule[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
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
      toast.error('Failed to load alert rules (admin only)')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

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
      toast.error('Failed to create rule')
    } finally {
      setCreating(false)
    }
  }

  const remove = async (id: string) => {
    if (!confirm('Delete this rule?')) return
    try {
      await alertRulesApi.remove(id)
      toast.success('Rule deleted')
      setRules((prev) => prev.filter((r) => r.id !== id))
    } catch {
      toast.error('Failed to delete rule')
    }
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-4xl space-y-6">
          <div className="flex items-center justify-between mb-6">
            <h1 className="text-2xl font-bold">Alert Rules</h1>
            <Button onClick={() => setShowCreate((s) => !s)}>
              {showCreate ? 'Cancel' : 'New rule'}
            </Button>
          </div>

          {showCreate && (
            <Card className="bg-slate-900/60 border-slate-800/50 mb-6">
              <CardHeader className="pb-3">
                <CardTitle className="text-base">Create rule</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div>
                  <label className="block text-sm text-slate-400 mb-1">Name</label>
                  <input
                    type="text"
                    value={form.name}
                    onChange={(e) => setForm({ ...form, name: e.target.value })}
                    className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white"
                    placeholder="e.g. Critical CPU on prod fleet"
                  />
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="block text-sm text-slate-400 mb-1">Event type</label>
                    <select
                      value={form.event_type}
                      onChange={(e) => setForm({ ...form, event_type: e.target.value })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white"
                    >
                      {KNOWN_EVENTS.map((ev) => <option key={ev} value={ev}>{ev}</option>)}
                    </select>
                  </div>
                  <div>
                    <label className="block text-sm text-slate-400 mb-1">Severity</label>
                    <select
                      value={form.severity}
                      onChange={(e) => setForm({ ...form, severity: e.target.value })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white"
                    >
                      {SEVERITY_OPTIONS.map((s) => <option key={s} value={s}>{s}</option>)}
                    </select>
                  </div>
                </div>
                <div>
                  <label className="block text-sm text-slate-400 mb-1">Email recipients (comma-separated, optional)</label>
                  <input
                    type="text"
                    value={form.email_recipients}
                    onChange={(e) => setForm({ ...form, email_recipients: e.target.value })}
                    className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white font-mono"
                    placeholder="ops@example.com,oncall@example.com"
                  />
                </div>
                <div>
                  <label className="block text-sm text-slate-400 mb-1">Webhook URL (optional)</label>
                  <input
                    type="url"
                    value={form.webhook_url}
                    onChange={(e) => setForm({ ...form, webhook_url: e.target.value })}
                    className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white"
                    placeholder="https://hooks.example.com/abc"
                  />
                </div>
                <div className="flex justify-end gap-2 pt-2 border-t border-slate-800/50">
                  <Button variant="ghost" onClick={() => setShowCreate(false)}>Cancel</Button>
                  <Button onClick={create} disabled={creating}>
                    {creating ? 'Creating…' : 'Create'}
                  </Button>
                </div>
              </CardContent>
            </Card>
          )}

          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">Loading…</CardContent>
            </Card>
          ) : rules.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">
                <p>No alert rules configured.</p>
                <p className="text-sm mt-2">Create one to wire alerts to email or a webhook.</p>
              </CardContent>
            </Card>
          ) : (
            <div className="grid gap-3">
              {rules.map((r) => (
                <Card key={r.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardContent className="py-4 flex items-start justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium text-white">{r.name}</p>
                      <div className="flex flex-wrap gap-2 mt-2">
                        <span className="px-2 py-0.5 rounded border border-slate-700 bg-slate-800 text-xs text-slate-300 font-mono">
                          {r.event_type}
                        </span>
                        <span className="px-2 py-0.5 rounded border border-amber-500/30 bg-amber-500/10 text-xs text-amber-300">
                          {r.severity}
                        </span>
                        {r.enabled ? (
                          <span className="px-2 py-0.5 rounded border border-emerald-500/30 bg-emerald-500/10 text-xs text-emerald-300">enabled</span>
                        ) : (
                          <span className="px-2 py-0.5 rounded border border-slate-700 bg-slate-800 text-xs text-slate-400">disabled</span>
                        )}
                      </div>
                      {(r.email_recipients || r.webhook_url) && (
                        <p className="text-xs text-slate-500 mt-2">
                          {r.email_recipients && <>email: {r.email_recipients}</>}
                          {r.email_recipients && r.webhook_url && ' · '}
                          {r.webhook_url && <>webhook: <span className="font-mono">{r.webhook_url}</span></>}
                        </p>
                      )}
                    </div>
                    <Button
                      size="sm"
                      variant="outline"
                      className="border-red-500/30 text-red-400 hover:bg-red-500/10"
                      onClick={() => remove(r.id)}
                    >
                      Delete
                    </Button>
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
