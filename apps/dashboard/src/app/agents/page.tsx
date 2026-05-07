'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { toast } from 'sonner'
import { devices as devicesApi, type Device } from '@/lib/api'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'

export default function AgentsPage() {
  const [deviceList, setDeviceList] = useState<Device[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    devicesApi.getAll()
      .then(setDeviceList)
      .catch(() => toast.error('Failed to load agents'))
      .finally(() => setLoading(false))
  }, [])

  return (
    <AuthGuard>
      <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
        <header className="border-b border-slate-800/50 bg-slate-950/80 backdrop-blur-xl sticky top-0 z-50">
          <div className="container mx-auto px-6 py-3">
            <div className="flex items-center justify-between">
              <Link href="/" className="text-xl font-bold bg-gradient-to-r from-blue-400 to-purple-400 bg-clip-text text-transparent">
                vaporRMM
              </Link>
              <div className="flex items-center gap-3">
                <ThemeToggle />
                <Link href="/">
                  <Button variant="ghost" size="sm" className="text-slate-400 hover:text-white">← Dashboard</Button>
                </Link>
              </div>
            </div>
          </div>
        </header>
        <main className="container mx-auto px-6 py-8">
          <h1 className="text-2xl font-bold mb-6">Agents</h1>
          {loading ? (
            <p className="text-slate-400">Loading...</p>
          ) : (
            <div className="grid gap-4">
              {deviceList.map(d => (
                <Card key={d.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardHeader className="pb-2">
                    <CardTitle className="text-base">{d.hostname}</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <div className="flex items-center gap-4 text-sm text-slate-400">
                      <span className={`w-2 h-2 rounded-full ${d.status === 'online' ? 'bg-green-500' : 'bg-red-500'}`} />
                      <span>{d.status}</span>
                      <span>{d.os_name} {d.os_version}</span>
                      <span>{d.ip_address}</span>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </main>
      </div>
    </AuthGuard>
  )
}
