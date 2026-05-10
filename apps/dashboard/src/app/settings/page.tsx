'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { toast } from 'sonner'
import { branding as brandingApi, default as api } from '@/lib/api'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'
import { Settings, Palette, Bot, CheckCircle, Shield, Lock } from 'lucide-react'
import { totpApi } from '@/lib/api'

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
  const [totpEnabled, setTotpEnabled] = useState(false)
  const [totpSetupData, setTotpSetupData] = useState<{ uri: string; secret: string; backup_codes: string[] } | null>(null)
  const [totpQrUrl, setTotpQrUrl] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [totpLoading, setTotpLoading] = useState(false)
  // Origin computed in effect to avoid SSR hydration mismatch on window.location.
  const [origin, setOrigin] = useState('')

  useEffect(() => {
    setOrigin(window.location.origin)
  }, [])

  useEffect(() => {
    loadSettings()
  }, [])

  useEffect(() => {
    if (activeTab === 'security') {
      totpApi.status().then(d => setTotpEnabled(d.enabled)).catch(() => {})
    }
  }, [activeTab])

  useEffect(() => {
    if (!totpSetupData?.uri) { setTotpQrUrl(''); return }
    import('qrcode').then((QRCode) => {
      QRCode.toDataURL(totpSetupData!.uri, { width: 200 }).then(setTotpQrUrl)
    })
  }, [totpSetupData?.uri])

  const loadSettings = async () => {
    try {
      const brandingData = await brandingApi.get()
      setBranding(brandingData)
    } catch {
      toast.error('Failed to load branding')
    }
  }

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

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text)
  }

  const tabs: { id: SettingsTab; label: string; icon: React.ElementType }[] = [
    { id: 'general', label: 'General', icon: Settings },
    { id: 'branding', label: 'Branding', icon: Palette },
    { id: 'agents', label: 'Agents', icon: Bot },
    { id: 'sessions', label: 'Sessions', icon: Shield },
    { id: 'security', label: 'Security', icon: Lock },
  ]

  const installCmdLinux = origin
    ? `curl -fsSL ${origin}/api/branding/agent-install?format=script | sudo bash -s -- --server ${origin}`
    : ''
  const installCmdWindows = origin
    ? `Invoke-WebRequest -Uri '${origin}/api/branding/agent-install?format=script' -UseBasicParsing | Invoke-Expression`
    : ''

  return (
    <AuthGuard>
    <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
      <header className="border-b border-slate-800/50 bg-slate-950/80 backdrop-blur-xl sticky top-0 z-50">
        <div className="container mx-auto px-6 py-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-8">
              <Link href="/" className="flex items-center gap-2">
                <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-blue-500 to-purple-600 flex items-center justify-center">
                  <span className="text-white font-bold text-sm">V</span>
                </div>
                <div>
                  <span className="text-xl font-bold bg-gradient-to-r from-blue-400 to-purple-400 bg-clip-text text-transparent">
                    {branding.app_name}
                  </span>
                  <p className="text-[10px] text-slate-500 -mt-1">Settings</p>
                </div>
              </Link>
              <nav className="hidden md:flex items-center gap-1">
                <Link href="/" className="px-3 py-2 text-sm text-slate-400 hover:text-white hover:bg-slate-800/50 rounded-lg transition-colors">Dashboard</Link>
                <Link href="/agents" className="px-3 py-2 text-sm text-slate-400 hover:text-white hover:bg-slate-800/50 rounded-lg transition-colors">Agents</Link>
                <Link href="/tickets" className="px-3 py-2 text-sm text-slate-400 hover:text-white hover:bg-slate-800/50 rounded-lg transition-colors">Tickets</Link>
                <Link href="/settings" className="px-3 py-2 text-sm font-medium text-blue-400 bg-blue-500/10 rounded-lg">Settings</Link>
              </nav>
            </div>
            <div className="flex items-center gap-3">
              <ThemeToggle />
              <Link href="/">
                <Button variant="ghost" size="sm" className="text-slate-400 hover:text-white">← Back to Dashboard</Button>
              </Link>
            </div>
          </div>
        </div>
      </header>

      <main className="container mx-auto px-6 py-8">
        <div className="flex gap-6">
          <div className="w-64 flex-shrink-0">
            <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm sticky top-24">
              <CardContent className="p-4">
                <nav className="space-y-1">
                  {tabs.map(tab => (
                    <button
                      key={tab.id}
                      onClick={() => setActiveTab(tab.id)}
                      className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors ${
                        activeTab === tab.id
                          ? 'bg-blue-500/10 text-blue-400 border border-blue-500/20'
                          : 'text-slate-400 hover:text-white hover:bg-slate-800/50'
                      }`}
                    >
                      <tab.icon className="w-5 h-5" />
                      {tab.label}
                    </button>
                  ))}
                </nav>
              </CardContent>
            </Card>
          </div>

          <div className="flex-1 max-w-3xl">
            {activeTab === 'general' && (
              <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle>General</CardTitle>
                  <CardDescription>Server identity and runtime configuration</CardDescription>
                </CardHeader>
                <CardContent className="space-y-6">
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Server URL</label>
                    <input
                      type="text"
                      value={origin}
                      readOnly
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white focus:outline-none"
                    />
                    <p className="text-xs text-slate-500 mt-1">URL agents connect to.</p>
                  </div>
                  <div className="rounded-lg border border-slate-800/50 bg-slate-800/30 p-4">
                    <p className="text-sm font-medium text-white mb-1">Runtime timing</p>
                    <p className="text-xs text-slate-400">
                      Heartbeat interval, offline threshold, and command timeout are set on the server via environment
                      variables (<code className="font-mono text-slate-300">OFFLINE_THRESHOLD_SECONDS</code>, agent
                      build flags). Edit your deployment config and restart the server to change them.
                    </p>
                  </div>
                </CardContent>
              </Card>
            )}

            {activeTab === 'branding' && (
              <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle>Branding Settings</CardTitle>
                  <CardDescription>Customize the appearance of your RMM platform</CardDescription>
                </CardHeader>
                <CardContent className="space-y-6">
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">App Name</label>
                    <input
                      type="text"
                      value={branding.app_name}
                      onChange={(e) => setBranding({ ...branding, app_name: e.target.value })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                      placeholder="vaporRMM"
                    />
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Company Name</label>
                    <input
                      type="text"
                      value={branding.company_name}
                      onChange={(e) => setBranding({ ...branding, company_name: e.target.value })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                      placeholder="Your Company"
                    />
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Logo URL</label>
                    <input
                      type="text"
                      value={branding.icon_url}
                      onChange={(e) => setBranding({ ...branding, icon_url: e.target.value })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                      placeholder="https://example.com/logo.png"
                    />
                    {branding.icon_url && (
                      <div className="mt-2 flex items-center gap-2">
                        <span className="text-xs text-slate-500">Preview:</span>
                        <img src={branding.icon_url} alt="Logo preview" className="w-8 h-8 rounded" onError={(e) => (e.target as HTMLImageElement).style.display = 'none'} />
                      </div>
                    )}
                  </div>
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Primary Color</label>
                    <div className="flex items-center gap-2">
                      <input
                        type="color"
                        value={branding.primary_color}
                        onChange={(e) => setBranding({ ...branding, primary_color: e.target.value })}
                        className="w-10 h-10 rounded-lg cursor-pointer bg-transparent"
                      />
                      <input
                        type="text"
                        value={branding.primary_color}
                        onChange={(e) => setBranding({ ...branding, primary_color: e.target.value })}
                        className="flex-1 bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white font-mono focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                        placeholder="#3b82f6"
                      />
                    </div>
                  </div>
                  <div className="p-4 bg-slate-800/30 rounded-xl">
                    <p className="text-xs text-slate-500 mb-2">Preview</p>
                    <div className="flex items-center gap-3">
                      {branding.icon_url ? (
                        <img src={branding.icon_url} alt="Logo" className="w-8 h-8 rounded" onError={(e) => (e.target as HTMLImageElement).style.display = 'none'} />
                      ) : (
                        <div className="w-8 h-8 rounded bg-gradient-to-br from-blue-500 to-purple-600 flex items-center justify-center text-sm font-bold text-white">
                          {branding.app_name.charAt(0).toUpperCase()}
                        </div>
                      )}
                      <div>
                        <span className="text-sm font-semibold" style={{ color: branding.primary_color }}>{branding.app_name}</span>
                        <p className="text-xs text-slate-500">{branding.company_name}</p>
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center justify-end gap-3 pt-4 border-t border-slate-800/30">
                    {saved && (
                      <span className="text-sm text-green-400 flex items-center gap-1"><CheckCircle className="w-4 h-4" /> Settings saved successfully</span>
                    )}
                    <Button onClick={handleSaveBranding} disabled={saving} style={{ backgroundColor: branding.primary_color }}>
                      {saving ? (
                        <>
                          <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin mr-2" />
                          Saving...
                        </>
                      ) : (
                        'Save Branding'
                      )}
                    </Button>
                  </div>
                </CardContent>
              </Card>
            )}

            {activeTab === 'agents' && (
              <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle>Agent Install</CardTitle>
                  <CardDescription>One-line install commands for new agents</CardDescription>
                </CardHeader>
                <CardContent className="space-y-6">
                  <div className="p-4 bg-slate-800/30 rounded-xl">
                    <h3 className="text-sm font-medium text-white mb-3">Linux / macOS</h3>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 bg-slate-900/50 px-3 py-2 rounded-lg text-xs font-mono text-slate-300 break-all">
                        {installCmdLinux || '…'}
                      </code>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-slate-400 hover:text-white"
                        disabled={!installCmdLinux}
                        onClick={() => copyToClipboard(installCmdLinux)}
                      >Copy</Button>
                    </div>
                    <p className="text-xs text-slate-500 mt-2">
                      Tenants with a registration secret should set <code className="font-mono">REGISTRATION_SECRET</code> before piping (see Tenants page → Rotate registration secret).
                    </p>
                  </div>
                  <div className="p-4 bg-slate-800/30 rounded-xl">
                    <h3 className="text-sm font-medium text-white mb-3">Windows (PowerShell)</h3>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 bg-slate-900/50 px-3 py-2 rounded-lg text-xs font-mono text-slate-300 break-all">
                        {installCmdWindows || '…'}
                      </code>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-slate-400 hover:text-white"
                        disabled={!installCmdWindows}
                        onClick={() => copyToClipboard(installCmdWindows)}
                      >Copy</Button>
                    </div>
                  </div>
                  <p className="text-xs text-slate-500">
                    Tailscale + Sunshine are configured per-device from the device detail page once an agent has registered.
                  </p>
                </CardContent>
              </Card>
            )}

            {activeTab === 'sessions' && (
              <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle>Active Sessions</CardTitle>
                  <CardDescription>Manage your active login sessions</CardDescription>
                </CardHeader>
                <CardContent className="space-y-4">
                  <div className="flex items-center justify-between">
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-xs"
                      onClick={async () => {
                        setLoadingSessions(true)
                        try {
                          const res = await api.get<{ sessions: SessionRow[] }>('/sessions')
                          setSessions(res.data.sessions || [])
                        } catch {
                          toast.error('Failed to load sessions')
                        } finally {
                          setLoadingSessions(false)
                        }
                      }}
                    >
                      {loadingSessions ? 'Loading...' : 'Refresh'}
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-xs border-red-500/30 text-red-400 hover:bg-red-500/10"
                      onClick={async () => {
                        if (!confirm('Revoke all other sessions?')) return
                        try {
                          await api.delete('/sessions')
                          toast.success('Other sessions revoked')
                          const res = await api.get<{ sessions: SessionRow[] }>('/sessions')
                          setSessions(res.data.sessions || [])
                        } catch {
                          toast.error('Failed to revoke sessions')
                        }
                      }}
                    >
                      Revoke All Others
                    </Button>
                  </div>
                  <div className="space-y-2">
                    {sessions.map((s) => (
                      <div key={s.id} className="flex items-center justify-between p-3 bg-slate-800/30 rounded-lg">
                        <div>
                          <p className="text-sm text-white">{s.ip_address || 'Unknown IP'}</p>
                          <p className="text-xs text-slate-500">{s.user_agent || 'Unknown browser'}</p>
                          <p className="text-xs text-slate-600">
                            Created: {new Date(s.created_at * 1000).toLocaleString()}
                          </p>
                        </div>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-xs text-red-400 hover:text-red-300 hover:bg-red-500/10"
                          onClick={async () => {
                            try {
                              await api.delete(`/sessions/${s.id}`)
                              toast.success('Session revoked')
                              setSessions(sessions.filter((x) => x.id !== s.id))
                            } catch {
                              toast.error('Failed to revoke session')
                            }
                          }}
                        >
                          Revoke
                        </Button>
                      </div>
                    ))}
                    {sessions.length === 0 && !loadingSessions && (
                      <p className="text-sm text-slate-500 text-center py-4">No active sessions found. Click Refresh to load.</p>
                    )}
                  </div>
                </CardContent>
              </Card>
            )}

            {activeTab === 'security' && (
              <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle>Two-Factor Authentication</CardTitle>
                  <CardDescription>Add an extra layer of security with an authenticator app</CardDescription>
                </CardHeader>
                <CardContent className="space-y-6">
                  <div className="flex items-center gap-3 p-4 rounded-lg border border-slate-800/50 bg-slate-800/30">
                    <div className={`w-3 h-3 rounded-full ${totpEnabled ? 'bg-emerald-400' : 'bg-slate-500'}`} />
                    <div>
                      <p className="text-sm font-medium text-white">
                        Two-factor authentication is {totpEnabled ? 'enabled' : 'disabled'}
                      </p>
                      <p className="text-xs text-slate-400 mt-0.5">
                        {totpEnabled ? 'Your account requires a TOTP code on login.' : 'Enable to require an authenticator code on every login.'}
                      </p>
                    </div>
                  </div>

                  {!totpEnabled && !totpSetupData && (
                    <Button
                      onClick={async () => {
                        setTotpLoading(true)
                        try {
                          const data = await totpApi.setup()
                          setTotpSetupData(data)
                          setTotpCode('')
                        } catch {
                          toast.error('Failed to start TOTP setup')
                        } finally {
                          setTotpLoading(false)
                        }
                      }}
                      disabled={totpLoading}
                      className="bg-blue-600 hover:bg-blue-500"
                    >
                      {totpLoading ? 'Generating...' : 'Enable Two-Factor Auth'}
                    </Button>
                  )}

                  {totpSetupData && (
                    <div className="space-y-4">
                      <p className="text-sm text-slate-300">
                        Scan the QR code with your authenticator app (Google Authenticator, Authy, 1Password, etc.), then enter the 6-digit code to confirm.
                      </p>
                      <div className="flex flex-col items-center gap-4">
                        {totpQrUrl && (
                          <img src={totpQrUrl} alt="TOTP QR code" className="rounded-lg border border-slate-700 p-2 bg-white" width={200} height={200} />
                        )}
                        <div className="text-center">
                          <p className="text-xs text-slate-500 mb-1">Manual entry key</p>
                          <code className="text-sm font-mono text-slate-300 bg-slate-800 px-3 py-1 rounded select-all">
                            {totpSetupData.secret}
                          </code>
                        </div>
                      </div>
                      <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-4 space-y-2">
                        <p className="text-sm font-medium text-amber-400">Save these backup codes</p>
                        <p className="text-xs text-slate-400">Each code can be used once if you lose access to your authenticator app. Store them somewhere safe.</p>
                        <div className="grid grid-cols-2 gap-2 mt-2">
                          {totpSetupData.backup_codes.map((code) => (
                            <code key={code} className="text-xs font-mono text-slate-300 bg-slate-900 px-2 py-1 rounded text-center select-all">
                              {code}
                            </code>
                          ))}
                        </div>
                      </div>
                      <div className="space-y-2">
                        <input
                          type="text"
                          inputMode="numeric"
                          pattern="[0-9]{6}"
                          maxLength={6}
                          value={totpCode}
                          onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ''))}
                          placeholder="Enter 6-digit code"
                          className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white text-center tracking-widest placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                        />
                        <div className="flex gap-2">
                          <Button
                            onClick={async () => {
                              setTotpLoading(true)
                              try {
                                await totpApi.enable(totpCode)
                                setTotpEnabled(true)
                                setTotpSetupData(null)
                                setTotpCode('')
                                toast.success('Two-factor authentication enabled')
                              } catch {
                                toast.error('Invalid code — try again')
                              } finally {
                                setTotpLoading(false)
                              }
                            }}
                            disabled={totpLoading || totpCode.length !== 6}
                            className="flex-1 bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50"
                          >
                            {totpLoading ? 'Verifying...' : 'Enable'}
                          </Button>
                          <Button
                            variant="ghost"
                            onClick={() => { setTotpSetupData(null); setTotpCode('') }}
                            className="text-slate-400"
                          >
                            Cancel
                          </Button>
                        </div>
                      </div>
                    </div>
                  )}

                  {totpEnabled && (
                    <div className="space-y-3 pt-2 border-t border-slate-800/50">
                      <p className="text-sm text-slate-400">
                        To disable two-factor authentication, enter your current authenticator code.
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
                          className="flex-1 bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white text-center tracking-widest placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-red-500/50"
                        />
                        <Button
                          onClick={async () => {
                            setTotpLoading(true)
                            try {
                              await totpApi.disable(totpCode)
                              setTotpEnabled(false)
                              setTotpCode('')
                              toast.success('Two-factor authentication disabled')
                            } catch {
                              toast.error('Invalid code')
                            } finally {
                              setTotpLoading(false)
                            }
                          }}
                          disabled={totpLoading || totpCode.length !== 6}
                          className="bg-red-600 hover:bg-red-500 disabled:opacity-50"
                        >
                          Disable
                        </Button>
                      </div>
                    </div>
                  )}
                </CardContent>
              </Card>
            )}
          </div>
        </div>
      </main>
    </div>
    </AuthGuard>
  )
}
