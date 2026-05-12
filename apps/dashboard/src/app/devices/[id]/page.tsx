'use client'

import { useEffect, useState } from 'react'
import { useParams, useRouter } from 'next/navigation'
import { Button } from '@/components/ui/button'
import {
  devices as devicesApi,
  deviceCommandsApi,
  deviceFilesApi,
  inventoryApi,
  type Device,
  type DeviceCommand,
  type FileTransfer,
  type SoftwareEntry,
  type HardwareInfo,
} from '@/lib/api'
import { formatBytes, formatOSVersion } from '@/lib/utils'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, Section, EmptyState } from '@/components/ui/page'
import { StatusDot, statusTone, Pill, Code } from '@/components/ui/status'
import { Monitor, ArrowLeft, Terminal, FileUp, Boxes, Tag } from 'lucide-react'
import { toast } from 'sonner'

type DeviceTab = 'overview' | 'commands' | 'files' | 'software'

function StatRow({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="flex items-center justify-between py-1.5 border-b border-white/[0.04] last:border-0">
      <span className="text-[11.5px] text-white/45">{label}</span>
      <span className="text-[12.5px] text-white/85 font-mono">{value}</span>
    </div>
  )
}

// fmtBytes is a thin wrapper around the shared formatBytes helper
// so existing call sites keep their familiar name. New code should
// import formatBytes directly from @/lib/utils.
const fmtBytes = formatBytes

export default function DeviceDetailPage() {
  const params = useParams()
  const router = useRouter()
  const deviceId = params.id as string
  const [device, setDevice] = useState<Device | null>(null)
  const [loading, setLoading] = useState(true)
  const [tab, setTab] = useState<DeviceTab>('overview')
  const [commands, setCommands] = useState<DeviceCommand[]>([])
  const [commandsLoading, setCommandsLoading] = useState(false)
  const [files, setFiles] = useState<FileTransfer[]>([])
  const [filesLoading, setFilesLoading] = useState(false)
  const [software, setSoftware] = useState<SoftwareEntry[]>([])
  const [softwareLoading, setSoftwareLoading] = useState(false)
  const [hardware, setHardware] = useState<HardwareInfo | null>(null)
  const [softwareFilter, setSoftwareFilter] = useState('')

  useEffect(() => {
    if (!deviceId) return
    devicesApi
      .getById(deviceId)
      .then(setDevice)
      .catch(() => toast.error('Failed to load device'))
      .finally(() => setLoading(false))
  }, [deviceId])

  useEffect(() => {
    if (!deviceId || tab !== 'commands') return
    setCommandsLoading(true)
    deviceCommandsApi.list(deviceId, 100).then(setCommands).catch(() => toast.error('Failed to load commands')).finally(() => setCommandsLoading(false))
  }, [deviceId, tab])

  useEffect(() => {
    if (!deviceId || tab !== 'files') return
    setFilesLoading(true)
    deviceFilesApi.list(deviceId).then(setFiles).catch(() => toast.error('Failed to load files')).finally(() => setFilesLoading(false))
  }, [deviceId, tab])

  useEffect(() => {
    if (!deviceId || tab !== 'software') return
    setSoftwareLoading(true)
    Promise.all([inventoryApi.deviceSoftware(deviceId), inventoryApi.deviceHardware(deviceId)])
      .then(([sw, hw]) => {
        setSoftware(sw)
        setHardware(hw?.hardware || null)
      })
      .catch(() => toast.error('Failed to load inventory'))
      .finally(() => setSoftwareLoading(false))
  }, [deviceId, tab])

  if (loading) {
    return (
      <AuthGuard>
        <DashboardShell>
          <p className="text-[13px] text-white/45">Loading device…</p>
        </DashboardShell>
      </AuthGuard>
    )
  }

  if (!device) {
    return (
      <AuthGuard>
        <DashboardShell>
          <EmptyState
            title="Device not found."
            action={
              <Button size="sm" onClick={() => router.push('/agents')}>
                <ArrowLeft className="w-3.5 h-3.5 mr-1.5" />
                Back to devices
              </Button>
            }
          />
        </DashboardShell>
      </AuthGuard>
    )
  }

  const tabs: { id: DeviceTab; label: string; Icon: typeof Monitor }[] = [
    { id: 'overview', label: 'Overview', Icon: Monitor },
    { id: 'commands', label: 'Commands', Icon: Terminal },
    { id: 'files', label: 'Files', Icon: FileUp },
    { id: 'software', label: 'Software', Icon: Boxes },
  ]

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          breadcrumbs={[
            { href: '/agents', label: 'Devices' },
            { label: device.hostname },
          ]}
          eyebrow={formatOSVersion(device.os_name, device.os_version, device.kernel_version)}
          title={device.hostname}
          description={device.id}
          actions={
            <span className="inline-flex items-center gap-1.5 text-[12px] text-white/65">
              <StatusDot tone={statusTone(device.status)} pulse={device.status === 'online'} />
              {device.status}
            </span>
          }
          separator={false}
        />

        <div className="flex gap-1 mb-6 border-b border-white/[0.06]">
          {tabs.map(({ id, label, Icon }) => (
            <button
              key={id}
              onClick={() => setTab(id)}
              className={`flex items-center gap-1.5 px-3 py-2 text-[12.5px] -mb-px border-b transition-colors ${
                tab === id
                  ? 'border-cyan-400/70 text-white'
                  : 'border-transparent text-white/45 hover:text-white/85'
              }`}
            >
              <Icon className="w-3.5 h-3.5" />
              {label}
            </button>
          ))}
        </div>

        {tab === 'overview' && (
          <div className="grid grid-cols-1 md:grid-cols-3 gap-px bg-white/[0.06] border border-white/[0.06] rounded-lg overflow-hidden">
            <div className="bg-[#030308] px-4 py-4">
              <p className="text-[10.5px] uppercase tracking-[0.14em] text-white/40 font-medium mb-2">System</p>
              <StatRow label="IP" value={device.ip_address || '—'} />
              <StatRow label="MAC" value={device.mac_address || '—'} />
              <StatRow label="Agent" value={device.agent_version || '—'} />
            </div>
            <div className="bg-[#030308] px-4 py-4">
              <p className="text-[10.5px] uppercase tracking-[0.14em] text-white/40 font-medium mb-2">Resources</p>
              <StatRow label="CPU" value={device.cpu || '—'} />
              <StatRow label="Memory" value={fmtBytes(device.memory)} />
              <StatRow label="Disk" value={fmtBytes(device.disk_size)} />
            </div>
            <div className="bg-[#030308] px-4 py-4">
              <p className="text-[10.5px] uppercase tracking-[0.14em] text-white/40 font-medium mb-2">Activity</p>
              <StatRow
                label="Last seen"
                value={device.last_seen > 0 ? new Date(device.last_seen * 1000).toLocaleString() : 'never'}
              />
              <StatRow label="Registered" value={new Date(device.created_at * 1000).toLocaleString()} />
              <StatRow
                label="Heartbeat"
                value={
                  device.last_seen > 0
                    ? `${Math.max(0, Math.floor((Date.now() / 1000 - device.last_seen) / 60))} min ago`
                    : 'never'
                }
              />
            </div>
            {device.tags && device.tags.length > 0 && (
              <div className="md:col-span-3 bg-[#030308] px-4 py-3">
                <p className="text-[10.5px] uppercase tracking-[0.14em] text-white/40 font-medium mb-2">Tags</p>
                <div className="flex flex-wrap gap-1.5">
                  {device.tags.map((t) => (
                    <span
                      key={t}
                      className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-white/[0.04] border border-white/[0.06] text-[11px] text-white/65"
                    >
                      <Tag className="w-2.5 h-2.5" />
                      {t}
                    </span>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {tab === 'commands' && (
          <Section className="mb-0">
            {commandsLoading ? (
              <p className="text-[13px] text-white/45">Loading…</p>
            ) : commands.length === 0 ? (
              <EmptyState title="No commands sent to this device." />
            ) : (
              <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
                {commands.map((c) => (
                  <li key={c.id} className="px-4 py-3">
                    <div className="flex items-center gap-2 flex-wrap">
                      <Code>{c.type}</Code>
                      <Pill tone={statusTone(c.status)}>{c.status}</Pill>
                      <span className="text-[11px] text-white/35">{new Date(c.created_at * 1000).toLocaleString()}</span>
                      {c.finished_at && (
                        <span className="text-[11px] text-white/35">· {Math.max(0, c.finished_at - c.created_at)}s</span>
                      )}
                    </div>
                    {c.payload && (
                      <pre className="mt-2 text-[11.5px] font-mono text-white/55 whitespace-pre-wrap break-all">
                        {c.payload.length > 500 ? c.payload.slice(0, 500) + '…' : c.payload}
                      </pre>
                    )}
                    {c.output && (
                      <details className="mt-2">
                        <summary className="text-[11px] text-white/40 cursor-pointer hover:text-white/65">
                          output ({c.output.length} chars)
                        </summary>
                        <pre className="mt-1 text-[11px] font-mono text-white/55 whitespace-pre-wrap break-all max-h-64 overflow-auto bg-white/[0.02] rounded p-2">
                          {c.output}
                        </pre>
                      </details>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </Section>
        )}

        {tab === 'software' && (
          <div className="space-y-6">
            {hardware && (
              <Section title="Hardware" className="mb-0">
                <div className="grid grid-cols-2 md:grid-cols-3 gap-px bg-white/[0.06] border border-white/[0.06] rounded-lg overflow-hidden">
                  {[
                    ['CPU', hardware.cpu_model || '—'],
                    ['Cores', String(hardware.cpu_cores || '—')],
                    ['RAM', formatBytes(hardware.ram_bytes)],
                    ['Disk', formatBytes(hardware.disk_total_bytes)],
                    ['Platform', `${hardware.platform || ''} ${hardware.platform_version || ''}`.trim() || '—'],
                    ['Kernel', hardware.kernel_version || '—'],
                  ].map(([label, value]) => (
                    <div key={label} className="bg-[#030308] px-4 py-3">
                      <p className="text-[10.5px] uppercase tracking-[0.14em] text-white/40">{label}</p>
                      <p className="text-[12.5px] text-white/85 mt-1 font-mono break-all">{value}</p>
                    </div>
                  ))}
                </div>
              </Section>
            )}
            <Section
              title={`Installed software (${software.length})`}
              actions={
                <input
                  type="text"
                  placeholder="filter…"
                  value={softwareFilter}
                  onChange={(e) => setSoftwareFilter(e.target.value)}
                  className="bg-white/[0.04] border border-white/[0.08] rounded-md px-2.5 py-1 text-[12px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]"
                />
              }
              className="mb-0"
            >
              {softwareLoading ? (
                <p className="text-[13px] text-white/45">Loading…</p>
              ) : software.length === 0 ? (
                <EmptyState
                  title="No inventory yet."
                  hint="Agent reports software every 6 hours; first snapshot ~1 minute after start."
                />
              ) : (
                <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01] max-h-[60vh] overflow-y-auto">
                  {software
                    .filter((s) => !softwareFilter || s.name.toLowerCase().includes(softwareFilter.toLowerCase()))
                    .map((s, i) => (
                      <li key={`${s.name}-${i}`} className="px-4 py-2 flex items-center gap-3">
                        <div className="min-w-0 flex-1">
                          <p className="text-[13px] text-white/85 truncate">{s.name}</p>
                          {s.vendor && <p className="text-[11px] text-white/40 truncate">{s.vendor}</p>}
                        </div>
                        <span className="text-[11.5px] font-mono text-white/45 shrink-0">{s.version || '—'}</span>
                      </li>
                    ))}
                </ul>
              )}
            </Section>
          </div>
        )}

        {tab === 'files' && (
          <Section className="mb-0">
            {filesLoading ? (
              <p className="text-[13px] text-white/45">Loading…</p>
            ) : files.length === 0 ? (
              <EmptyState title="No file transfers for this device." />
            ) : (
              <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
                {files.map((f) => (
                  <li key={f.id} className="px-4 py-3 flex items-center gap-3">
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] text-white/85 truncate font-mono">{f.file_name}</p>
                      <p className="text-[11px] text-white/40 truncate">
                        {f.type} · {f.file_path}
                      </p>
                      <p className="text-[10.5px] text-white/30 mt-0.5">
                        {new Date(f.created_at * 1000).toLocaleString()}
                        {f.completed_at && <> · completed {new Date(f.completed_at * 1000).toLocaleString()}</>}
                      </p>
                    </div>
                    <div className="text-right shrink-0">
                      <Pill tone={statusTone(f.status)}>{f.status}</Pill>
                      {f.progress > 0 && f.progress < 100 && (
                        <p className="text-[11px] text-white/40 mt-1 tabular-nums">{f.progress}%</p>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </Section>
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
