'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { neighborsApi, type UnmanagedNeighbor } from '@/lib/api'

export default function NeighborsPage() {
  const [rows, setRows] = useState<UnmanagedNeighbor[]>([])
  const [loading, setLoading] = useState(true)

  const load = async () => {
    setLoading(true)
    try { setRows(await neighborsApi.list()) }
    catch { toast.error('Failed to load') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-4xl space-y-6">
          <div className="flex items-center justify-between">
            <h1 className="text-2xl font-bold">Unmanaged neighbors</h1>
            <Button size="sm" variant="outline" onClick={load}>Refresh</Button>
          </div>
          <p className="text-sm text-slate-400">
            IPs your agents have observed via ARP / ip-neigh that don't match any registered device. Useful for finding rogue or unmanaged hardware on the LAN.
          </p>
          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">Loading…</CardContent></Card>
          ) : rows.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">No unmanaged neighbors observed yet. Agents report ARP every hour.</CardContent></Card>
          ) : (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3"><CardTitle className="text-base">{rows.length} unmanaged IP{rows.length === 1 ? '' : 's'}</CardTitle></CardHeader>
              <CardContent className="p-0">
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-slate-800/50 text-xs uppercase text-slate-400">
                        <th className="text-left px-4 py-2">IP</th>
                        <th className="text-left px-4 py-2">MAC</th>
                        <th className="text-left px-4 py-2">Hostname</th>
                        <th className="text-left px-4 py-2">Observers</th>
                        <th className="text-left px-4 py-2">Last seen</th>
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((r) => (
                        <tr key={r.ip} className="border-b border-slate-800/30 hover:bg-slate-800/20">
                          <td className="px-4 py-2 font-mono text-slate-200">{r.ip}</td>
                          <td className="px-4 py-2 font-mono text-xs text-slate-400">{r.mac || '—'}</td>
                          <td className="px-4 py-2 text-slate-400">{r.hostname || '—'}</td>
                          <td className="px-4 py-2 text-slate-400">{r.observers}</td>
                          <td className="px-4 py-2 text-slate-500 text-xs">{new Date(r.last_seen_at * 1000).toLocaleString()}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </CardContent>
            </Card>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
