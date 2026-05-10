'use client'

import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Pill, severityTone } from '@/components/ui/status'
import { FilterBar, FilterChip } from '@/components/ui/data-table'
import { Button } from '@/components/ui/button'
import { alertsApi, type Alert } from '@/lib/api'

type View = 'active' | 'resolved' | 'all'

export default function AlertsPage() {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [loading, setLoading] = useState(true)
  const [view, setView] = useState<View>('active')
  const [resolvingId, setResolvingId] = useState('')

  const load = async (showResolved: boolean) => {
    setLoading(true)
    try {
      setAlerts(await alertsApi.list(showResolved))
    } catch {
      toast.error('Failed to load alerts')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load(view !== 'active')
  }, [view])

  const counts = useMemo(() => {
    const all = alerts.length
    const active = alerts.filter((a) => !a.resolved).length
    return { all, active, resolved: all - active }
  }, [alerts])

  const handleResolve = async (id: string) => {
    setResolvingId(id)
    try {
      await alertsApi.resolve(id)
      toast.success('Alert resolved')
      await load(view !== 'active')
    } catch {
      toast.error('Failed to resolve')
    } finally {
      setResolvingId('')
    }
  }

  const visible = view === 'active' ? alerts.filter((a) => !a.resolved) : view === 'resolved' ? alerts.filter((a) => a.resolved) : alerts
  const critCount = alerts.filter((a) => !a.resolved && a.severity === 'critical').length

  return (
    <AuthGuard>
      <DashboardShell alertCount={critCount}>
        <PageHeader
          eyebrow="Operate"
          title="Alerts"
          description="System-emitted incidents from agent telemetry, alert rules, and integrations."
          separator={false}
        />

        <FilterBar>
          <FilterChip label="Active" active={view === 'active'} onClick={() => setView('active')} count={counts.active} />
          <FilterChip label="Resolved" active={view === 'resolved'} onClick={() => setView('resolved')} count={counts.resolved} />
          <FilterChip label="All" active={view === 'all'} onClick={() => setView('all')} count={counts.all} />
        </FilterBar>

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : visible.length === 0 ? (
          <EmptyState
            title={view === 'active' ? 'No active alerts.' : 'Nothing here.'}
            hint={view === 'active' ? 'When telemetry crosses a threshold, alerts surface here.' : undefined}
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {visible.map((a) => (
              <li
                key={a.id}
                className="px-4 py-3 flex items-start gap-3 hover:bg-white/[0.02] transition-colors"
              >
                <Pill tone={severityTone(a.severity)}>{a.severity}</Pill>
                <div className="min-w-0 flex-1">
                  <p className="text-[13px] text-white/85">{a.message}</p>
                  <p className="text-[11px] text-white/35 mt-1">
                    {a.type} · {new Date(a.created_at * 1000).toLocaleString()}
                    {a.device_id && (
                      <>
                        {' '}
                        · device <span className="font-mono">{a.device_id.slice(0, 8)}</span>
                      </>
                    )}
                  </p>
                </div>
                <div className="shrink-0">
                  {a.resolved ? (
                    <span className="text-[11px] text-emerald-300">resolved</span>
                  ) : (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={resolvingId === a.id}
                      onClick={() => handleResolve(a.id)}
                    >
                      {resolvingId === a.id ? 'Resolving…' : 'Resolve'}
                    </Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
