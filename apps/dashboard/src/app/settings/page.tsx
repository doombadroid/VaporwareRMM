'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { toast } from 'sonner'
import { branding as brandingApi, devices as devicesApi, default as api } from '@/lib/api'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'
import { Settings, Palette, Link as LinkIcon, Bot, CheckCircle, Shield } from 'lucide-react'

interface BrandingConfig {
  app_name: string
  icon_url: string
  company_name: string
  primary_color: string
}

interface TailscaleSettings {
  enabled: boolean
  auth_key: string
  exit_node: boolean
  tags: string[]
}

type SettingsTab = 'general' | 'branding' | 'tailscale' | 'agents' | 'sessions'

export default function SettingsPage() {
  const [activeTab, setActiveTab] = useState<SettingsTab>('general')
  const [branding, setBranding] = useState<BrandingConfig>({
    app_name: 'vaporRMM',
    icon_url: '',
    company_name: 'Vaporware RMM',
    primary_color: '#3b82f6',
  })
  const [tailscale, setTailscale] = useState<TailscaleSettings>({
    enabled: false,
    auth_key: '',
    exit_node: false,
    tags: [],
  })
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [newTag, setNewTag] = useState('')
  const [generatedAuthKey, setGeneratedAuthKey] = useState('')
  const [generatingKey, setGeneratingKey] = useState(false)
  const [heartbeatInterval, setHeartbeatInterval] = useState(30)
  const [offlineThreshold, setOfflineThreshold] = useState(300)
  const [commandTimeout, setCommandTimeout] = useState(30)
  const [sessions, setSessions] = useState<any[]>([])
  const [loadingSessions, setLoadingSessions] = useState(false)

  useEffect(() => {
    const h = localStorage.getItem('settings_heartbeat')
    const o = localStorage.getItem('settings_offline')
    const c = localStorage.getItem('settings_timeout')
    if (h) setHeartbeatInterval(parseInt(h))
    if (o) setOfflineThreshold(parseInt(o))
    if (c) setCommandTimeout(parseInt(c))
  }, [])

  useEffect(() => {
    loadSettings()
  }, [])

  const loadSettings = async () => {
    try {
      const brandingData = await brandingApi.get()
      setBranding(brandingData)
    } catch (err) {
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
    } catch (err) {
      toast.error('Failed to save branding')
    } finally {
      setSaving(false)
    }
  }

  const handleGenerateAuthKey = async () => {
    setGeneratingKey(true)
    try {
      // Use a dummy device ID for generating a general auth key
      const response = await devicesApi.generateTailscaleAuthKey('server', {
        reusable: true,
        ephemeral: true,
        tags: tailscale.tags.length > 0 ? tailscale.tags : undefined,
      })
      setGeneratedAuthKey(response.auth_key)
    } catch (err) {
      toast.error('Failed to generate auth key')
    } finally {
      setGeneratingKey(false)
    }
  }

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text)
  }

  const addTag = () => {
    if (newTag && !tailscale.tags.includes(newTag)) {
      setTailscale({ ...tailscale, tags: [...tailscale.tags, newTag] })
      setNewTag('')
    }
  }

  const removeTag = (tag: string) => {
    setTailscale({ ...tailscale, tags: tailscale.tags.filter(t => t !== tag) })
  }

  const tabs: { id: SettingsTab; label: string; icon: React.ElementType }[] = [
    { id: 'general', label: 'General', icon: Settings },
    { id: 'branding', label: 'Branding', icon: Palette },
    { id: 'tailscale', label: 'Tailscale', icon: LinkIcon },
    { id: 'agents', label: 'Agents', icon: Bot },
    { id: 'sessions', label: 'Sessions', icon: Shield },
  ]

  return (
    <AuthGuard>
    <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
      {/* Header */}
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
                <Link href="/" className="px-3 py-2 text-sm text-slate-400 hover:text-white hover:bg-slate-800/50 rounded-lg transition-colors">
                  Dashboard
                </Link>
                <Link href="/agents" className="px-3 py-2 text-sm text-slate-400 hover:text-white hover:bg-slate-800/50 rounded-lg transition-colors">
                  Agents
                </Link>
                <Link href="/tickets" className="px-3 py-2 text-sm text-slate-400 hover:text-white hover:bg-slate-800/50 rounded-lg transition-colors">
                  Tickets
                </Link>
                <Link href="/settings" className="px-3 py-2 text-sm font-medium text-blue-400 bg-blue-500/10 rounded-lg">
                  Settings
                </Link>
              </nav>
            </div>
            
            <div className="flex items-center gap-3">
              <ThemeToggle />
              <Link href="/">
                <Button variant="ghost" size="sm" className="text-slate-400 hover:text-white">
                  ← Back to Dashboard
                </Button>
              </Link>
            </div>
          </div>
        </div>
      </header>

      {/* Main Content */}
      <main className="container mx-auto px-6 py-8">
        <div className="flex gap-6">
          {/* Sidebar */}
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

          {/* Content Area */}
          <div className="flex-1 max-w-3xl">
            {/* General Settings */}
            {activeTab === 'general' && (
              <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle>General Settings</CardTitle>
                  <CardDescription>Configure basic application settings</CardDescription>
                </CardHeader>
                <CardContent className="space-y-6">
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Server URL</label>
                    <input 
                      type="text" 
                      defaultValue={typeof window !== 'undefined' ? window.location.origin : ''}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                      readOnly
                    />
                    <p className="text-xs text-slate-500 mt-1">This is the URL agents will connect to</p>
                  </div>
                  
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Heartbeat Interval</label>
                    <select 
                      value={heartbeatInterval}
                      onChange={(e) => setHeartbeatInterval(parseInt(e.target.value))}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                    >
                      <option value={15}>15 seconds</option>
                      <option value={30}>30 seconds</option>
                      <option value={60}>60 seconds</option>
                      <option value={120}>2 minutes</option>
                      <option value={300}>5 minutes</option>
                    </select>
                    <p className="text-xs text-slate-500 mt-1">How often agents report their status</p>
                  </div>

                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Offline Threshold</label>
                    <select 
                      value={offlineThreshold}
                      onChange={(e) => setOfflineThreshold(parseInt(e.target.value))}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                    >
                      <option value={60}>1 minute</option>
                      <option value={120}>2 minutes</option>
                      <option value={300}>5 minutes</option>
                      <option value={600}>10 minutes</option>
                      <option value={900}>15 minutes</option>
                    </select>
                    <p className="text-xs text-slate-500 mt-1">Time before a device is marked as offline</p>
                  </div>
                  <div className="flex items-center justify-end gap-3 pt-4 border-t border-slate-800/30">
                    <Button 
                      onClick={() => {
                        localStorage.setItem('settings_heartbeat', String(heartbeatInterval))
                        localStorage.setItem('settings_offline', String(offlineThreshold))
                        localStorage.setItem('settings_timeout', String(commandTimeout))
                        toast.success('Settings saved')
                      }}
                    >
                      Save Settings
                    </Button>
                  </div>
                </CardContent>
              </Card>
            )}

            {/* Branding Settings */}
            {activeTab === 'branding' && (
              <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle>Branding Settings</CardTitle>
                  <CardDescription>Customize the appearance of your RMM platform</CardDescription>
                </CardHeader>
                <CardContent className="space-y-6">
                  {/* App Name */}
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">App Name</label>
                    <input 
                      type="text" 
                      value={branding.app_name}
                      onChange={(e) => setBranding({...branding, app_name: e.target.value})}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                      placeholder="vaporRMM"
                    />
                  </div>

                  {/* Company Name */}
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Company Name</label>
                    <input 
                      type="text" 
                      value={branding.company_name}
                      onChange={(e) => setBranding({...branding, company_name: e.target.value})}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                      placeholder="Your Company"
                    />
                  </div>

                  {/* Icon URL */}
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Logo URL</label>
                    <input 
                      type="text" 
                      value={branding.icon_url}
                      onChange={(e) => setBranding({...branding, icon_url: e.target.value})}
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

                  {/* Primary Color */}
                  <div>
                    <label className="block text-sm font-medium text-slate-300 mb-2">Primary Color</label>
                    <div className="flex items-center gap-2">
                      <input 
                        type="color" 
                        value={branding.primary_color}
                        onChange={(e) => setBranding({...branding, primary_color: e.target.value})}
                        className="w-10 h-10 rounded-lg cursor-pointer bg-transparent"
                      />
                      <input 
                        type="text" 
                        value={branding.primary_color}
                        onChange={(e) => setBranding({...branding, primary_color: e.target.value})}
                        className="flex-1 bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white font-mono focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                        placeholder="#3b82f6"
                      />
                    </div>
                  </div>

                  {/* Preview */}
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

                  {/* Save Button */}
                  <div className="flex items-center justify-end gap-3 pt-4 border-t border-slate-800/30">
                    {saved && (
                      <span className="text-sm text-green-400 flex items-center gap-1"><CheckCircle className="w-4 h-4" /> Settings saved successfully</span>
                    )}
                    <Button 
                      onClick={handleSaveBranding} 
                      disabled={saving}
                      style={{ backgroundColor: branding.primary_color }}
                    >
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

            {/* Tailscale Settings */}
            {activeTab === 'tailscale' && (
              <div className="space-y-6">
                {/* Tailscale Configuration */}
                <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                  <CardHeader>
                    <CardTitle>Tailscale Configuration</CardTitle>
                    <CardDescription>Configure Tailscale network settings for agent deployment</CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-6">
                    {/* Enable Tailscale */}
                    <div className="flex items-center justify-between p-4 bg-slate-800/30 rounded-xl">
                      <div>
                        <p className="text-sm font-medium text-white">Enable Tailscale Integration</p>
                        <p className="text-xs text-slate-400 mt-1">Allow agents to connect via Tailscale network</p>
                      </div>
                      <label className="relative inline-flex items-center cursor-pointer">
                        <input 
                          type="checkbox" 
                          checked={tailscale.enabled}
                          onChange={(e) => setTailscale({...tailscale, enabled: e.target.checked})}
                          className="sr-only peer"
                        />
                        <div className="w-11 h-6 bg-slate-700 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-blue-600"></div>
                      </label>
                    </div>

                    {/* Auth Key Generation */}
                    <div className="p-4 bg-slate-800/30 rounded-xl">
                      <h3 className="text-sm font-medium text-white mb-3">Auth Key Management</h3>
                      <p className="text-xs text-slate-400 mb-4">Generate pre-authenticated keys for automatic device enrollment</p>
                      
                      {generatedAuthKey ? (
                        <div className="p-3 bg-green-500/10 border border-green-500/30 rounded-lg">
                          <p className="text-xs text-green-400 mb-2">Auth Key Generated</p>
                          <div className="flex items-center gap-2">
                            <code className="flex-1 bg-slate-900/50 px-3 py-2 rounded text-xs font-mono text-slate-300 break-all">
                              {generatedAuthKey}
                            </code>
                            <Button 
                              size="sm" 
                              variant="ghost" 
                              className="text-slate-400 hover:text-white flex-shrink-0"
                              onClick={() => copyToClipboard(generatedAuthKey)}
                            >
                              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                                <rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>
                              </svg>
                            </Button>
                          </div>
                          <Button 
                            size="sm" 
                            variant="ghost" 
                            className="text-xs text-slate-400 hover:text-white mt-2"
                            onClick={() => setGeneratedAuthKey('')}
                          >
                            Generate New Key
                          </Button>
                        </div>
                      ) : (
                        <Button 
                          onClick={handleGenerateAuthKey}
                          disabled={generatingKey}
                          className="w-full bg-blue-600 hover:bg-blue-700 text-white"
                        >
                          {generatingKey ? (
                            <>
                              <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin mr-2" />
                              Generating...
                            </>
                          ) : (
                            <>
                              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="mr-2">
                                <rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
                              </svg>
                              Generate Auth Key
                            </>
                          )}
                        </Button>
                      )}
                    </div>

                    {/* Tags */}
                    <div className="p-4 bg-slate-800/30 rounded-xl">
                      <h3 className="text-sm font-medium text-white mb-3">Device Tags</h3>
                      <p className="text-xs text-slate-400 mb-3">Assign Tailscale ACL tags to enrolled devices</p>
                      
                      <div className="flex flex-wrap gap-2 mb-3">
                        {tailscale.tags.map(tag => (
                          <span key={tag} className="inline-flex items-center gap-1 px-2 py-1 bg-blue-500/10 border border-blue-500/20 rounded-full text-xs text-blue-400">
                            {tag}
                            <button onClick={() => removeTag(tag)} className="hover:text-white">×</button>
                          </span>
                        ))}
                      </div>
                      
                      <div className="flex gap-2">
                        <input 
                          type="text" 
                          value={newTag}
                          onChange={(e) => setNewTag(e.target.value)}
                          onKeyDown={(e) => e.key === 'Enter' && addTag()}
                          placeholder="tag:production"
                          className="flex-1 bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white placeholder:text-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                        />
                        <Button size="sm" onClick={addTag} variant="outline" className="border-slate-600">Add</Button>
                      </div>
                    </div>

                    {/* Exit Node */}
                    <div className="flex items-center justify-between p-4 bg-slate-800/30 rounded-xl">
                      <div>
                        <p className="text-sm font-medium text-white">Advertise as Exit Node</p>
                        <p className="text-xs text-slate-400 mt-1">Allow agents to be used as exit nodes</p>
                      </div>
                      <label className="relative inline-flex items-center cursor-pointer">
                        <input 
                          type="checkbox" 
                          checked={tailscale.exit_node}
                          onChange={(e) => setTailscale({...tailscale, exit_node: e.target.checked})}
                          className="sr-only peer"
                        />
                        <div className="w-11 h-6 bg-slate-700 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-blue-600"></div>
                      </label>
                    </div>
                  </CardContent>
                </Card>

                {/* Tailscale Network Status */}
                <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                  <CardHeader>
                    <CardTitle>Network Status</CardTitle>
                    <CardDescription>Current Tailscale network information</CardDescription>
                  </CardHeader>
                  <CardContent>
                    <div className="space-y-3">
                      <div className="flex items-center justify-between p-3 bg-slate-800/30 rounded-lg">
                        <span className="text-sm text-slate-400">Server Status</span>
                        <span className="text-sm text-green-400">Connected</span>
                      </div>
                      <div className="flex items-center justify-between p-3 bg-slate-800/30 rounded-lg">
                        <span className="text-sm text-slate-400">Tailnet</span>
                        <span className="text-sm font-mono text-slate-300">example.com</span>
                      </div>
                      <div className="flex items-center justify-between p-3 bg-slate-800/30 rounded-lg">
                        <span className="text-sm text-slate-400">Connected Devices</span>
                        <span className="text-sm font-mono text-slate-300">0</span>
                      </div>
                    </div>
                    <p className="text-xs text-slate-500 mt-4">
                      Note: Server must have Tailscale CLI installed and authenticated to manage the network
                    </p>
                  </CardContent>
                </Card>

                {/* Install Command */}
                <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                  <CardHeader>
                    <CardTitle>Agent Install Command</CardTitle>
                    <CardDescription>Use this command to install Tailscale on agents</CardDescription>
                  </CardHeader>
                  <CardContent>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 bg-slate-900/50 px-3 py-2 rounded-lg text-xs font-mono text-slate-300">
                        vaporrmm install-tailscale --authkey={generatedAuthKey || 'YOUR_KEY'} {tailscale.exit_node ? '--exit-node' : ''}
                      </code>
                      <Button 
                        size="sm" 
                        variant="ghost" 
                        className="text-slate-400 hover:text-white"
                        onClick={() => copyToClipboard(`vaporrmm install-tailscale --authkey=${generatedAuthKey || 'YOUR_KEY'} ${tailscale.exit_node ? '--exit-node' : ''}`)}
                      >
                        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>
                        </svg>
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              </div>
            )}

            {/* Agent Settings */}
            {activeTab === 'agents' && (
              <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
                <CardHeader>
                  <CardTitle>Agent Configuration</CardTitle>
                  <CardDescription>Manage agent deployment and settings</CardDescription>
                </CardHeader>
                <CardContent className="space-y-6">
                  {/* Agent Install Script */}
                  <div className="p-4 bg-slate-800/30 rounded-xl">
                    <h3 className="text-sm font-medium text-white mb-3">Quick Install</h3>
                    <p className="text-xs text-slate-400 mb-3">One-line install command for new agents</p>
                    <div className="flex items-center gap-2">
                      <code className="flex-1 bg-slate-900/50 px-3 py-2 rounded-lg text-xs font-mono text-slate-300">
                        curl -fsSL {typeof window !== 'undefined' ? window.location.origin : ''}/api/agent/install.sh | sudo bash -s -- --server {typeof window !== 'undefined' ? window.location.origin : ''}
                      </code>
                      <Button 
                        size="sm" 
                        variant="ghost" 
                        className="text-slate-400 hover:text-white"
                        onClick={() => copyToClipboard(`curl -fsSL ${typeof window !== 'undefined' ? window.location.origin : ''}/api/agent/install.sh | sudo bash -s -- --server ${typeof window !== 'undefined' ? window.location.origin : ''}`)}
                      >
                        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>
                        </svg>
                      </Button>
                    </div>
                  </div>

                  {/* Sunshine Settings */}
                  <div className="p-4 bg-slate-800/30 rounded-xl">
                    <h3 className="text-sm font-medium text-white mb-3">Sunshine (Remote Desktop)</h3>
                    <p className="text-xs text-slate-400 mb-3">Configure default Sunshine settings for new agents</p>
                    <div className="space-y-3">
                      <div>
                        <label className="block text-xs text-slate-400 mb-1">Default Port</label>
                        <input 
                          type="number" 
                          defaultValue="47990"
                          className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                        />
                      </div>
                      <div className="flex items-center gap-2">
                        <input type="checkbox" id="auto-start-sunshine" defaultChecked className="rounded border-slate-600 bg-slate-800" />
                        <label htmlFor="auto-start-sunshine" className="text-sm text-slate-300">Auto-start Sunshine on install</label>
                      </div>
                    </div>
                  </div>

                  {/* Agent Timeout */}
                  <div className="p-4 bg-slate-800/30 rounded-xl">
                    <h3 className="text-sm font-medium text-white mb-3">Command Timeout</h3>
                    <p className="text-xs text-slate-400 mb-3">Maximum time to wait for agent command responses</p>
                    <select 
                      value={commandTimeout}
                      onChange={(e) => setCommandTimeout(parseInt(e.target.value))}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-500/50"
                    >
                      <option value={10}>10 seconds</option>
                      <option value={30}>30 seconds</option>
                      <option value={60}>60 seconds</option>
                      <option value={120}>2 minutes</option>
                    </select>
                  </div>
                </CardContent>
              </Card>
            )}

            {/* Sessions */}
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
                          const res = await api.get('/sessions')
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
                          const res = await api.get('/sessions')
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
                    {sessions.map((s: any) => (
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
                              setSessions(sessions.filter((x: any) => x.id !== s.id))
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
          </div>
        </div>
      </main>
    </div>
    </AuthGuard>
  )
}