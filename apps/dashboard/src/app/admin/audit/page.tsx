'use client'

import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
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
      toast.error('Failed to load audit log (admin only)')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [limit])

  // Client-side filter on action / resource_type. Server returns up to
  // `limit`; filtering further locally keeps the UI snappy without extra
  // backend complexity.
  const filtered = useMemo(() => {
    return logs.filter((l) => {
      if (actionFilter && !l.action.toLowerCase().includes(actionFilter.toLowerCase())) return false
      if (resourceFilter && !l.resource_type.toLowerCase().includes(resourceFilter.toLowerCase())) return false
      return true
    })
  }, [logs, actionFilter, resourceFilter])

  const distinctResources = useMemo(() => {
    const set = new Set<string>()
    logs.forEach((l) => set.add(l.resource_type))
    return Array.from(set).sort()
  }, [logs])

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="space-y-6">
          <div className="flex items-center justify-between mb-6 flex-wrap gap-3">
            <h1 className="text-2xl font-bold">Audit Log</h1>
            <div className="flex items-center gap-2 flex-wrap">
              <input
                type="text"
                placeholder="filter action..."
                value={actionFilter}
                onChange={(e) => setActionFilter(e.target.value)}
                className="bg-slate-800 border border-slate-700 rounded-md px-3 py-1.5 text-sm w-40"
              />
              <select
                value={resourceFilter}
                onChange={(e) => setResourceFilter(e.target.value)}
                className="bg-slate-800 border border-slate-700 rounded-md px-3 py-1.5 text-sm"
              >
                <option value="">all resources</option>
                {distinctResources.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
              <select
                value={limit}
                onChange={(e) => setLimit(parseInt(e.target.value))}
                className="bg-slate-800 border border-slate-700 rounded-md px-3 py-1.5 text-sm"
              >
                {PAGE_LIMIT_OPTIONS.map((n) => <option key={n} value={n}>{n} rows</option>)}
              </select>
              <Button size="sm" variant="outline" onClick={load} disabled={loading}>
                {loading ? 'Loading…' : 'Refresh'}
              </Button>
            </div>
          </div>

          <Card className="bg-slate-900/60 border-slate-800/50">
            <CardHeader className="pb-3">
              <CardTitle className="text-base">
                {filtered.length} of {logs.length} entries
              </CardTitle>
            </CardHeader>
            <CardContent className="p-0">
              {loading ? (
                <p className="px-6 py-8 text-center text-slate-400">Loading…</p>
              ) : filtered.length === 0 ? (
                <p className="px-6 py-8 text-center text-slate-400">No entries match.</p>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-slate-800/50 text-slate-400 text-xs uppercase">
                        <th className="text-left px-4 py-2">When</th>
                        <th className="text-left px-4 py-2">Action</th>
                        <th className="text-left px-4 py-2">Resource</th>
                        <th className="text-left px-4 py-2">Details</th>
                        <th className="text-left px-4 py-2">IP</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filtered.map((l) => (
                        <tr key={l.id} className="border-b border-slate-800/30 hover:bg-slate-800/20">
                          <td className="px-4 py-2 text-slate-300 whitespace-nowrap font-mono text-xs">
                            {new Date(l.created_at * 1000).toLocaleString()}
                          </td>
                          <td className="px-4 py-2 text-slate-200 font-mono text-xs">{l.action}</td>
                          <td className="px-4 py-2 text-slate-400 text-xs">
                            {l.resource_type}
                            {l.resource_id && (
                              <span className="block text-slate-600 font-mono">{l.resource_id.slice(0, 8)}</span>
                            )}
                          </td>
                          <td className="px-4 py-2 text-slate-400 max-w-md truncate" title={l.details}>{l.details}</td>
                          <td className="px-4 py-2 text-slate-500 font-mono text-xs">{l.ip_address}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
