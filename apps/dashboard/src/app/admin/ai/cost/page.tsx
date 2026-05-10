'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { aiCostApi, type AICostResponse } from '@/lib/api'

const usdFromMicros = (m: number): string => `$${(m / 1_000_000).toFixed(4)}`

export default function AICostPage() {
  const [days, setDays] = useState(30)
  const [data, setData] = useState<AICostResponse | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    aiCostApi.get(days)
      .then(setData)
      .catch(() => toast.error('Failed to load (admin only?)'))
      .finally(() => setLoading(false))
  }, [days])

  const maxDailyMicros = data?.daily.reduce((acc, d) => Math.max(acc, d.cost_usd_micros), 0) || 1

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="max-w-5xl space-y-6">
          <div className="flex items-center justify-between">
            <h1 className="text-2xl font-bold">AI cost</h1>
            <select
              value={days}
              onChange={(e) => setDays(parseInt(e.target.value))}
              className="bg-slate-800 border border-slate-700 rounded-md px-3 py-1.5 text-sm"
            >
              <option value={7}>7 days</option>
              <option value={30}>30 days</option>
              <option value={90}>90 days</option>
              <option value={365}>365 days</option>
            </select>
          </div>

          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50"><CardContent className="py-12 text-center text-slate-400">Loading…</CardContent></Card>
          ) : !data ? null : (
            <>
              <div className="grid grid-cols-3 gap-3">
                <Card className="bg-slate-900/60 border-slate-800/50">
                  <CardContent className="py-4">
                    <p className="text-xs text-slate-400">Total cost</p>
                    <p className="text-2xl font-bold text-emerald-300">{usdFromMicros(data.total_usd_micros)}</p>
                  </CardContent>
                </Card>
                <Card className="bg-slate-900/60 border-slate-800/50">
                  <CardContent className="py-4">
                    <p className="text-xs text-slate-400">Total tokens</p>
                    <p className="text-2xl font-bold">{data.total_tokens.toLocaleString()}</p>
                  </CardContent>
                </Card>
                <Card className="bg-slate-900/60 border-slate-800/50">
                  <CardContent className="py-4">
                    <p className="text-xs text-slate-400">Calls</p>
                    <p className="text-2xl font-bold">{data.total_calls.toLocaleString()}</p>
                  </CardContent>
                </Card>
              </div>

              <Card className="bg-slate-900/60 border-slate-800/50">
                <CardHeader className="pb-3"><CardTitle className="text-base">Daily spend</CardTitle></CardHeader>
                <CardContent>
                  {data.daily.length === 0 ? (
                    <p className="text-sm text-slate-400 py-6 text-center">No AI runs in the window.</p>
                  ) : (
                    <div className="space-y-1">
                      {data.daily.map((d) => (
                        <div key={d.day} className="flex items-center gap-3 text-xs">
                          <span className="w-24 text-slate-400 font-mono">{new Date(d.day * 1000).toISOString().slice(0, 10)}</span>
                          <div className="flex-1 h-3 bg-slate-800/50 rounded">
                            <div className="h-3 rounded bg-cyan-500/40" style={{ width: `${(d.cost_usd_micros / maxDailyMicros) * 100}%` }} />
                          </div>
                          <span className="w-24 text-right text-slate-300 font-mono">{usdFromMicros(d.cost_usd_micros)}</span>
                          <span className="w-20 text-right text-slate-500 font-mono">{d.calls} calls</span>
                        </div>
                      ))}
                    </div>
                  )}
                </CardContent>
              </Card>

              <Card className="bg-slate-900/60 border-slate-800/50">
                <CardHeader className="pb-3"><CardTitle className="text-base">By capability</CardTitle></CardHeader>
                <CardContent className="p-0">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b border-slate-800/50 text-xs uppercase text-slate-400">
                        <th className="text-left px-4 py-2">Capability</th>
                        <th className="text-right px-4 py-2">Cost</th>
                        <th className="text-right px-4 py-2">Tokens</th>
                        <th className="text-right px-4 py-2">Calls</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.by_capability.map((c) => (
                        <tr key={c.capability} className="border-b border-slate-800/30">
                          <td className="px-4 py-2 font-mono text-slate-200">{c.capability}</td>
                          <td className="px-4 py-2 text-right text-slate-300 font-mono">{usdFromMicros(c.cost_usd_micros)}</td>
                          <td className="px-4 py-2 text-right text-slate-400 font-mono">{c.tokens.toLocaleString()}</td>
                          <td className="px-4 py-2 text-right text-slate-400 font-mono">{c.calls.toLocaleString()}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </CardContent>
              </Card>
            </>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
