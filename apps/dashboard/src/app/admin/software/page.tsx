'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { inventoryApi, type FleetSoftwareRow } from '@/lib/api'

export default function FleetSoftwarePage() {
  const [rows, setRows] = useState<FleetSoftwareRow[]>([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState('')

  const load = async () => {
    setLoading(true)
    try {
      setRows(await inventoryApi.fleetSoftware(filter || undefined))
    } catch {
      toast.error('Failed to load fleet software')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-4xl space-y-6">
          <div className="flex items-center justify-between gap-3 flex-wrap">
            <h1 className="text-2xl font-bold">Fleet software</h1>
            <div className="flex items-center gap-2">
              <input
                type="text"
                placeholder="filter by name…"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && load()}
                className="bg-slate-800 border border-slate-700 rounded-md px-3 py-1.5 text-sm w-56"
              />
              <Button size="sm" variant="outline" onClick={load} disabled={loading}>
                {loading ? 'Loading…' : 'Search'}
              </Button>
            </div>
          </div>

          <Card className="bg-slate-900/60 border-slate-800/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-base">{rows.length} packages, top by device count</CardTitle>
            </CardHeader>
            <CardContent className="p-0">
              {loading ? (
                <p className="px-6 py-8 text-center text-slate-400">Loading…</p>
              ) : rows.length === 0 ? (
                <p className="px-6 py-8 text-center text-slate-400">
                  No inventory yet — agents report software every 6 hours.
                </p>
              ) : (
                <div className="divide-y divide-slate-800/50 max-h-[70vh] overflow-y-auto">
                  {rows.map((r, i) => (
                    <div key={`${r.name}-${i}`} className="px-4 py-2 flex items-center justify-between gap-3">
                      <span className="text-sm text-slate-200 truncate">{r.name}</span>
                      <span className="text-xs font-mono text-slate-400 shrink-0">{r.device_count} device{r.device_count === 1 ? '' : 's'}</span>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
