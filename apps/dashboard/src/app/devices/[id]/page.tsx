'use client'

import { useEffect, useState } from 'react'
import { useParams, useRouter } from 'next/navigation'
import Link from 'next/link'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  devices as devicesApi,
  deviceCommandsApi,
  deviceFilesApi,
  inventoryApi,
  type Device,
  type DeviceCommand,
  type FileTransfer,
  type SoftwareEntry,
  type HardwareInfo,
} from '@/lib/api'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'
import { Monitor, ArrowLeft, Wifi, HardDrive, Cpu, Calendar, Clock, Tag, Terminal, FileUp, Boxes } from 'lucide-react'
import { toast } from 'sonner'

const cmdStatusClass: Record<string, string> = {
  pending: 'bg-blue-500/15 border-blue-500/40 text-blue-300',
  running: 'bg-amber-500/15 border-amber-500/40 text-amber-300',
  completed: 'bg-emerald-500/15 border-emerald-500/40 text-emerald-300',
  failed: 'bg-red-500/15 border-red-500/40 text-red-300',
}

type DeviceTab = 'overview' | 'commands' | 'files' | 'software'

export default function DeviceDetailPage() {
  const params = useParams()
  const router = useRouter()
  const deviceId = params.id as string
  const [device, setDevice] = useState<Device | null>(null)
  const [loading, setLoading] = useState(true)
  const [tab, setTab] = useState<DeviceTab>('overview')
  const [commands, setCommands] = useState<DeviceCommand[]>([])
  const [commandsLoading, setCommandsLoading] = useState(false)
  const [files, setFiles] = useState<FileTransfer[]>([])
  const [filesLoading, setFilesLoading] = useState(false)
  const [software, setSoftware] = useState<SoftwareEntry[]>([])
  const [softwareLoading, setSoftwareLoading] = useState(false)
  const [hardware, setHardware] = useState<HardwareInfo | null>(null)
  const [softwareFilter, setSoftwareFilter] = useState('')

  useEffect(() => {
    if (!deviceId) return
    devicesApi.getById(deviceId)
      .then(setDevice)
      .catch(() => toast.error('Failed to load device'))
      .finally(() => setLoading(false))
  }, [deviceId])

  useEffect(() => {
    if (!deviceId || tab !== 'commands') return
    setCommandsLoading(true)
    deviceCommandsApi.list(deviceId, 100)
      .then(setCommands)
      .catch(() => toast.error('Failed to load commands'))
      .finally(() => setCommandsLoading(false))
  }, [deviceId, tab])

  useEffect(() => {
    if (!deviceId || tab !== 'files') return
    setFilesLoading(true)
    deviceFilesApi.list(deviceId)
      .then(setFiles)
      .catch(() => toast.error('Failed to load file transfers'))
      .finally(() => setFilesLoading(false))
  }, [deviceId, tab])

  useEffect(() => {
    if (!deviceId || tab !== 'software') return
    setSoftwareLoading(true)
    Promise.all([
      inventoryApi.deviceSoftware(deviceId),
      inventoryApi.deviceHardware(deviceId),
    ])
      .then(([sw, hw]) => {
        setSoftware(sw)
        setHardware(hw?.hardware || null)
      })
      .catch(() => toast.error('Failed to load inventory'))
      .finally(() => setSoftwareLoading(false))
  }, [deviceId, tab])

  if (loading) {
    return (
      <AuthGuard>
        <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white flex items-center justify-center">
          <div className="text-slate-400">Loading...</div>
        </div>
      </AuthGuard>
    )
  }

  if (!device) {
    return (
      <AuthGuard>
        <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white flex items-center justify-center">
          <div className="text-center">
            <p className="text-slate-400 mb-4">Device not found</p>
            <Button onClick={() => router.push('/')}>
              <ArrowLeft className="w-4 h-4 mr-2" />
              Back to Dashboard
            </Button>
          </div>
        </div>
      </AuthGuard>
    )
  }

  return (
    <AuthGuard>
      <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
        <header className="border-b border-slate-800/50 bg-slate-950/80 backdrop-blur-xl sticky top-0 z-50">
          <div className="container mx-auto px-6 py-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-4">
                <Link href="/">
                  <Button variant="ghost" size="sm" className="text-slate-400 hover:text-white">
                    <ArrowLeft className="w-4 h-4 mr-1" />
                    Dashboard
                  </Button>
                </Link>
              </div>
              <ThemeToggle />
            </div>
          </div>
        </header>

        <main className="container mx-auto px-6 py-8">
          <div className="mb-6">
            <div className="flex items-center gap-3 mb-2">
              <Monitor className="w-6 h-6 text-blue-400" />
              <h1 className="text-2xl font-bold">{device.hostname}</h1>
              <span className={`px-2 py-0.5 rounded-full text-xs font-medium ${
                device.status === 'online'
                  ? 'bg-green-500/10 text-green-400 border border-green-500/20'
                  : 'bg-red-500/10 text-red-400 border border-red-500/20'
              }`}>
                {device.status}
              </span>
            </div>
            <p className="text-slate-400 text-sm">{device.os_name} {device.os_version}</p>
          </div>

          <div className="flex gap-1 mb-4 border-b border-slate-800/50">
            {([
              { id: 'overview' as const, label: 'Overview', Icon: Monitor },
              { id: 'commands' as const, label: 'Commands', Icon: Terminal },
              { id: 'files' as const, label: 'Files', Icon: FileUp },
              { id: 'software' as const, label: 'Software', Icon: Boxes },
            ]).map(({ id, label, Icon }) => (
              <button
                key={id}
                onClick={() => setTab(id)}
                className={`flex items-center gap-2 px-4 py-2 text-sm border-b-2 transition-colors ${
                  tab === id
                    ? 'border-blue-400 text-blue-400'
                    : 'border-transparent text-slate-400 hover:text-white'
                }`}
              >
                <Icon className="w-4 h-4" />
                {label}
              </button>
            ))}
          </div>

          {tab === 'overview' && (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-medium text-slate-300">System Info</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="flex items-center justify-between">
                  <span className="text-sm text-slate-400">IP Address</span>
                  <span className="text-sm text-white font-mono">{device.ip_address || 'N/A'}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-sm text-slate-400">MAC Address</span>
                  <span className="text-sm text-white font-mono">{device.mac_address || 'N/A'}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-sm text-slate-400">Agent Version</span>
                  <span className="text-sm text-white">{device.agent_version || 'N/A'}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-sm text-slate-400">Device ID</span>
                  <span className="text-sm text-white font-mono text-xs">{device.id}</span>
                </div>
              </CardContent>
            </Card>

            <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-medium text-slate-300">Resources</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="flex items-center gap-3">
                  <Cpu className="w-4 h-4 text-blue-400" />
                  <div className="flex-1">
                    <p className="text-sm text-slate-400">CPU</p>
                    <p className="text-sm text-white">{device.cpu || 'N/A'}</p>
                  </div>
                </div>
                <div className="flex items-center gap-3">
                  <HardDrive className="w-4 h-4 text-purple-400" />
                  <div className="flex-1">
                    <p className="text-sm text-slate-400">Memory</p>
                    <p className="text-sm text-white">
                      {device.memory ? `${(device.memory / 1024 / 1024 / 1024).toFixed(1)} GB` : 'N/A'}
                    </p>
                  </div>
                </div>
                <div className="flex items-center gap-3">
                  <HardDrive className="w-4 h-4 text-cyan-400" />
                  <div className="flex-1">
                    <p className="text-sm text-slate-400">Disk</p>
                    <p className="text-sm text-white">{device.disk_size ? `${Math.round(device.disk_size / 1024 / 1024 / 1024)} GB` : 'N/A'}</p>
                  </div>
                </div>
              </CardContent>
            </Card>

            <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-medium text-slate-300">Status</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="flex items-center gap-3">
                  <Wifi className="w-4 h-4 text-green-400" />
                  <div className="flex-1">
                    <p className="text-sm text-slate-400">Last Seen</p>
                    <p className="text-sm text-white">
                      {device.last_seen > 0 ? new Date(device.last_seen * 1000).toLocaleString() : 'Never'}
                    </p>
                  </div>
                </div>
                <div className="flex items-center gap-3">
                  <Calendar className="w-4 h-4 text-blue-400" />
                  <div className="flex-1">
                    <p className="text-sm text-slate-400">Registered</p>
                    <p className="text-sm text-white">{new Date(device.created_at * 1000).toLocaleString()}</p>
                  </div>
                </div>
                <div className="flex items-center gap-3">
                  <Clock className="w-4 h-4 text-yellow-400" />
                  <div className="flex-1">
                    <p className="text-sm text-slate-400">Last Heartbeat</p>
                    <p className="text-sm text-white">
                      {device.last_seen > 0
                        ? `${Math.max(0, Math.floor((Date.now() / 1000 - device.last_seen) / 60))} min ago`
                        : 'Never'}
                    </p>
                  </div>
                </div>
              </CardContent>
            </Card>

            {device.tags && device.tags.length > 0 && (
              <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm md:col-span-2 lg:col-span-3">
                <CardHeader className="pb-3">
                  <CardTitle className="text-sm font-medium text-slate-300">Tags</CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="flex flex-wrap gap-2">
                    {device.tags.map(tag => (
                      <span key={tag} className="px-2 py-1 rounded-lg text-xs bg-slate-700/50 text-slate-300 flex items-center gap-1">
                        <Tag className="w-3 h-3" />
                        {tag}
                      </span>
                    ))}
                  </div>
                </CardContent>
              </Card>
            )}
          </div>
          )}

          {tab === 'commands' && (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3 flex flex-row items-center justify-between">
                <CardTitle className="text-sm font-medium text-slate-300">Command history</CardTitle>
                <span className="text-xs text-slate-500">last 100</span>
              </CardHeader>
              <CardContent className="p-0">
                {commandsLoading ? (
                  <p className="px-6 py-8 text-center text-slate-400">Loading…</p>
                ) : commands.length === 0 ? (
                  <p className="px-6 py-8 text-center text-slate-400">No commands sent to this device.</p>
                ) : (
                  <div className="divide-y divide-slate-800/50">
                    {commands.map((c) => (
                      <div key={c.id} className="px-4 py-3">
                        <div className="flex items-center gap-2 flex-wrap">
                          <span className="font-mono text-xs text-slate-300">{c.type}</span>
                          <span className={`px-2 py-0.5 rounded border text-xs ${cmdStatusClass[c.status] ?? cmdStatusClass.pending}`}>
                            {c.status}
                          </span>
                          <span className="text-xs text-slate-500">{new Date(c.created_at * 1000).toLocaleString()}</span>
                          {c.finished_at && (
                            <span className="text-xs text-slate-500">
                              · {Math.max(0, c.finished_at - c.created_at)}s
                            </span>
                          )}
                        </div>
                        {c.payload && (
                          <pre className="mt-1 text-xs font-mono text-slate-400 whitespace-pre-wrap break-all">
                            {c.payload.length > 500 ? c.payload.slice(0, 500) + '…' : c.payload}
                          </pre>
                        )}
                        {c.output && (
                          <details className="mt-2">
                            <summary className="text-xs text-slate-500 cursor-pointer">output ({c.output.length} chars)</summary>
                            <pre className="mt-1 text-xs font-mono text-slate-400 whitespace-pre-wrap break-all max-h-64 overflow-auto bg-slate-900/40 rounded p-2">
                              {c.output}
                            </pre>
                          </details>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          )}

          {tab === 'software' && (
            <div className="space-y-4">
              {hardware && (
                <Card className="bg-slate-900/60 border-slate-800/50">
                  <CardHeader className="pb-3">
                    <CardTitle className="text-sm font-medium text-slate-300">Hardware</CardTitle>
                  </CardHeader>
                  <CardContent className="grid grid-cols-2 md:grid-cols-3 gap-4 text-sm">
                    <div><p className="text-xs text-slate-500">CPU</p><p>{hardware.cpu_model || 'N/A'}</p></div>
                    <div><p className="text-xs text-slate-500">Cores</p><p>{hardware.cpu_cores || 'N/A'}</p></div>
                    <div><p className="text-xs text-slate-500">RAM</p><p>{hardware.ram_bytes ? `${(hardware.ram_bytes / 1024 / 1024 / 1024).toFixed(1)} GB` : 'N/A'}</p></div>
                    <div><p className="text-xs text-slate-500">Disk</p><p>{hardware.disk_total_bytes ? `${(hardware.disk_total_bytes / 1024 / 1024 / 1024).toFixed(0)} GB` : 'N/A'}</p></div>
                    <div><p className="text-xs text-slate-500">Platform</p><p>{hardware.platform || 'N/A'} {hardware.platform_version}</p></div>
                    <div><p className="text-xs text-slate-500">Kernel</p><p className="font-mono text-xs">{hardware.kernel_version || 'N/A'}</p></div>
                  </CardContent>
                </Card>
              )}
              <Card className="bg-slate-900/60 border-slate-800/50">
                <CardHeader className="pb-3 flex flex-row items-center justify-between gap-2 flex-wrap">
                  <CardTitle className="text-sm font-medium text-slate-300">
                    Installed software ({software.length})
                  </CardTitle>
                  <input
                    type="text"
                    placeholder="filter…"
                    value={softwareFilter}
                    onChange={(e) => setSoftwareFilter(e.target.value)}
                    className="bg-slate-800 border border-slate-700 rounded-md px-2 py-1 text-xs"
                  />
                </CardHeader>
                <CardContent className="p-0">
                  {softwareLoading ? (
                    <p className="px-6 py-8 text-center text-slate-400">Loading…</p>
                  ) : software.length === 0 ? (
                    <p className="px-6 py-8 text-center text-slate-400">
                      No inventory yet. Agent reports software every 6 hours; first snapshot ~1 minute after agent start.
                    </p>
                  ) : (
                    <div className="divide-y divide-slate-800/50 max-h-[60vh] overflow-y-auto">
                      {software
                        .filter((s) => !softwareFilter || s.name.toLowerCase().includes(softwareFilter.toLowerCase()))
                        .map((s, i) => (
                          <div key={`${s.name}-${i}`} className="px-4 py-2 flex items-center justify-between gap-3">
                            <div className="min-w-0">
                              <p className="text-sm text-slate-200 truncate">{s.name}</p>
                              {s.vendor && <p className="text-xs text-slate-500 truncate">{s.vendor}</p>}
                            </div>
                            <span className="text-xs font-mono text-slate-400 shrink-0">{s.version || '—'}</span>
                          </div>
                        ))}
                    </div>
                  )}
                </CardContent>
              </Card>
            </div>
          )}

          {tab === 'files' && (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-medium text-slate-300">File transfers</CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                {filesLoading ? (
                  <p className="px-6 py-8 text-center text-slate-400">Loading…</p>
                ) : files.length === 0 ? (
                  <p className="px-6 py-8 text-center text-slate-400">No file transfers for this device.</p>
                ) : (
                  <div className="divide-y divide-slate-800/50">
                    {files.map((f) => (
                      <div key={f.id} className="px-4 py-3 flex items-center justify-between gap-3">
                        <div className="min-w-0 flex-1">
                          <p className="text-sm text-slate-200 truncate font-mono">{f.file_name}</p>
                          <p className="text-xs text-slate-500 truncate">
                            {f.type} · {f.file_path}
                          </p>
                          <p className="text-xs text-slate-600">
                            {new Date(f.created_at * 1000).toLocaleString()}
                            {f.completed_at && <> · completed {new Date(f.completed_at * 1000).toLocaleString()}</>}
                          </p>
                        </div>
                        <div className="text-right shrink-0">
                          <span className={`px-2 py-0.5 rounded border text-xs ${cmdStatusClass[f.status] ?? cmdStatusClass.pending}`}>
                            {f.status}
                          </span>
                          {f.progress > 0 && f.progress < 100 && (
                            <p className="text-xs text-slate-500 mt-1">{f.progress}%</p>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          )}
        </main>
      </div>
    </AuthGuard>
  )
}
