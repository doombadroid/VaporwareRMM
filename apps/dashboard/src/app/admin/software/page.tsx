'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Button } from '@/components/ui/button'
import { Search } from 'lucide-react'
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
      toast.error('Failed to load')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  // Max device count for the bar visualisation.
  const max = rows.reduce((m, r) => Math.max(m, r.device_count), 0)

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Manage"
          title="Fleet software"
          description={`${rows.length} packages observed across reporting devices.`}
          separator={false}
        />

        <div className="flex items-center gap-2 mb-5 pb-4 border-b border-white/[0.04]">
          <div className="relative flex-1 max-w-sm">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-white/35" />
            <input
              type="text"
              placeholder="filter by name…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && load()}
              className="bg-white/[0.04] border border-white/[0.08] rounded-md pl-8 pr-3 py-1.5 text-[12px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2] w-full"
            />
          </div>
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            {loading ? 'Searching…' : 'Search'}
          </Button>
        </div>

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : rows.length === 0 ? (
          <EmptyState
            title="No inventory yet."
            hint="Agents report software every 6 hours."
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {rows.map((r, i) => {
              const pct = max > 0 ? (r.device_count / max) * 100 : 0
              return (
                <li
                  key={`${r.name}-${i}`}
                  className="px-4 py-2.5 flex items-center gap-3 hover:bg-white/[0.02] transition-colors relative"
                >
                  <span
                    className="absolute left-0 top-0 bottom-0 bg-cyan-500/[0.04] border-r border-cyan-500/10"
                    style={{ width: `${pct}%` }}
                    aria-hidden
                  />
                  <span className="relative text-[13px] text-white/85 truncate flex-1">{r.name}</span>
                  <span className="relative text-[11.5px] font-mono text-white/45 tabular-nums shrink-0">
                    {r.device_count} {r.device_count === 1 ? 'device' : 'devices'}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
