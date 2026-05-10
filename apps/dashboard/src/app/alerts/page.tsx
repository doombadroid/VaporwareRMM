'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'
import { alertsApi, type Alert } from '@/lib/api'

const severityClass: Record<string, string> = {
  critical: 'bg-red-500/15 border-red-500/40 text-red-300',
  warning: 'bg-amber-500/15 border-amber-500/40 text-amber-300',
  info: 'bg-blue-500/15 border-blue-500/40 text-blue-300',
}

export default function AlertsPage() {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [loading, setLoading] = useState(true)
  const [includeResolved, setIncludeResolved] = useState(false)
  const [resolvingId, setResolvingId] = useState('')

  const load = async (showResolved: boolean) => {
    setLoading(true)
    try {
      const data = await alertsApi.list(showResolved)
      setAlerts(data)
    } catch {
      toast.error('Failed to load alerts')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load(includeResolved) }, [includeResolved])

  const handleResolve = async (id: string) => {
    setResolvingId(id)
    try {
      await alertsApi.resolve(id)
      toast.success('Alert resolved')
      await load(includeResolved)
    } catch {
      toast.error('Failed to resolve alert')
    } finally {
      setResolvingId('')
    }
  }

  return (
    <AuthGuard>
      <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
        <header className="border-b border-slate-800/50 bg-slate-950/80 backdrop-blur-xl sticky top-0 z-50">
          <div className="container mx-auto px-6 py-3">
            <div className="flex items-center justify-between">
              <Link href="/" className="text-xl font-bold bg-gradient-to-r from-blue-400 to-purple-400 bg-clip-text text-transparent">
                vaporRMM
              </Link>
              <div className="flex items-center gap-3">
                <ThemeToggle />
                <Link href="/">
                  <Button variant="ghost" size="sm" className="text-slate-400 hover:text-white">← Dashboard</Button>
                </Link>
              </div>
            </div>
          </div>
        </header>
        <main className="container mx-auto px-6 py-8">
          <div className="flex items-center justify-between mb-6">
            <h1 className="text-2xl font-bold">Alerts</h1>
            <label className="flex items-center gap-2 text-sm text-slate-400">
              <input
                type="checkbox"
                checked={includeResolved}
                onChange={(e) => setIncludeResolved(e.target.checked)}
                className="rounded border-slate-600 bg-slate-800"
              />
              Include resolved
            </label>
          </div>

          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">Loading…</CardContent>
            </Card>
          ) : alerts.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">
                <p>{includeResolved ? 'No alerts on record.' : 'No active alerts.'}</p>
              </CardContent>
            </Card>
          ) : (
            <div className="grid gap-3">
              {alerts.map((a) => (
                <Card key={a.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardHeader className="pb-2 flex flex-row items-start justify-between gap-3">
                    <div className="flex flex-col">
                      <CardTitle className="text-base flex items-center gap-2">
                        <span className={`px-2 py-0.5 rounded text-xs border ${severityClass[a.severity] ?? severityClass.info}`}>
                          {a.severity}
                        </span>
                        <span>{a.type}</span>
                      </CardTitle>
                      <span className="text-xs text-slate-500 mt-1">
                        {new Date(a.created_at * 1000).toLocaleString()}
                        {a.device_id && <> · device {a.device_id.slice(0, 8)}</>}
                      </span>
                    </div>
                    {!a.resolved && (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={resolvingId === a.id}
                        onClick={() => handleResolve(a.id)}
                      >
                        {resolvingId === a.id ? 'Resolving…' : 'Resolve'}
                      </Button>
                    )}
                    {a.resolved && (
                      <span className="text-xs text-emerald-400">resolved</span>
                    )}
                  </CardHeader>
                  <CardContent>
                    <p className="text-sm text-slate-300">{a.message}</p>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </main>
      </div>
    </AuthGuard>
  )
}
