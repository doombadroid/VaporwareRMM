'use client'

import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { toast } from 'sonner'
import { branding as brandingApi, default as api, totpApi } from '@/lib/api'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, Section, EmptyState } from '@/components/ui/page'
import { StatusDot, Code } from '@/components/ui/status'
import { ConfirmDialog } from '@/components/ui/sheet'
import { Settings, Palette, Bot, CheckCircle, Shield, Lock, Copy } from 'lucide-react'

interface BrandingConfig {
  app_name: string
  icon_url: string
  company_name: string
  primary_color: string
}

type SettingsTab = 'general' | 'branding' | 'agents' | 'sessions' | 'security'

interface SessionRow {
  id: string
  ip_address?: string
  user_agent?: string
  created_at: number
}

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

export default function SettingsPage() {
  const [activeTab, setActiveTab] = useState<SettingsTab>('general')
  const [branding, setBranding] = useState<BrandingConfig>({
    app_name: 'vaporRMM',
    icon_url: '',
    company_name: 'Vaporware RMM',
    primary_color: '#3b82f6',
  })
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [sessions, setSessions] = useState<SessionRow[]>([])
  const [loadingSessions, setLoadingSessions] = useState(false)
  const [confirmRevokeAll, setConfirmRevokeAll] = useState(false)
  const [totpEnabled, setTotpEnabled] = useState(false)
  const [totpSetupData, setTotpSetupData] = useState<{ uri: string; secret: string; backup_codes: string[] } | null>(null)
  const [totpQrUrl, setTotpQrUrl] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [totpLoading, setTotpLoading] = useState(false)
  const [origin, setOrigin] = useState('')

  useEffect(() => {
    setOrigin(window.location.origin)
  }, [])
  useEffect(() => {
    brandingApi.get().then(setBranding).catch(() => toast.error('Failed to load branding'))
  }, [])
  useEffect(() => {
    if (activeTab === 'security') {
      totpApi.status().then((d) => setTotpEnabled(d.enabled)).catch(() => {})
    }
  }, [activeTab])
  useEffect(() => {
    if (!totpSetupData?.uri) {
      setTotpQrUrl('')
      return
    }
    import('qrcode').then((QRCode) => {
      QRCode.toDataURL(totpSetupData!.uri, { width: 200 }).then(setTotpQrUrl)
    })
  }, [totpSetupData?.uri])

  const handleSaveBranding = async () => {
    setSaving(true)
    setSaved(false)
    try {
      await brandingApi.update(branding)
      setSaved(true)
      setTimeout(() => setSaved(false), 3000)
    } catch {
      toast.error('Failed to save branding')
    } finally {
      setSaving(false)
    }
  }

  const copy = (text: string) => {
    navigator.clipboard.writeText(text)
    toast.success('Copied')
  }

  const refreshSessions = async () => {
    setLoadingSessions(true)
    try {
      const res = await api.get<{ sessions: SessionRow[] }>('/sessions')
      setSessions(res.data.sessions || [])
    } catch {
      toast.error('Failed to load sessions')
    } finally {
      setLoadingSessions(false)
    }
  }

  const revokeAllOthers = async () => {
    try {
      await api.delete('/sessions')
      toast.success('Other sessions revoked')
      await refreshSessions()
      setConfirmRevokeAll(false)
    } catch {
      toast.error('Failed to revoke sessions')
    }
  }

  const installCmdLinux = origin
    ? `curl -fsSL ${origin}/api/branding/agent-install?format=script | sudo bash -s -- --server ${origin}`
    : ''
  const installCmdWindows = origin
    ? `Invoke-WebRequest -Uri '${origin}/api/branding/agent-install?format=script' -UseBasicParsing | Invoke-Expression`
    : ''

  const tabs: { id: SettingsTab; label: string; icon: React.ElementType }[] = [
    { id: 'general', label: 'General', icon: Settings },
    { id: 'branding', label: 'Branding', icon: Palette },
    { id: 'agents', label: 'Agents', icon: Bot },
    { id: 'sessions', label: 'Sessions', icon: Shield },
    { id: 'security', label: 'Security', icon: Lock },
  ]

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="System"
          title="Settings"
          description="Server identity, branding, agent install, sessions, and 2FA."
          separator={false}
        />

        <div className="flex gap-6">
          <aside className="w-56 shrink-0">
            <nav className="space-y-0.5">
              {tabs.map((t) => {
                const active = activeTab === t.id
                return (
                  <button
                    key={t.id}
                    onClick={() => setActiveTab(t.id)}
                    className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-md text-[13px] transition-colors ${
                      active
                        ? 'bg-white/[0.06] text-white'
                        : 'text-white/55 hover:text-white hover:bg-white/[0.02]'
                    }`}
                  >
                    <t.icon className={`w-3.5 h-3.5 ${active ? 'text-cyan-400' : 'text-white/40'}`} />
                    {t.label}
                  </button>
                )
              })}
            </nav>
          </aside>

          <div className="flex-1 max-w-2xl">
            {activeTab === 'general' && (
              <>
                <Section title="Server identity" className="mb-6">
                  <div className="space-y-4">
                    <div>
                      <label className={labelCls}>Server URL</label>
                      <input type="text" value={origin} readOnly className={`w-full ${inputCls} font-mono`} />
                      <p className="text-[11.5px] text-white/35 mt-1.5">URL agents connect to.</p>
                    </div>
                    <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] px-4 py-3">
                      <p className="text-[12.5px] font-medium text-white/85 mb-1">Runtime timing</p>
                      <p className="text-[11.5px] text-white/55 leading-relaxed">
                        Heartbeat interval, offline threshold, and command timeout are set on the server via env vars
                        (<Code>OFFLINE_THRESHOLD_SECONDS</Code>, agent build flags). Edit your deployment config and restart.
                      </p>
                    </div>
                  </div>
                </Section>
                <TailscaleIndicator />
              </>
            )}

            {activeTab === 'branding' && (
              <Section title="Branding" description="Tenant-visible appearance." className="mb-0">
                <div className="space-y-4">
                  <div>
                    <label className={labelCls}>App name</label>
                    <input
                      type="text"
                      value={branding.app_name}
                      onChange={(e) => setBranding({ ...branding, app_name: e.target.value })}
                      className={`w-full ${inputCls}`}
                    />
                  </div>
                  <div>
                    <label className={labelCls}>Company name</label>
                    <input
                      type="text"
                      value={branding.company_name}
                      onChange={(e) => setBranding({ ...branding, company_name: e.target.value })}
                      className={`w-full ${inputCls}`}
                    />
                  </div>
                  <div>
                    <label className={labelCls}>Logo URL</label>
                    <input
                      type="text"
                      value={branding.icon_url}
                      onChange={(e) => setBranding({ ...branding, icon_url: e.target.value })}
                      className={`w-full ${inputCls}`}
                      placeholder="https://example.com/logo.png"
                    />
                    {branding.icon_url && (
                      <div className="mt-2 flex items-center gap-2">
                        <span className="text-[11px] text-white/35">Preview</span>
                        {/* eslint-disable-next-line @next/next/no-img-element */}
                        <img
                          src={branding.icon_url}
                          alt="Logo preview"
                          className="w-7 h-7 rounded"
                          onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')}
                        />
                      </div>
                    )}
                  </div>
                  <div>
                    <label className={labelCls}>Primary color</label>
                    <div className="flex items-center gap-2">
                      <input
                        type="color"
                        value={branding.primary_color}
                        onChange={(e) => setBranding({ ...branding, primary_color: e.target.value })}
                        className="w-9 h-9 rounded-md cursor-pointer bg-transparent border border-white/[0.08]"
                      />
                      <input
                        type="text"
                        value={branding.primary_color}
                        onChange={(e) => setBranding({ ...branding, primary_color: e.target.value })}
                        className={`flex-1 ${inputCls} font-mono`}
                      />
                    </div>
                  </div>
                  <div className="flex items-center justify-end gap-3 pt-3 border-t border-white/[0.06]">
                    {saved && (
                      <span className="text-[12px] text-emerald-300 inline-flex items-center gap-1">
                        <CheckCircle className="w-3.5 h-3.5" />
                        Saved
                      </span>
                    )}
                    <Button size="sm" onClick={handleSaveBranding} disabled={saving}>
                      {saving ? 'Saving…' : 'Save branding'}
                    </Button>
                  </div>
                </div>
              </Section>
            )}

            {activeTab === 'agents' && (
              <Section title="Agent install" description="One-line install commands for new devices.">
                <div className="space-y-4">
                  <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] px-4 py-3">
                    <p className="text-[12.5px] font-medium text-white/85 mb-2">Linux / macOS</p>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 bg-black/40 px-3 py-2 rounded text-[11.5px] font-mono text-white/85 break-all select-all">
                        {installCmdLinux || '…'}
                      </code>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={!installCmdLinux}
                        onClick={() => copy(installCmdLinux)}
                      >
                        <Copy className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                    <p className="text-[11px] text-white/35 mt-2">
                      Tenants with a registration secret should set <Code>REGISTRATION_SECRET</Code> before piping.
                    </p>
                  </div>
                  <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] px-4 py-3">
                    <p className="text-[12.5px] font-medium text-white/85 mb-2">Windows (PowerShell)</p>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 bg-black/40 px-3 py-2 rounded text-[11.5px] font-mono text-white/85 break-all select-all">
                        {installCmdWindows || '…'}
                      </code>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={!installCmdWindows}
                        onClick={() => copy(installCmdWindows)}
                      >
                        <Copy className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  </div>
                  <p className="text-[11.5px] text-white/40">
                    Tailscale + Sunshine are configured per-device from the device detail page once an agent registers.
                  </p>
                </div>
              </Section>
            )}

            {activeTab === 'sessions' && (
              <Section
                title="Active sessions"
                description="Sign out of stale browsers and devices."
                actions={
                  <div className="flex gap-2">
                    <Button size="sm" variant="ghost" onClick={refreshSessions} disabled={loadingSessions}>
                      {loadingSessions ? 'Loading…' : 'Refresh'}
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => setConfirmRevokeAll(true)}>
                      Revoke all others
                    </Button>
                  </div>
                }
              >
                {sessions.length === 0 && !loadingSessions ? (
                  <EmptyState title="No sessions loaded yet." hint="Hit refresh to load your active sessions." />
                ) : (
                  <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
                    {sessions.map((s) => (
                      <li key={s.id} className="px-4 py-3 flex items-center gap-3">
                        <div className="min-w-0 flex-1">
                          <p className="text-[13px] text-white/85 font-mono">{s.ip_address || 'Unknown IP'}</p>
                          <p className="text-[11.5px] text-white/45 truncate">{s.user_agent || 'Unknown browser'}</p>
                          <p className="text-[11px] text-white/30 mt-0.5">
                            Created {new Date(s.created_at * 1000).toLocaleString()}
                          </p>
                        </div>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={async () => {
                            try {
                              await api.delete(`/sessions/${s.id}`)
                              toast.success('Session revoked')
                              setSessions(sessions.filter((x) => x.id !== s.id))
                            } catch {
                              toast.error('Failed to revoke')
                            }
                          }}
                        >
                          Revoke
                        </Button>
                      </li>
                    ))}
                  </ul>
                )}
              </Section>
            )}

            {activeTab === 'security' && (
              <Section title="Two-factor authentication" description="Authenticator-app codes on every login." className="mb-0">
                <div className="space-y-5">
                  <div className="flex items-center gap-3 px-4 py-3 rounded-lg border border-white/[0.06] bg-white/[0.01]">
                    <StatusDot tone={totpEnabled ? 'success' : 'muted'} />
                    <div>
                      <p className="text-[13px] font-medium text-white/90">
                        Two-factor auth is {totpEnabled ? 'enabled' : 'disabled'}
                      </p>
                      <p className="text-[11.5px] text-white/45 mt-0.5">
                        {totpEnabled
                          ? 'Your account requires a TOTP code on login.'
                          : 'Enable to require an authenticator code on every login.'}
                      </p>
                    </div>
                  </div>

                  {!totpEnabled && !totpSetupData && (
                    <Button
                      size="sm"
                      onClick={async () => {
                        setTotpLoading(true)
                        try {
                          const data = await totpApi.setup()
                          setTotpSetupData(data)
                          setTotpCode('')
                        } catch {
                          toast.error('Failed to start setup')
                        } finally {
                          setTotpLoading(false)
                        }
                      }}
                      disabled={totpLoading}
                    >
                      {totpLoading ? 'Generating…' : 'Enable two-factor auth'}
                    </Button>
                  )}

                  {totpSetupData && (
                    <div className="space-y-4 border border-white/[0.06] rounded-lg bg-white/[0.01] p-4">
                      <p className="text-[12.5px] text-white/75">
                        Scan with your authenticator app (Google Authenticator, Authy, 1Password). Then enter the 6-digit code to confirm.
                      </p>
                      <div className="flex flex-col items-center gap-3">
                        {totpQrUrl && (
                          // eslint-disable-next-line @next/next/no-img-element
                          <img src={totpQrUrl} alt="TOTP QR code" className="rounded-md p-2 bg-white" width={200} height={200} />
                        )}
                        <div className="text-center">
                          <p className="text-[11px] text-white/40 mb-1">Manual entry key</p>
                          <Code className="text-[12px]">{totpSetupData.secret}</Code>
                        </div>
                      </div>
                      <div className="rounded-md border border-amber-500/15 bg-amber-500/[0.04] p-3 space-y-2">
                        <p className="text-[12px] font-medium text-amber-300">Save these backup codes</p>
                        <p className="text-[11.5px] text-white/55">
                          Each code can be used once if you lose access to your authenticator.
                        </p>
                        <div className="grid grid-cols-2 gap-1.5 mt-2">
                          {totpSetupData.backup_codes.map((c) => (
                            <code
                              key={c}
                              className="text-[11.5px] font-mono text-white/85 bg-black/40 px-2 py-1 rounded text-center select-all"
                            >
                              {c}
                            </code>
                          ))}
                        </div>
                      </div>
                      <input
                        type="text"
                        inputMode="numeric"
                        pattern="[0-9]{6}"
                        maxLength={6}
                        value={totpCode}
                        onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ''))}
                        placeholder="6-digit code"
                        className={`w-full ${inputCls} text-center tracking-widest font-mono`}
                      />
                      <div className="flex gap-2">
                        <Button
                          size="sm"
                          className="flex-1"
                          onClick={async () => {
                            setTotpLoading(true)
                            try {
                              await totpApi.enable(totpCode)
                              setTotpEnabled(true)
                              setTotpSetupData(null)
                              setTotpCode('')
                              toast.success('2FA enabled')
                            } catch {
                              toast.error('Invalid code')
                            } finally {
                              setTotpLoading(false)
                            }
                          }}
                          disabled={totpLoading || totpCode.length !== 6}
                        >
                          {totpLoading ? 'Verifying…' : 'Enable'}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => {
                            setTotpSetupData(null)
                            setTotpCode('')
                          }}
                        >
                          Cancel
                        </Button>
                      </div>
                    </div>
                  )}

                  {totpEnabled && (
                    <div className="space-y-3 pt-3 border-t border-white/[0.06]">
                      <p className="text-[12.5px] text-white/55">
                        To disable 2FA, enter your current authenticator code.
                      </p>
                      <div className="flex gap-2">
                        <input
                          type="text"
                          inputMode="numeric"
                          pattern="[0-9]{6}"
                          maxLength={6}
                          value={totpCode}
                          onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ''))}
                          placeholder="Current code"
                          className={`flex-1 ${inputCls} text-center tracking-widest font-mono`}
                        />
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={async () => {
                            setTotpLoading(true)
                            try {
                              await totpApi.disable(totpCode)
                              setTotpEnabled(false)
                              setTotpCode('')
                              toast.success('2FA disabled')
                            } catch {
                              toast.error('Invalid code')
                            } finally {
                              setTotpLoading(false)
                            }
                          }}
                          disabled={totpLoading || totpCode.length !== 6}
                        >
                          Disable
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              </Section>
            )}
          </div>
        </div>

        <ConfirmDialog
          open={confirmRevokeAll}
          onClose={() => setConfirmRevokeAll(false)}
          onConfirm={revokeAllOthers}
          title="Revoke all other sessions?"
          description="Signs out of every browser and device except this one. You'll need to sign in again on those devices."
          confirmLabel="Revoke all"
          tone="warn"
        />
      </DashboardShell>
    </AuthGuard>
  )
}

// TailscaleIndicator: read-only tenant-admin view of the
// fleet-wide Tailscale connection. Tenant admins can't reach
// Settings → Network (commit 5), so this small card on Settings →
// General is the only surface they see. The server endpoint
// returns just { connected, tailnet_display_name } for non-super-
// admin roles (commit 3), so the operational metadata
// (connected_at, who-connected, the raw tailnet name) is never
// exposed to tenants.
function TailscaleIndicator() {
  const [conn, setConn] = useState<{ connected?: boolean; tailnet_display_name?: string }>({})
  useEffect(() => {
    api.get('/tailscale/connection').then(r => setConn(r.data)).catch(() => setConn({ connected: false }))
  }, [])
  return (
    <Section title="Network" className="mb-0">
      <div className="flex items-center gap-3">
        <StatusDot tone={conn.connected ? 'online' : 'offline'} pulse={!!conn.connected} />
        {conn.connected ? (
          <span className="text-[12.5px] text-white/80">
            Tailscale — connected to{' '}
            <span className="font-mono text-white">{conn.tailnet_display_name || 'tailnet'}</span>
          </span>
        ) : (
          <span className="text-[12.5px] text-white/55">Tailscale — not configured</span>
        )}
      </div>
      <p className="text-[11.5px] text-white/35 mt-1.5">
        Network configuration is managed by your administrator.
      </p>
    </Section>
  )
}
