'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Pill, statusTone, StatusDot, Code } from '@/components/ui/status'
import { FilterChip } from '@/components/ui/data-table'
import TopologyGraph from '@/components/dashboard/TopologyGraph'
import { networkApi, type NetworkTopology } from '@/lib/api'

type NetworkView = 'graph' | 'list'

export default function NetworkPage() {
  const [topology, setTopology] = useState<NetworkTopology | null>(null)
  const [loading, setLoading] = useState(true)
  const [view, setView] = useState<NetworkView>('graph')

  useEffect(() => {
    networkApi
      .getTopology()
      .then(setTopology)
      .catch(() => toast.error('Failed to load topology'))
      .finally(() => setLoading(false))
  }, [])

  const summary = topology
    ? { total: topology.total, installed: topology.tailscale_installed, connected: topology.tailscale_connected }
    : { total: 0, installed: 0, connected: 0 }

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Network"
          title="Network map"
          description="Tailscale tailnet plus device connectivity, observed by the server."
          actions={
            <div className="flex items-center gap-1">
              <FilterChip label="Graph" active={view === 'graph'} onClick={() => setView('graph')} />
              <FilterChip label="List" active={view === 'list'} onClick={() => setView('list')} />
            </div>
          }
        />

        <div className="grid grid-cols-3 gap-px bg-white/[0.06] border border-white/[0.06] rounded-lg overflow-hidden mb-6">
          {[
            { label: 'Devices', value: summary.total, color: 'text-white' },
            { label: 'Tailscale installed', value: summary.installed, color: 'text-white/85' },
            { label: 'Tailscale connected', value: summary.connected, color: 'text-emerald-300' },
          ].map((s) => (
            <div key={s.label} className="bg-[#030308] px-4 py-4">
              <p className="text-[10.5px] uppercase tracking-[0.14em] text-white/40 font-medium">{s.label}</p>
              <p className={`text-2xl font-semibold tabular-nums tracking-tight mt-1 ${s.color}`}>{s.value}</p>
            </div>
          ))}
        </div>

        {loading ? (
          <p className="text-[13px] text-white/45">Loading topology…</p>
        ) : !topology || topology.nodes.length === 0 ? (
          <EmptyState
            title="No devices yet."
            hint="Install agents to populate the network map."
          />
        ) : view === 'graph' ? (
          <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] p-4">
            <TopologyGraph nodes={topology.nodes} />
            <div className="flex items-center justify-center gap-4 text-[11.5px] text-white/45 mt-4 pt-4 border-t border-white/[0.04] flex-wrap">
              <span className="inline-flex items-center gap-1.5">
                <StatusDot tone="online" />
                online
              </span>
              <span className="inline-flex items-center gap-1.5">
                <StatusDot tone="danger" />
                offline
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="w-3 h-0.5 bg-emerald-500/60" />
                tailscale connected
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="w-3 h-0.5 bg-amber-500/60" />
                disconnected
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="w-3 h-0.5 bg-white/20" />
                no tailscale
              </span>
            </div>
          </div>
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {topology.nodes.map((n) => {
              const tsTone = !n.tailscale_installed ? 'muted' : n.tailscale_connected ? 'success' : 'warn'
              const tsLabel = !n.tailscale_installed ? 'no tailscale' : n.tailscale_connected ? 'connected' : 'disconnected'
              return (
                <li key={n.id} className="flex items-center gap-3 px-4 py-3 hover:bg-white/[0.02] transition-colors">
                  <StatusDot tone={statusTone(n.status)} pulse={n.status === 'online'} />
                  <div className="min-w-0 flex-1">
                    <p className="text-[13px] text-white/90 font-medium truncate">{n.hostname || n.id.slice(0, 8)}</p>
                    <p className="text-[11.5px] text-white/40 truncate">
                      <Code>{n.ip_address || '—'}</Code>
                      {n.tailscale_ip && (
                        <>
                          <span className="mx-1.5">·</span>
                          ts <Code>{n.tailscale_ip}</Code>
                        </>
                      )}
                      {n.tailscale_peers > 0 && (
                        <span className="ml-2">
                          {n.tailscale_peers} {n.tailscale_peers === 1 ? 'peer' : 'peers'}
                        </span>
                      )}
                    </p>
                  </div>
                  <Pill tone={tsTone}>{tsLabel}</Pill>
                </li>
              )
            })}
          </ul>
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
