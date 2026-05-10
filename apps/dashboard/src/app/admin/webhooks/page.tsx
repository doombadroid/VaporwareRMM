'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
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

export default function WebhooksPage() {
  const [hooks, setHooks] = useState<Webhook[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ url: '', secret: '', events: '', enabled: true })

  const load = async () => {
    setLoading(true)
    try {
      setHooks(await webhooksApi.list())
    } catch {
      toast.error('Failed to load webhooks (admin only)')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

  const create = async () => {
    if (!form.url || !form.events) {
      toast.error('URL and events are required')
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
      toast.error('Failed to create webhook')
    } finally {
      setCreating(false)
    }
  }

  const remove = async (id: string) => {
    if (!confirm('Delete this webhook?')) return
    try {
      await webhooksApi.remove(id)
      toast.success('Webhook deleted')
      setHooks((prev) => prev.filter((h) => h.id !== id))
    } catch {
      toast.error('Failed to delete webhook')
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
        <div className="max-w-4xl space-y-6">
          <div className="flex items-center justify-between mb-6">
            <h1 className="text-2xl font-bold">Webhooks</h1>
            <Button onClick={() => setShowCreate((s) => !s)}>
              {showCreate ? 'Cancel' : 'New webhook'}
            </Button>
          </div>

          {showCreate && (
            <Card className="bg-slate-900/60 border-slate-800/50 mb-6">
              <CardHeader className="pb-3">
                <CardTitle className="text-base">Create webhook</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div>
                  <label className="block text-sm text-slate-400 mb-1">URL (https://, no private IPs)</label>
                  <input
                    type="url"
                    value={form.url}
                    onChange={(e) => setForm({ ...form, url: e.target.value })}
                    className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white"
                    placeholder="https://hooks.example.com/abc"
                  />
                </div>
                <div>
                  <label className="block text-sm text-slate-400 mb-1">Secret (HMAC, optional)</label>
                  <input
                    type="password"
                    value={form.secret}
                    onChange={(e) => setForm({ ...form, secret: e.target.value })}
                    className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white font-mono"
                    placeholder="signing secret"
                  />
                </div>
                <div>
                  <label className="block text-sm text-slate-400 mb-2">Events</label>
                  <div className="flex flex-wrap gap-2">
                    {KNOWN_EVENTS.map((ev) => (
                      <button
                        key={ev}
                        type="button"
                        onClick={() => toggleEvent(ev)}
                        className={`px-2 py-1 rounded border text-xs ${
                          formEvents.includes(ev)
                            ? 'bg-blue-500/15 border-blue-500/40 text-blue-300'
                            : 'bg-slate-800 border-slate-700 text-slate-400'
                        }`}
                      >
                        {ev}
                      </button>
                    ))}
                  </div>
                  <input
                    type="text"
                    value={form.events}
                    onChange={(e) => setForm({ ...form, events: e.target.value })}
                    className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white font-mono mt-2"
                    placeholder="comma-separated event names"
                  />
                </div>
                <label className="flex items-center gap-2 text-sm text-slate-300">
                  <input
                    type="checkbox"
                    checked={form.enabled}
                    onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
                    className="rounded border-slate-600 bg-slate-800"
                  />
                  Enabled
                </label>
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
          ) : hooks.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">No webhooks configured.</CardContent>
            </Card>
          ) : (
            <div className="grid gap-3">
              {hooks.map((h) => (
                <Card key={h.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardContent className="py-4 flex items-start justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-mono text-white break-all">{h.url}</p>
                      <div className="flex flex-wrap gap-1 mt-2">
                        {h.events.split(',').map((ev) => ev.trim()).filter(Boolean).map((ev) => (
                          <span key={ev} className="px-2 py-0.5 rounded border border-slate-700 bg-slate-800 text-xs text-slate-300">
                            {ev}
                          </span>
                        ))}
                      </div>
                      <p className="text-xs text-slate-500 mt-2">
                        {h.enabled ? <span className="text-emerald-400">enabled</span> : <span className="text-slate-500">disabled</span>}
                        {h.secret && <span> · secret set</span>}
                        <span> · created {new Date(h.created_at * 1000).toLocaleDateString()}</span>
                      </p>
                    </div>
                    <Button
                      size="sm"
                      variant="outline"
                      className="border-red-500/30 text-red-400 hover:bg-red-500/10"
                      onClick={() => remove(h.id)}
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
