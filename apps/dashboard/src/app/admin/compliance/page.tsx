'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Pill, statusTone, Code } from '@/components/ui/status'
import { DataTable, type Column } from '@/components/ui/data-table'
import { Button } from '@/components/ui/button'
import { ShieldCheck } from 'lucide-react'
import { complianceApi, type ComplianceResult } from '@/lib/api'

export default function CompliancePage() {
  const [results, setResults] = useState<ComplianceResult[]>([])
  const [loading, setLoading] = useState(true)
  const [scanning, setScanning] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      setResults(await complianceApi.results())
    } catch {
      toast.error('Failed to load (admin only)')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const scan = async () => {
    setScanning(true)
    try {
      const data = await complianceApi.scan()
      setResults(data.results || [])
      toast.success(`Scan complete: ${data.issues || 0} issues`)
    } catch {
      toast.error('Scan failed')
    } finally {
      setScanning(false)
    }
  }

  const failCount = results.filter((r) => r.status === 'fail').length
  const warnCount = results.filter((r) => r.status === 'warn').length
  const passCount = results.filter((r) => r.status === 'pass').length

  const columns: Column<ComplianceResult>[] = [
    { key: 'status', header: 'Status', render: (r) => <Pill tone={statusTone(r.status)}>{r.status}</Pill> },
    { key: 'device', header: 'Device', render: (r) => <span className="text-white/85">{r.hostname || r.device_id.slice(0, 8)}</span> },
    { key: 'check', header: 'Check', render: (r) => <Code>{r.check}</Code> },
    { key: 'details', header: 'Details', render: (r) => <span className="text-white/55">{r.details}</span> },
  ]

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Audit"
          title="Compliance"
          description="Hardening checks across the fleet."
          actions={
            <Button size="sm" onClick={scan} disabled={scanning}>
              <ShieldCheck className="w-3.5 h-3.5 mr-1.5" />
              {scanning ? 'Scanning…' : 'Run scan'}
            </Button>
          }
        />

        <div className="grid grid-cols-3 gap-px bg-white/[0.06] border border-white/[0.06] rounded-lg overflow-hidden mb-6">
          {[
            { label: 'Passing', value: passCount, color: 'text-emerald-300' },
            { label: 'Warnings', value: warnCount, color: 'text-amber-300' },
            { label: 'Failures', value: failCount, color: 'text-rose-300' },
          ].map((s) => (
            <div key={s.label} className="bg-[#030308] px-4 py-4">
              <p className="text-[10.5px] uppercase tracking-[0.14em] text-white/40 font-medium">{s.label}</p>
              <p className={`text-2xl font-semibold tabular-nums tracking-tight mt-1 ${s.color}`}>{s.value}</p>
            </div>
          ))}
        </div>

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : results.length === 0 ? (
          <EmptyState title="No compliance results yet." hint="Run a scan to evaluate the fleet." />
        ) : (
          <DataTable rows={results} columns={columns} rowKey={(r) => `${r.device_id}-${r.check}`} dense />
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
