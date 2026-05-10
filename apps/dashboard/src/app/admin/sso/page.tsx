'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, Section } from '@/components/ui/page'
import { Code } from '@/components/ui/status'
import { ConfirmDialog } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { oidcApi, type OIDCConfig } from '@/lib/api'

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

export default function SSOPage() {
  const [cfg, setCfg] = useState<OIDCConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [confirmRemove, setConfirmRemove] = useState(false)
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
    } catch {
      toast.error('Failed to load')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

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
    } finally {
      setSaving(false)
    }
  }

  const remove = async () => {
    try {
      await oidcApi.remove()
      toast.success('Removed')
      setConfirmRemove(false)
      await load()
    } catch {
      toast.error('Delete failed')
    }
  }

  const loginURL =
    typeof window !== 'undefined' && cfg?.configured && cfg.enabled
      ? `${window.location.origin}/api/auth/oidc/login?tenant=${encodeURIComponent('YOUR_TENANT_ID')}`
      : ''

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="System"
          title="SSO (OpenID Connect)"
          description="Federate login through your IdP. Per-tenant configuration."
          actions={
            <div className="flex items-center gap-2">
              {cfg?.configured && (
                <Button size="sm" variant="ghost" onClick={() => setConfirmRemove(true)}>
                  Remove
                </Button>
              )}
              <Button size="sm" onClick={save} disabled={saving || loading}>
                {saving ? 'Saving…' : 'Save'}
              </Button>
            </div>
          }
        />

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : (
          <div className="max-w-2xl space-y-6">
            <Section title="Provider" className="mb-0">
              <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] p-4 space-y-4">
                <div>
                  <label className={labelCls}>Issuer URL</label>
                  <input
                    type="url"
                    value={form.issuer_url}
                    onChange={(e) => setForm({ ...form, issuer_url: e.target.value })}
                    placeholder="https://accounts.google.com"
                    className={`w-full ${inputCls} font-mono`}
                  />
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className={labelCls}>Client ID</label>
                    <input
                      type="text"
                      value={form.client_id}
                      onChange={(e) => setForm({ ...form, client_id: e.target.value })}
                      className={`w-full ${inputCls}`}
                    />
                  </div>
                  <div>
                    <label className={labelCls}>
                      Client secret {cfg?.configured && <span className="text-white/35 normal-case">(re-paste to rotate)</span>}
                    </label>
                    <input
                      type="password"
                      value={form.client_secret}
                      onChange={(e) => setForm({ ...form, client_secret: e.target.value })}
                      className={`w-full ${inputCls}`}
                    />
                  </div>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className={labelCls}>Default role for JIT users</label>
                    <select
                      value={form.default_role}
                      onChange={(e) => setForm({ ...form, default_role: e.target.value as 'user' | 'admin' })}
                      className={`w-full ${inputCls}`}
                    >
                      <option value="user">user</option>
                      <option value="admin">admin</option>
                    </select>
                  </div>
                  <div className="flex items-end">
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
                </div>
              </div>
            </Section>

            {cfg?.configured && cfg.enabled && (
              <Section title="User-facing login URL" className="mb-0">
                <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] p-4 space-y-3">
                  <p className="text-[12px] text-white/55">
                    Replace <Code>YOUR_TENANT_ID</Code> with your tenant id, then link from your IdP or docs.
                  </p>
                  <code className="block bg-black/40 px-3 py-2 rounded text-[11.5px] font-mono text-white/85 break-all select-all">
                    {loginURL}
                  </code>
                  <p className="text-[11.5px] text-white/45">
                    Configure your IdP&apos;s redirect URI to:{' '}
                    <Code>
                      {typeof window !== 'undefined' ? `${window.location.origin}/api/auth/oidc/callback` : ''}
                    </Code>
                  </p>
                </div>
              </Section>
            )}
          </div>
        )}

        <ConfirmDialog
          open={confirmRemove}
          onClose={() => setConfirmRemove(false)}
          onConfirm={remove}
          title="Remove OIDC config?"
          description="Stops federated login. Existing JIT users keep working with passwords if they have one set."
          confirmLabel="Remove"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
