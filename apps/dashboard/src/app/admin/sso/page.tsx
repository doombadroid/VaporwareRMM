'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { oidcApi, type OIDCConfig } from '@/lib/api'

export default function SSOPage() {
  const [cfg, setCfg] = useState<OIDCConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState({
    issuer_url: '',
    client_id: '',
    client_secret: '',
    default_role: 'user' as 'user' | 'admin',
    enabled: true,
  })

  const load = async () => {
    setLoading(true)
    try {
      const c = await oidcApi.get()
      setCfg(c)
      if (c.configured) {
        setForm((f) => ({
          ...f,
          issuer_url: c.issuer_url || '',
          client_id: c.client_id || '',
          default_role: (c.default_role as 'user' | 'admin') || 'user',
          enabled: !!c.enabled,
        }))
      }
    } catch { toast.error('Failed to load (admin only?)') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

  const save = async () => {
    if (!form.issuer_url || !form.client_id || !form.client_secret) {
      toast.error('issuer / client_id / client_secret required')
      return
    }
    setSaving(true)
    try {
      await oidcApi.save(form)
      toast.success('Saved')
      setForm((f) => ({ ...f, client_secret: '' }))
      await load()
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || 'Save failed'
      toast.error(msg)
    } finally { setSaving(false) }
  }

  const remove = async () => {
    if (!confirm('Remove OIDC config?')) return
    try { await oidcApi.remove(); toast.success('Removed'); await load() }
    catch { toast.error('Delete failed') }
  }

  const loginURL = typeof window !== 'undefined' && cfg?.configured && cfg.enabled
    ? `${window.location.origin}/api/auth/oidc/login?tenant=${encodeURIComponent('YOUR_TENANT_ID')}`
    : ''

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-3xl space-y-6">
          <h1 className="text-2xl font-bold">SSO (OpenID Connect)</h1>
          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">Loading…</CardContent></Card>
          ) : (
            <>
              <Card className="bg-slate-900/60 border-slate-800/50">
                <CardHeader className="pb-3">
                  <CardTitle className="text-base">Provider</CardTitle>
                </CardHeader>
                <CardContent className="space-y-3">
                  <div>
                    <label className="block text-xs text-slate-400 mb-1">Issuer URL</label>
                    <input
                      type="url"
                      value={form.issuer_url}
                      onChange={(e) => setForm({ ...form, issuer_url: e.target.value })}
                      placeholder="https://accounts.google.com"
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm font-mono"
                    />
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="block text-xs text-slate-400 mb-1">Client ID</label>
                      <input
                        type="text"
                        value={form.client_id}
                        onChange={(e) => setForm({ ...form, client_id: e.target.value })}
                        className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
                      />
                    </div>
                    <div>
                      <label className="block text-xs text-slate-400 mb-1">
                        Client Secret {cfg?.configured && '(re-paste to rotate)'}
                      </label>
                      <input
                        type="password"
                        value={form.client_secret}
                        onChange={(e) => setForm({ ...form, client_secret: e.target.value })}
                        className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
                      />
                    </div>
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="block text-xs text-slate-400 mb-1">Default role for JIT users</label>
                      <select
                        value={form.default_role}
                        onChange={(e) => setForm({ ...form, default_role: e.target.value as 'user' | 'admin' })}
                        className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-2 text-sm"
                      >
                        <option value="user">user</option>
                        <option value="admin">admin</option>
                      </select>
                    </div>
                    <div className="flex items-end">
                      <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={form.enabled}
                          onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
                          className="rounded border-slate-600 bg-slate-800"
                        />
                        Enabled
                      </label>
                    </div>
                  </div>
                  <div className="flex justify-end gap-2 pt-3 border-t border-slate-800/50">
                    {cfg?.configured && (
                      <Button variant="outline" className="border-red-500/30 text-red-400 hover:bg-red-500/10" onClick={remove}>
                        Remove
                      </Button>
                    )}
                    <Button onClick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
                  </div>
                </CardContent>
              </Card>

              {cfg?.configured && cfg.enabled && (
                <Card className="bg-slate-900/60 border-slate-800/50">
                  <CardHeader className="pb-3">
                    <CardTitle className="text-base">User-facing login URL</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <p className="text-xs text-slate-400 mb-2">
                      Replace YOUR_TENANT_ID with your tenant id and link from your IdP / docs:
                    </p>
                    <code className="block bg-slate-900/40 px-3 py-2 rounded text-xs font-mono text-slate-300 break-all">
                      {loginURL}
                    </code>
                    <p className="text-xs text-slate-500 mt-2">
                      Configure your IdP&apos;s redirect URI to: <code className="font-mono">{typeof window !== 'undefined' ? `${window.location.origin}/api/auth/oidc/callback` : ''}</code>
                    </p>
                  </CardContent>
                </Card>
              )}
            </>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
