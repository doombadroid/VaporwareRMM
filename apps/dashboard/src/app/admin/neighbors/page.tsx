'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { DataTable, type Column } from '@/components/ui/data-table'
import { Button } from '@/components/ui/button'
import { Code } from '@/components/ui/status'
import { RotateCw } from 'lucide-react'
import { neighborsApi, type UnmanagedNeighbor } from '@/lib/api'

export default function NeighborsPage() {
  const [rows, setRows] = useState<UnmanagedNeighbor[]>([])
  const [loading, setLoading] = useState(true)

  const load = async () => {
    setLoading(true)
    try {
      setRows(await neighborsApi.list())
    } catch {
      toast.error('Failed to load')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const columns: Column<UnmanagedNeighbor>[] = [
    { key: 'ip', header: 'IP', render: (r) => <Code>{r.ip}</Code> },
    {
      key: 'mac',
      header: 'MAC',
      mono: true,
      render: (r) => <span className="text-white/55 text-[11.5px]">{r.mac || '—'}</span>,
    },
    { key: 'host', header: 'Hostname', render: (r) => <span className="text-white/65">{r.hostname || '—'}</span> },
    {
      key: 'obs',
      header: 'Observers',
      align: 'right',
      render: (r) => <span className="text-white/55 tabular-nums">{r.observers}</span>,
    },
    {
      key: 'last',
      header: 'Last seen',
      render: (r) => (
        <span className="text-white/40 text-[11.5px]">{new Date(r.last_seen_at * 1000).toLocaleString()}</span>
      ),
    },
  ]

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Network"
          title="Unmanaged neighbors"
          description="IPs agents observed via ARP that don't match any registered device. Useful for finding rogue hardware on the LAN."
          actions={
            <Button variant="ghost" size="sm" onClick={load} disabled={loading}>
              <RotateCw className={`w-3.5 h-3.5 mr-1.5 ${loading ? 'animate-spin' : ''}`} />
              Refresh
            </Button>
          }
        />

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : rows.length === 0 ? (
          <EmptyState
            title="No unmanaged neighbors observed yet."
            hint="Agents report ARP every hour."
          />
        ) : (
          <DataTable rows={rows} columns={columns} rowKey={(r) => r.ip} />
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
