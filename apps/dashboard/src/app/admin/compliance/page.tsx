'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { complianceApi, type ComplianceResult } from '@/lib/api'

const statusClass: Record<string, string> = {
  pass: 'bg-emerald-500/15 border-emerald-500/40 text-emerald-300',
  warn: 'bg-amber-500/15 border-amber-500/40 text-amber-300',
  fail: 'bg-red-500/15 border-red-500/40 text-red-300',
}

export default function CompliancePage() {
  const [results, setResults] = useState<ComplianceResult[]>([])
  const [loading, setLoading] = useState(true)
  const [scanning, setScanning] = useState(false)
  const [issues, setIssues] = useState(0)

  const load = async () => {
    setLoading(true)
    try {
      setResults(await complianceApi.results())
    } catch {
      toast.error('Failed to load compliance results (admin only)')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

  const scan = async () => {
    setScanning(true)
    try {
      const data = await complianceApi.scan()
      setResults(data.results || [])
      setIssues(data.issues || 0)
      toast.success(`Scan complete: ${data.issues || 0} issues`)
    } catch {
      toast.error('Failed to run scan')
    } finally {
      setScanning(false)
    }
  }

  const failCount = results.filter((r) => r.status === 'fail').length
  const warnCount = results.filter((r) => r.status === 'warn').length
  const passCount = results.filter((r) => r.status === 'pass').length

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-5xl space-y-6">
          <div className="flex items-center justify-between mb-6">
            <h1 className="text-2xl font-bold">Compliance</h1>
            <Button onClick={scan} disabled={scanning}>
              {scanning ? 'Scanning…' : 'Run scan'}
            </Button>
          </div>

          <div className="grid grid-cols-3 gap-3 mb-6">
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-4">
                <p className="text-xs text-slate-400">Passing</p>
                <p className="text-2xl font-bold text-emerald-300">{passCount}</p>
              </CardContent>
            </Card>
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-4">
                <p className="text-xs text-slate-400">Warnings</p>
                <p className="text-2xl font-bold text-amber-300">{warnCount}</p>
              </CardContent>
            </Card>
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-4">
                <p className="text-xs text-slate-400">Failures{issues > 0 && ` (${issues})`}</p>
                <p className="text-2xl font-bold text-red-300">{failCount}</p>
              </CardContent>
            </Card>
          </div>

          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">Loading…</CardContent>
            </Card>
          ) : results.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">
                <p>No compliance results yet.</p>
                <p className="text-sm mt-2">Click Run scan to evaluate the fleet.</p>
              </CardContent>
            </Card>
          ) : (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3">
                <CardTitle className="text-base">Results ({results.length})</CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-slate-800/50 text-slate-400 text-xs uppercase">
                        <th className="text-left px-4 py-2">Status</th>
                        <th className="text-left px-4 py-2">Device</th>
                        <th className="text-left px-4 py-2">Check</th>
                        <th className="text-left px-4 py-2">Details</th>
                      </tr>
                    </thead>
                    <tbody>
                      {results.map((r, i) => (
                        <tr key={i} className="border-b border-slate-800/30 hover:bg-slate-800/20">
                          <td className="px-4 py-2">
                            <span className={`px-2 py-0.5 rounded border text-xs ${statusClass[r.status] ?? statusClass.warn}`}>
                              {r.status}
                            </span>
                          </td>
                          <td className="px-4 py-2 text-slate-200">{r.hostname || r.device_id.slice(0, 8)}</td>
                          <td className="px-4 py-2 text-slate-300 font-mono text-xs">{r.check}</td>
                          <td className="px-4 py-2 text-slate-400">{r.details}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </CardContent>
            </Card>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
