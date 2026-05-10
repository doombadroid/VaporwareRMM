'use client'

import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { DataTable, type Column } from '@/components/ui/data-table'
import { Button } from '@/components/ui/button'
import { Code } from '@/components/ui/status'
import { RotateCw } from 'lucide-react'
import { auditApi, type AuditLogEntry } from '@/lib/api'

const PAGE_LIMIT_OPTIONS = [50, 100, 250, 500]

export default function AuditLogPage() {
  const [logs, setLogs] = useState<AuditLogEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [limit, setLimit] = useState(100)
  const [actionFilter, setActionFilter] = useState('')
  const [resourceFilter, setResourceFilter] = useState('')

  const load = async () => {
    setLoading(true)
    try {
      setLogs(await auditApi.list(limit))
    } catch {
      toast.error('Failed to load (admin only)')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [limit])

  const filtered = useMemo(() => {
    return logs.filter((l) => {
      if (actionFilter && !l.action.toLowerCase().includes(actionFilter.toLowerCase())) return false
      if (resourceFilter && l.resource_type !== resourceFilter) return false
      return true
    })
  }, [logs, actionFilter, resourceFilter])

  const distinctResources = useMemo(() => {
    const set = new Set<string>()
    logs.forEach((l) => set.add(l.resource_type))
    return Array.from(set).sort()
  }, [logs])

  const columns: Column<AuditLogEntry>[] = [
    {
      key: 'when',
      header: 'When',
      mono: true,
      render: (l) => (
        <span className="text-white/65 text-[11.5px] whitespace-nowrap">
          {new Date(l.created_at * 1000).toLocaleString()}
        </span>
      ),
    },
    { key: 'action', header: 'Action', render: (l) => <Code>{l.action}</Code> },
    {
      key: 'res',
      header: 'Resource',
      render: (l) => (
        <span className="text-white/55 text-[12px]">
          {l.resource_type}
          {l.resource_id && (
            <span className="block text-white/30 font-mono text-[10.5px]">{l.resource_id.slice(0, 8)}</span>
          )}
        </span>
      ),
    },
    {
      key: 'details',
      header: 'Details',
      render: (l) => (
        <span className="text-white/55 truncate block max-w-md" title={l.details}>
          {l.details}
        </span>
      ),
    },
    { key: 'ip', header: 'IP', mono: true, render: (l) => <span className="text-white/40 text-[11px]">{l.ip_address}</span> },
  ]

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Audit"
          title="Audit log"
          description={`${filtered.length} of ${logs.length} entries.`}
          actions={
            <Button variant="ghost" size="sm" onClick={load} disabled={loading}>
              <RotateCw className={`w-3.5 h-3.5 mr-1.5 ${loading ? 'animate-spin' : ''}`} />
              Refresh
            </Button>
          }
          separator={false}
        />

        <div className="flex flex-wrap items-center gap-2 mb-4 pb-4 border-b border-white/[0.04]">
          <input
            type="text"
            placeholder="filter action…"
            value={actionFilter}
            onChange={(e) => setActionFilter(e.target.value)}
            className="bg-white/[0.04] border border-white/[0.08] rounded-md px-2.5 py-1 text-[12px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2] w-44"
          />
          <select
            value={resourceFilter}
            onChange={(e) => setResourceFilter(e.target.value)}
            className="bg-white/[0.04] border border-white/[0.08] rounded-md px-2.5 py-1 text-[12px] text-white/85 focus:outline-none focus:border-white/[0.2]"
          >
            <option value="">all resources</option>
            {distinctResources.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
          <select
            value={limit}
            onChange={(e) => setLimit(parseInt(e.target.value))}
            className="bg-white/[0.04] border border-white/[0.08] rounded-md px-2.5 py-1 text-[12px] text-white/85 focus:outline-none focus:border-white/[0.2] ml-auto"
          >
            {PAGE_LIMIT_OPTIONS.map((n) => (
              <option key={n} value={n}>
                {n} rows
              </option>
            ))}
          </select>
        </div>

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : filtered.length === 0 ? (
          <EmptyState title="No entries match." />
        ) : (
          <DataTable rows={filtered} columns={columns} rowKey={(l) => l.id} dense />
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
