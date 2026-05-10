'use client'

import { useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import { toast } from 'sonner'
import { devices as devicesApi, type Device } from '@/lib/api'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader } from '@/components/ui/page'
import { DataTable, FilterBar, FilterChip, type Column } from '@/components/ui/data-table'
import { StatusDot, statusTone, Code } from '@/components/ui/status'
import { Button } from '@/components/ui/button'
import { Download, Search } from 'lucide-react'

type StatusFilter = 'all' | 'online' | 'offline' | 'warning'

export default function AgentsPage() {
  const [devices, setDevices] = useState<Device[]>([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState<StatusFilter>('all')
  const [query, setQuery] = useState('')

  useEffect(() => {
    devicesApi
      .getAll()
      .then(setDevices)
      .catch(() => toast.error('Failed to load devices'))
      .finally(() => setLoading(false))
  }, [])

  const counts = useMemo(() => {
    const total = devices.length
    const online = devices.filter((d) => d.status === 'online').length
    const offline = devices.filter((d) => d.status === 'offline').length
    const warning = devices.filter((d) => d.status === 'warning').length
    return { total, online, offline, warning }
  }, [devices])

  const visible = useMemo(() => {
    let rows = devices
    if (filter !== 'all') rows = rows.filter((d) => d.status === filter)
    if (query) {
      const q = query.toLowerCase()
      rows = rows.filter(
        (d) =>
          (d.hostname || '').toLowerCase().includes(q) ||
          (d.ip_address || '').toLowerCase().includes(q) ||
          (d.os_name || '').toLowerCase().includes(q),
      )
    }
    return rows
  }, [devices, filter, query])

  const handleExport = async () => {
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
      toast.error('Export failed')
    }
  }

  const columns: Column<Device>[] = [
    {
      key: 'hostname',
      header: 'Hostname',
      primary: true,
      render: (d) => (
        <Link
          href={`/devices/${d.id}`}
          className="text-white/90 hover:text-cyan-400 font-medium transition-colors"
        >
          {d.hostname || d.id.slice(0, 8)}
        </Link>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      render: (d) => (
        <span className="inline-flex items-center gap-1.5 text-[12px] text-white/65">
          <StatusDot tone={statusTone(d.status)} pulse={d.status === 'online'} />
          {d.status}
        </span>
      ),
    },
    { key: 'os', header: 'OS', render: (d) => <span className="text-white/55 text-[12px]">{d.os_name} {d.os_version}</span> },
    { key: 'ip', header: 'IP', render: (d) => <Code>{d.ip_address || '—'}</Code> },
    {
      key: 'last',
      header: 'Last seen',
      render: (d) => (
        <span className="text-white/40 text-[11.5px]">
          {d.last_seen ? new Date(d.last_seen * 1000).toLocaleString() : 'never'}
        </span>
      ),
    },
  ]

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Operate"
          title="Devices"
          description={`${counts.total} reporting · ${counts.online} online, ${counts.offline} offline.`}
          actions={
            <Button variant="outline" size="sm" onClick={handleExport}>
              <Download className="w-3.5 h-3.5 mr-1.5" />
              Export CSV
            </Button>
          }
          separator={false}
        />

        <FilterBar>
          <FilterChip label="All" active={filter === 'all'} onClick={() => setFilter('all')} count={counts.total} />
          <FilterChip label="Online" active={filter === 'online'} onClick={() => setFilter('online')} count={counts.online} />
          <FilterChip label="Offline" active={filter === 'offline'} onClick={() => setFilter('offline')} count={counts.offline} />
          {counts.warning > 0 && (
            <FilterChip label="Warning" active={filter === 'warning'} onClick={() => setFilter('warning')} count={counts.warning} />
          )}
          <div className="ml-auto relative">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-white/35" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter hostname, IP, OS"
              className="bg-white/[0.04] border border-white/[0.08] rounded-md pl-8 pr-3 py-1 text-[12px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2] w-64"
            />
          </div>
        </FilterBar>

        {loading ? (
          <p className="text-[13px] text-white/45">Loading devices…</p>
        ) : (
          <DataTable
            rows={visible}
            columns={columns}
            rowKey={(d) => d.id}
            empty={query || filter !== 'all' ? 'No matches.' : 'No devices reporting yet.'}
          />
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
