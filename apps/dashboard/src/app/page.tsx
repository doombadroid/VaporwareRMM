'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { toast } from 'sonner'
import {
  type DashboardOverview,
  type Device,
  type InstallLinks,
  devices as devicesApi,
  branding as brandingApi,
} from '@/lib/api'
import api from '@/lib/api'
import { formatOSVersion } from '@/lib/utils'
import AuthGuard from '@/components/AuthGuard'
import { useBranding } from '@/components/BrandingProvider'
import DashboardShell from '@/components/layout/DashboardShell'
import RemoteControlModal from '@/components/dashboard/RemoteControlModal'
import TailscaleModal from '@/components/dashboard/TailscaleModal'
import BrandingModal from '@/components/dashboard/BrandingModal'
import InstallLinksModal from '@/components/dashboard/InstallLinksModal'
import ResourceChart from '@/components/dashboard/ResourceChart'
import RecentActivityPanel from '@/components/dashboard/RecentActivityPanel'
import SlaCard from '@/components/dashboard/SlaCard'
import CreateTicketModal from '@/components/dashboard/CreateTicketModal'
import SetupWizard from '@/components/dashboard/SetupWizard'
import { PageHeader, Section, EmptyState } from '@/components/ui/page'
import { StatusDot, Pill, Code, severityTone, statusTone } from '@/components/ui/status'
import { Button } from '@/components/ui/button'
import { Plus, Download, Cog, Sparkles, RotateCw } from 'lucide-react'

// Vital signs row. Two columns: a left "fleet pulse" with the four
// numbers an operator triages on first, and a right utilisation bar
// stack. Avoids the six-StatCard grid that DESIGN.md flags as the
// hero-metric template.
function FleetPulse({ overview }: { overview: DashboardOverview }) {
  const total = overview.system_health.total_devices
  const online = overview.system_health.online_devices
  const offline = overview.system_health.offline_devices
  const onlinePct = total > 0 ? Math.round((online / total) * 100) : 0

  const tickets = overview.pending_tickets?.length || 0
  const alerts = overview.active_alerts?.length || 0
  const crit = (overview.active_alerts || []).filter((a) => a.severity === 'critical').length

  type CellTone = 'success' | 'warn' | 'danger' | 'info' | 'muted'
  const cells: { label: string; value: string; sub: string; tone: CellTone }[] = [
    { label: 'Online', value: `${online}`, sub: `${onlinePct}% of ${total}`, tone: online === total ? 'success' : online === 0 ? 'danger' : 'warn' },
    { label: 'Offline', value: `${offline}`, sub: offline > 0 ? 'investigate' : 'none reporting', tone: offline > 0 ? 'warn' : 'muted' },
    { label: 'Open tickets', value: `${tickets}`, sub: tickets === 0 ? 'inbox clear' : 'pending review', tone: tickets > 0 ? 'info' : 'muted' },
    { label: 'Active alerts', value: `${alerts}`, sub: crit > 0 ? `${crit} critical` : alerts === 0 ? 'all clear' : 'open', tone: crit > 0 ? 'danger' : alerts > 0 ? 'warn' : 'muted' },
  ]

  return (
    <div className="grid grid-cols-2 lg:grid-cols-4 gap-px bg-white/[0.06] border border-white/[0.06] rounded-lg overflow-hidden">
      {cells.map((c) => (
        <div key={c.label} className="bg-[#030308] px-4 py-4">
          <div className="flex items-center gap-1.5 text-[10.5px] uppercase tracking-[0.14em] text-white/40 font-medium">
            <StatusDot tone={c.tone} />
            {c.label}
          </div>
          <p className="mt-2 text-2xl font-semibold text-white tabular-nums tracking-tight">
            {c.value}
          </p>
          <p className="text-[11.5px] text-white/35 mt-0.5">{c.sub}</p>
        </div>
      ))}
    </div>
  )
}

// Compact bar reading 0-100. Used for CPU/Mem/Disk fleet averages.
function MeterRow({ label, value, suffix = '%' }: { label: string; value: number; suffix?: string }) {
  const pct = Math.max(0, Math.min(100, value))
  const tone =
    pct >= 90 ? 'bg-rose-400' : pct >= 75 ? 'bg-amber-400' : pct >= 50 ? 'bg-cyan-400' : 'bg-emerald-400'
  return (
    <div className="flex items-center gap-3 py-2">
      <span className="text-[12px] text-white/55 w-16 shrink-0">{label}</span>
      <div className="flex-1 h-1.5 rounded-full bg-white/[0.06] overflow-hidden">
        <div className={`h-full ${tone}`} style={{ width: `${pct}%` }} />
      </div>
      <span className="text-[12px] text-white/85 tabular-nums w-12 text-right">
        {pct.toFixed(1)}
        {suffix}
      </span>
    </div>
  )
}

export default function DashboardPage() {
  const [overview, setOverview] = useState<DashboardOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [remoteControlModal, setRemoteControlModal] = useState<{ open: boolean; device: Device | null }>({
    open: false,
    device: null,
  })
  const [tailscaleModal, setTailscaleModal] = useState<{ open: boolean; device: Device | null }>({
    open: false,
    device: null,
  })
  const { branding, setBranding } = useBranding()
  const [brandingModal, setBrandingModal] = useState(false)
  const [installLinksModal, setInstallLinksModal] = useState(false)
  const [installLinks, setInstallLinks] = useState<InstallLinks | null>(null)
  const [realDevices, setRealDevices] = useState<Device[]>([])
  const [createTicketModal, setCreateTicketModal] = useState(false)
  const [setupWizard, setSetupWizard] = useState(false)

  const loadData = async (silent = false) => {
    if (!silent) setLoading(true)
    else setRefreshing(true)
    try {
      const overviewData = await import('@/lib/api').then((m) => m.dashboard.getOverview())
      setOverview(overviewData)
      try {
        const deviceList = await devicesApi.getAll()
        setRealDevices(deviceList)
      } catch {
        toast.error('Failed to load devices')
      }
    } catch (err) {
      console.error('Failed to load overview:', err)
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => {
    loadData()
    const interval = setInterval(() => loadData(true), 30000)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    const completed = localStorage.getItem('setup_completed')
    if (completed !== 'true') {
      const timer = setTimeout(() => setSetupWizard(true), 800)
      return () => clearTimeout(timer)
    }
  }, [])

  const handleLoadInstallLinks = async () => {
    try {
      const links = await brandingApi.getInstallLinks()
      setInstallLinks(links)
      setInstallLinksModal(true)
    } catch {
      toast.error('Failed to load install links')
    }
  }

  const handleExportCSV = async () => {
    try {
      const blob = await devicesApi.exportCSV()
      const url = window.URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'devices.csv'
      a.click()
      window.URL.revokeObjectURL(url)
      toast.success('Exported devices.csv')
    } catch {
      toast.error('Failed to export devices')
    }
  }

  const handleScanSecurity = async () => {
    try {
      const { data } = await api.get('/compliance/scan')
      toast.success(`Scan complete: ${data.issues || 0} issues found`)
      loadData(true)
    } catch {
      toast.error('Failed to run security scan')
    }
  }

  const critAlerts =
    (overview?.active_alerts || []).filter((a) => a.severity === 'critical').length

  return (
    <AuthGuard>
      <DashboardShell alertCount={critAlerts}>
        <PageHeader
          eyebrow="Operate"
          title="Dashboard"
          description="Fleet pulse, recent activity, SLA at a glance."
          actions={
            <>
              <Button variant="ghost" size="sm" onClick={() => loadData(true)} disabled={refreshing}>
                <RotateCw className={`w-3.5 h-3.5 mr-1.5 ${refreshing ? 'animate-spin' : ''}`} />
                Refresh
              </Button>
              <Button variant="outline" size="sm" onClick={() => setCreateTicketModal(true)}>
                <Plus className="w-3.5 h-3.5 mr-1.5" />
                New ticket
              </Button>
              <Button size="sm" onClick={handleLoadInstallLinks}>
                <Download className="w-3.5 h-3.5 mr-1.5" />
                Add device
              </Button>
            </>
          }
        />

        {loading || !overview ? (
          <div className="space-y-5">
            <div className="h-24 rounded-lg bg-white/[0.02] border border-white/[0.06] animate-pulse" />
            <div className="grid grid-cols-1 lg:grid-cols-3 gap-5">
              <div className="lg:col-span-2 h-64 rounded-lg bg-white/[0.02] border border-white/[0.06] animate-pulse" />
              <div className="h-64 rounded-lg bg-white/[0.02] border border-white/[0.06] animate-pulse" />
            </div>
          </div>
        ) : (
          <div className="space-y-8">
            <FleetPulse overview={overview} />

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-5">
              <Section
                title="Resource utilisation"
                description="24h fleet average across reporting devices."
                className="lg:col-span-2 mb-0"
              >
                <ResourceChart data={overview.resource_history || []} />
                <div className="mt-3 px-1 border-t border-white/[0.04] pt-3">
                  <MeterRow label="CPU" value={overview.system_health.cpu_usage} />
                  <MeterRow label="Memory" value={overview.system_health.memory_usage} />
                  <MeterRow label="Disk" value={overview.system_health.disk_usage} />
                </div>
              </Section>

              <Section title="Recent activity" className="mb-0">
                <RecentActivityPanel activity={overview.recent_activity} />
              </Section>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">
              <Section
                title="Active alerts"
                actions={
                  <Link
                    href="/alerts"
                    className="text-[11.5px] text-white/45 hover:text-white transition-colors"
                  >
                    View all →
                  </Link>
                }
                className="mb-0"
              >
                {(overview.active_alerts || []).length === 0 ? (
                  <EmptyState title="No active alerts." />
                ) : (
                  <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04]">
                    {(overview.active_alerts || []).slice(0, 6).map((a) => (
                      <li
                        key={a.id}
                        className="px-3.5 py-2.5 flex items-center gap-3 hover:bg-white/[0.02] transition-colors"
                      >
                        <Pill tone={severityTone(a.severity)}>{a.severity}</Pill>
                        <div className="min-w-0 flex-1">
                          <p className="text-[13px] text-white/85 truncate">{a.message}</p>
                          <p className="text-[11px] text-white/35 mt-0.5">
                            {a.type} · {new Date(a.created_at * 1000).toLocaleString()}
                          </p>
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
              </Section>

              <Section
                title="Pending tickets"
                actions={
                  <Link
                    href="/tickets"
                    className="text-[11.5px] text-white/45 hover:text-white transition-colors"
                  >
                    View all →
                  </Link>
                }
                className="mb-0"
              >
                {(overview.pending_tickets || []).length === 0 ? (
                  <EmptyState title="No tickets pending." />
                ) : (
                  <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04]">
                    {(overview.pending_tickets || []).slice(0, 6).map((t) => (
                      <li key={t.id}>
                        <Link
                          href={`/tickets/${t.id}`}
                          className="px-3.5 py-2.5 flex items-center gap-3 hover:bg-white/[0.02] transition-colors"
                        >
                          <Pill tone={severityTone(t.priority)}>{t.priority}</Pill>
                          <div className="min-w-0 flex-1">
                            <p className="text-[13px] text-white/85 truncate">{t.title}</p>
                            <p className="text-[11px] text-white/35 mt-0.5">
                              {t.status} · {new Date(t.created_at * 1000).toLocaleString()}
                            </p>
                          </div>
                        </Link>
                      </li>
                    ))}
                  </ul>
                )}
              </Section>
            </div>

            <Section
              title="Devices"
              description={`${realDevices.length} reporting`}
              actions={
                <div className="flex gap-2">
                  <Button variant="ghost" size="sm" onClick={handleExportCSV}>
                    Export CSV
                  </Button>
                  <Link href="/agents">
                    <Button variant="outline" size="sm">
                      Manage all
                    </Button>
                  </Link>
                </div>
              }
              className="mb-0"
            >
              {realDevices.length === 0 ? (
                <EmptyState
                  title="No devices reporting yet."
                  hint="Install an agent on a machine to get started."
                  action={
                    <Button size="sm" onClick={handleLoadInstallLinks}>
                      Add device
                    </Button>
                  }
                />
              ) : (
                <div className="border border-white/[0.06] rounded-lg overflow-hidden bg-white/[0.01]">
                  <div className="overflow-x-auto">
                    <table className="w-full text-[13px]">
                      <thead className="bg-white/[0.02]">
                        <tr className="border-b border-white/[0.06] text-[10.5px] uppercase tracking-[0.12em] text-white/40 font-medium">
                          <th className="text-left px-3 py-2">Hostname</th>
                          <th className="text-left px-3 py-2">Status</th>
                          <th className="text-left px-3 py-2">OS</th>
                          <th className="text-left px-3 py-2">IP</th>
                          <th className="text-left px-3 py-2">Last seen</th>
                          <th className="text-right px-3 py-2">Actions</th>
                        </tr>
                      </thead>
                      <tbody>
                        {realDevices.slice(0, 10).map((d) => (
                          <tr
                            key={d.id}
                            className="h-11 border-b border-white/[0.04] last:border-0 hover:bg-white/[0.03] transition-colors"
                          >
                            <td className="px-3">
                              <Link
                                href={`/devices/${d.id}`}
                                className="text-white/90 hover:text-cyan-400 font-medium transition-colors"
                              >
                                {d.hostname || d.id.slice(0, 8)}
                              </Link>
                            </td>
                            <td className="px-3">
                              <span className="inline-flex items-center gap-1.5 text-[12px] text-white/65">
                                <StatusDot tone={statusTone(d.status)} />
                                {d.status}
                              </span>
                            </td>
                            <td className="px-3 text-white/60 text-[12px]">
                              {formatOSVersion(d.os_name, d.os_version, d.kernel_version)}
                            </td>
                            <td className="px-3">
                              <Code>{d.ip_address || '—'}</Code>
                            </td>
                            <td className="px-3 text-white/40 text-[11.5px]">
                              {d.last_seen
                                ? new Date(d.last_seen * 1000).toLocaleTimeString()
                                : 'never'}
                            </td>
                            <td className="px-3 text-right">
                              <button
                                onClick={() =>
                                  setRemoteControlModal({ open: true, device: d })
                                }
                                className="text-[11.5px] text-white/45 hover:text-cyan-400 transition-colors mr-3"
                              >
                                Remote
                              </button>
                              <button
                                onClick={() => setTailscaleModal({ open: true, device: d })}
                                className="text-[11.5px] text-white/45 hover:text-cyan-400 transition-colors"
                              >
                                Tailscale
                              </button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                  {realDevices.length > 10 && (
                    <Link
                      href="/agents"
                      className="block text-center text-[11.5px] text-white/45 hover:text-white py-2 border-t border-white/[0.04] transition-colors"
                    >
                      View {realDevices.length - 10} more →
                    </Link>
                  )}
                </div>
              )}
            </Section>

            <SlaCard sla={overview.sla} />

            <div className="flex items-center gap-2 text-[11.5px] text-white/35 pt-4 border-t border-white/[0.04]">
              <Sparkles className="w-3 h-3 text-cyan-400" />
              <span>
                AI surface available at{' '}
                <Link href="/admin/ai" className="text-white/55 hover:text-white">
                  /admin/ai
                </Link>
                . Branding live; see{' '}
                <button
                  onClick={() => setBrandingModal(true)}
                  className="text-white/55 hover:text-white"
                >
                  customise
                </button>
                .
              </span>
              <button
                onClick={handleScanSecurity}
                className="ml-auto text-white/45 hover:text-white inline-flex items-center gap-1"
              >
                <Cog className="w-3 h-3" />
                Run compliance scan
              </button>
            </div>
          </div>
        )}

        <RemoteControlModal
          isOpen={remoteControlModal.open}
          onClose={() => setRemoteControlModal({ open: false, device: null })}
          device={remoteControlModal.device}
        />
        <TailscaleModal
          isOpen={tailscaleModal.open}
          onClose={() => setTailscaleModal({ open: false, device: null })}
          device={tailscaleModal.device}
        />
        <BrandingModal
          open={brandingModal}
          onClose={() => setBrandingModal(false)}
          branding={branding}
          onBrandingChange={setBranding}
        />
        <InstallLinksModal
          open={installLinksModal}
          onClose={() => setInstallLinksModal(false)}
          links={installLinks}
        />
        <CreateTicketModal
          open={createTicketModal}
          onClose={() => setCreateTicketModal(false)}
          onCreated={() => loadData(true)}
        />
        <SetupWizard open={setupWizard} onClose={() => setSetupWizard(false)} />
      </DashboardShell>
    </AuthGuard>
  )
}
