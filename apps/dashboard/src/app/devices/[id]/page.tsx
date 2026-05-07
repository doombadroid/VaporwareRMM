'use client'

import { useEffect, useState } from 'react'
import { useParams, useRouter } from 'next/navigation'
import Link from 'next/link'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { devices as devicesApi, type Device } from '@/lib/api'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'
import { Monitor, ArrowLeft, Wifi, HardDrive, Cpu, Calendar, Clock, Tag } from 'lucide-react'
import { toast } from 'sonner'

export default function DeviceDetailPage() {
  const params = useParams()
  const router = useRouter()
  const deviceId = params.id as string
  const [device, setDevice] = useState<Device | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!deviceId) return
    devicesApi.getById(deviceId)
      .then(setDevice)
      .catch(() => toast.error('Failed to load device'))
      .finally(() => setLoading(false))
  }, [deviceId])

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

          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            <Card className="bg-slate-900/60 border-slate-800/50 backdrop-blur-sm">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-medium text-slate-300">System Info</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="flex items-center justify-between">
                  <span className="text-sm text-slate-400">IP Address</span>
                  <span className="text-sm text-white font-mono">{device.ip_address}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-sm text-slate-400">MAC Address</span>
                  <span className="text-sm text-white font-mono">{device.mac_address}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-sm text-slate-400">Agent Version</span>
                  <span className="text-sm text-white">{device.agent_version}</span>
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
                    <p className="text-sm text-white">{device.memory ? `${device.memory}%` : 'N/A'}</p>
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
                    <p className="text-sm text-white">{new Date(device.last_seen * 1000).toLocaleString()}</p>
                  </div>
                </div>
                <div className="flex items-center gap-3">
                  <Calendar className="w-4 h-4 text-blue-400" />
                  <div className="flex-1">
                    <p className="text-sm text-slate-400">Created</p>
                    <p className="text-sm text-white">{new Date(device.created_at * 1000).toLocaleString()}</p>
                  </div>
                </div>
                <div className="flex items-center gap-3">
                  <Clock className="w-4 h-4 text-yellow-400" />
                  <div className="flex-1">
                    <p className="text-sm text-slate-400">Uptime</p>
                    <p className="text-sm text-white">{Math.floor((Date.now() / 1000 - device.last_seen) / 60)} minutes ago</p>
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
        </main>
      </div>
    </AuthGuard>
  )
}
