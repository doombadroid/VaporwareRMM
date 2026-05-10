'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'
import { networkApi, type NetworkTopology } from '@/lib/api'

export default function NetworkPage() {
  const [topology, setTopology] = useState<NetworkTopology | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    networkApi.getTopology()
      .then(setTopology)
      .catch(() => toast.error('Failed to load topology'))
      .finally(() => setLoading(false))
  }, [])

  const summary = topology
    ? { total: topology.total, installed: topology.tailscale_installed, connected: topology.tailscale_connected }
    : { total: 0, installed: 0, connected: 0 }

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
          <h1 className="text-2xl font-bold mb-6">Network Map</h1>

          <div className="grid grid-cols-3 gap-3 mb-6">
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-4">
                <p className="text-xs text-slate-400">Devices</p>
                <p className="text-2xl font-bold">{summary.total}</p>
              </CardContent>
            </Card>
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-4">
                <p className="text-xs text-slate-400">Tailscale installed</p>
                <p className="text-2xl font-bold">{summary.installed}</p>
              </CardContent>
            </Card>
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-4">
                <p className="text-xs text-slate-400">Tailscale connected</p>
                <p className="text-2xl font-bold text-emerald-300">{summary.connected}</p>
              </CardContent>
            </Card>
          </div>

          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">Loading…</CardContent>
            </Card>
          ) : !topology || topology.nodes.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">
                <p>No devices yet.</p>
                <p className="text-sm mt-2">Install agents to populate the network map.</p>
              </CardContent>
            </Card>
          ) : (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader>
                <CardTitle className="text-base">Nodes</CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                <div className="divide-y divide-slate-800/50">
                  {topology.nodes.map((n) => {
                    // Server is authoritative for online/offline (set by the
                    // periodic offline sweep using OFFLINE_THRESHOLD_SECONDS).
                    const onlineDot = n.status === 'online' ? 'bg-emerald-500' : 'bg-red-500'
                    const tsBadge = !n.tailscale_installed
                      ? { label: 'no tailscale', cls: 'bg-slate-500/15 border-slate-500/40 text-slate-300' }
                      : n.tailscale_connected
                      ? { label: 'connected', cls: 'bg-emerald-500/15 border-emerald-500/40 text-emerald-300' }
                      : { label: 'disconnected', cls: 'bg-amber-500/15 border-amber-500/40 text-amber-300' }
                    return (
                      <div key={n.id} className="flex items-center justify-between gap-4 px-4 py-3">
                        <div className="flex items-center gap-3 min-w-0">
                          <span className={`w-2 h-2 rounded-full shrink-0 ${onlineDot}`} />
                          <div className="min-w-0">
                            <p className="font-medium truncate">{n.hostname || n.id.slice(0, 8)}</p>
                            <p className="text-xs text-slate-500 truncate">
                              {n.ip_address || '—'}
                              {n.tailscale_ip && <> · ts {n.tailscale_ip}</>}
                              {n.tailscale_peers > 0 && <> · {n.tailscale_peers} peer{n.tailscale_peers === 1 ? '' : 's'}</>}
                            </p>
                          </div>
                        </div>
                        <span className={`px-2 py-0.5 rounded border text-xs whitespace-nowrap ${tsBadge.cls}`}>
                          {tsBadge.label}
                        </span>
                      </div>
                    )
                  })}
                </div>
              </CardContent>
            </Card>
          )}
        </main>
      </div>
    </AuthGuard>
  )
}
