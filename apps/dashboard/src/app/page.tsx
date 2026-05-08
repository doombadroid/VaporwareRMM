'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { Monitor, Ticket, AlertTriangle, Zap, Cpu, Globe } from 'lucide-react'
import { toast } from 'sonner'
import {
  type DashboardOverview,
  type Device,
  type InstallLinks,
  devices as devicesApi,
  branding as brandingApi,
} from '@/lib/api'
import api from '@/lib/api'
import AuthGuard from '@/components/AuthGuard'
import { useBranding } from '@/components/BrandingProvider'
import DashboardShell from '@/components/layout/DashboardShell'
import StatCard from '@/components/dashboard/StatCard'
import HealthScore from '@/components/dashboard/HealthScore'
import DeviceFleetTable from '@/components/dashboard/DeviceFleetTable'
import AlertsPanel from '@/components/dashboard/AlertsPanel'
import TicketsPanel from '@/components/dashboard/TicketsPanel'
import RemoteControlModal from '@/components/dashboard/RemoteControlModal'
import TailscaleModal from '@/components/dashboard/TailscaleModal'
import BrandingModal from '@/components/dashboard/BrandingModal'
import InstallLinksModal from '@/components/dashboard/InstallLinksModal'
import ResourceChart from '@/components/dashboard/ResourceChart'
import DeviceStatusChart from '@/components/dashboard/DeviceStatusChart'
import QuickActionsPanel from '@/components/dashboard/QuickActionsPanel'
import RecentActivityPanel from '@/components/dashboard/RecentActivityPanel'
import SlaCard from '@/components/dashboard/SlaCard'
import DashboardLoading from '@/components/dashboard/DashboardLoading'
import CreateTicketModal from '@/components/dashboard/CreateTicketModal'
import SetupWizard from '@/components/dashboard/SetupWizard'

export default function DashboardPage() {
  const router = useRouter()
  const [overview, setOverview] = useState<DashboardOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [remoteControlModal, setRemoteControlModal] = useState<{
    open: boolean
    device: Device | null
  }>({ open: false, device: null })
  const [tailscaleModal, setTailscaleModal] = useState<{
    open: boolean
    device: Device | null
  }>({ open: false, device: null })
  const { branding, setBranding } = useBranding()
  const [brandingModal, setBrandingModal] = useState(false)
  const [installLinksModal, setInstallLinksModal] = useState(false)
  const [installLinks, setInstallLinks] = useState<InstallLinks | null>(null)
  const [realDevices, setRealDevices] = useState<Device[]>([])
  const [selectedDevices, setSelectedDevices] = useState<Set<string>>(
    new Set()
  )
  const [createTicketModal, setCreateTicketModal] = useState(false)
  const [setupWizard, setSetupWizard] = useState(false)

  const loadData = async () => {
    setLoading(true)
    try {
      const overviewData = await import('@/lib/api').then((m) =>
        m.dashboard.getOverview()
      )
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
    }
  }

  useEffect(() => {
    loadData()
    const interval = setInterval(loadData, 30000)
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

  const totalDevices = overview?.system_health?.total_devices || 0
  const onlineDevices = overview?.system_health?.online_devices || 0
  const cpuUsage = overview?.system_health?.cpu_usage || 0
  const memUsage = overview?.system_health?.memory_usage || 0
  const critAlerts =
    (overview?.active_alerts?.filter((a) => a.severity === 'critical') || [])
      .length || 0

  const healthScore = overview
    ? totalDevices > 0
      ? Math.round(
          (onlineDevices / totalDevices) * 40 +
            Math.max(0, (100 - cpuUsage) / 100) * 20 +
            Math.max(0, (100 - memUsage) / 100) * 20 +
            Math.max(0, (100 - critAlerts * 20) / 100) * 20
        )
      : 100
    : 0

  const deviceStatusData = overview
    ? [
        {
          name: 'Online',
          value: overview.system_health.online_devices,
          color: '#10b981',
        },
        {
          name: 'Offline',
          value: overview.system_health.offline_devices,
          color: '#f43f5e',
        },
        {
          name: 'Maintenance',
          value: overview.device_stats.maintenance,
          color: '#f59e0b',
        },
      ]
    : []

  const handleBulkDelete = async () => {
    if (!confirm(`Delete ${selectedDevices.size} devices?`)) return
    try {
      await devicesApi.bulkDelete(Array.from(selectedDevices))
      toast.success(`Deleted ${selectedDevices.size} devices`)
      setSelectedDevices(new Set())
      loadData()
    } catch {
      toast.error('Failed to delete devices')
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

  const handleToggleAll = (checked: boolean) => {
    if (checked) setSelectedDevices(new Set(realDevices.map((d) => d.id)))
    else setSelectedDevices(new Set())
  }

  const handleToggleDevice = (id: string, checked: boolean) => {
    const next = new Set(selectedDevices)
    if (checked) next.add(id)
    else next.delete(id)
    setSelectedDevices(next)
  }

  // Quick Actions handlers
  const handleRemoteControl = () => {
    if (realDevices.length === 0) {
      toast.info('No devices registered yet. Install an agent first.')
      return
    }
    setRemoteControlModal({ open: true, device: realDevices[0] })
  }

  const handleDeployUpdates = async () => {
    if (realDevices.length === 0) {
      toast.info('No devices to update')
      return
    }
    try {
      await api.get('/compliance/scan')
      toast.success('Security scan started')
    } catch {
      toast.error('Failed to start scan')
    }
  }

  const handleScanSecurity = async () => {
    try {
      const { data } = await api.get('/compliance/scan')
      toast.success(`Scan complete: ${data.issues || 0} issues found`)
      loadData()
    } catch {
      toast.error('Failed to run security scan')
    }
  }

  const handleRunReport = () => {
    handleExportCSV()
  }

  const handleAddDevice = () => {
    handleLoadInstallLinks()
  }

  return (
    <AuthGuard>
      <DashboardShell
        alertCount={
          overview?.active_alerts?.filter((a) => a.severity === 'critical')
            .length || 0
        }
      >
        {loading || !overview ? (
          <DashboardLoading />
        ) : (
          <div className="space-y-6 animate-fadeInUp">
            <div className="grid grid-cols-1 lg:grid-cols-4 gap-6">
              <HealthScore score={healthScore} />
              <div className="lg:col-span-3 grid grid-cols-2 lg:grid-cols-3 gap-4">
                <StatCard
                  title="Devices Online"
                  value={overview.system_health.online_devices}
                  icon={Monitor}
                  progress={
                    totalDevices > 0 ? (onlineDevices / totalDevices) * 100 : 0
                  }
                  accent="emerald"
                  trend={{
                    direction: 'up',
                    percentage: Math.round(
                      (onlineDevices / totalDevices) * 100
                    ),
                  }}
                />
                <StatCard
                  title="Pending Tickets"
                  value={overview.pending_tickets?.length || 0}
                  icon={Ticket}
                  accent="cyan"
                  trend={{
                    direction: 'down',
                    percentage:
                      overview.pending_tickets?.filter(
                        (t) => t.priority === 'critical'
                      ).length || 0,
                  }}
                />
                <StatCard
                  title="Active Alerts"
                  value={overview.active_alerts?.length || 0}
                  icon={AlertTriangle}
                  accent="amber"
                  trend={{ direction: 'up', percentage: critAlerts }}
                />
                <StatCard
                  title="CPU Usage"
                  value={overview.system_health.cpu_usage}
                  icon={Zap}
                  accent="violet"
                  progress={cpuUsage}
                />
                <StatCard
                  title="Memory Usage"
                  value={overview.system_health.memory_usage}
                  icon={Cpu}
                  accent="cyan"
                  progress={memUsage}
                />
                <StatCard
                  title="Network Latency"
                  value={overview.system_health.network_latency}
                  icon={Globe}
                  accent="emerald"
                />
              </div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
              <ResourceChart data={overview.resource_history || []} />
              <DeviceStatusChart data={deviceStatusData} />
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
              <TicketsPanel tickets={overview.pending_tickets || []} />
              <AlertsPanel alerts={overview.active_alerts || []} />
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
              <div className="lg:col-span-2">
                <DeviceFleetTable
                  devices={realDevices}
                  selectedDevices={selectedDevices}
                  onToggleDevice={handleToggleDevice}
                  onToggleAll={handleToggleAll}
                  onBulkDelete={handleBulkDelete}
                  onExportCSV={handleExportCSV}
                  onRemoteControl={(device) =>
                    setRemoteControlModal({ open: true, device })
                  }
                  onTailscale={(device) =>
                    setTailscaleModal({ open: true, device })
                  }
                />
              </div>
              <div className="space-y-6">
                <QuickActionsPanel
                  onRemoteControl={handleRemoteControl}
                  onNewTicket={() => setCreateTicketModal(true)}
                  onDeployUpdates={handleDeployUpdates}
                  onRunReport={handleRunReport}
                  onScanSecurity={handleScanSecurity}
                  onAddDevice={handleAddDevice}
                  onBranding={() => setBrandingModal(true)}
                  onClientLinks={handleLoadInstallLinks}
                  onSetupWizard={() => setSetupWizard(true)}
                />
                <RecentActivityPanel />
              </div>
            </div>

            <SlaCard />
          </div>
        )}

        <RemoteControlModal
          isOpen={remoteControlModal.open}
          onClose={() =>
            setRemoteControlModal({ open: false, device: null })
          }
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
          onCreated={loadData}
        />
        <SetupWizard
          open={setupWizard}
          onClose={() => setSetupWizard(false)}
        />
      </DashboardShell>
    </AuthGuard>
  )
}
