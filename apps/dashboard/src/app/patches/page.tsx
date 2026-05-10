'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'
import { patchesApi, type Patch, type PatchStatusFilter } from '@/lib/api'

const severityClass: Record<string, string> = {
  critical: 'bg-red-500/15 border-red-500/40 text-red-300',
  high: 'bg-orange-500/15 border-orange-500/40 text-orange-300',
  medium: 'bg-amber-500/15 border-amber-500/40 text-amber-300',
  low: 'bg-slate-500/15 border-slate-500/40 text-slate-300',
}

const statusClass: Record<string, string> = {
  pending: 'bg-blue-500/15 border-blue-500/40 text-blue-300',
  installing: 'bg-amber-500/15 border-amber-500/40 text-amber-300',
  installed: 'bg-emerald-500/15 border-emerald-500/40 text-emerald-300',
  failed: 'bg-red-500/15 border-red-500/40 text-red-300',
}

const FILTERS: PatchStatusFilter[] = ['pending', 'installing', 'installed', 'failed', 'all']

export default function PatchesPage() {
  const [patches, setPatches] = useState<Patch[]>([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState<PatchStatusFilter>('pending')
  const [actingId, setActingId] = useState('')

  const load = async (status: PatchStatusFilter) => {
    setLoading(true)
    try {
      setPatches(await patchesApi.list(status))
    } catch {
      toast.error('Failed to load patches')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load(filter) }, [filter])

  const install = async (p: Patch) => {
    setActingId(p.id)
    try {
      if (p.source && p.kb_id) {
        // Patch was discovered by agent — push real install command.
        await patchesApi.install(p.id)
        toast.success('Install queued on device')
      } else {
        // Manually-created patch with no source/kb_id — fall back to
        // "mark installed" for parity with prior behavior.
        await patchesApi.updateStatus(p.id, 'installed')
        toast.success('Marked installed')
      }
      await load(filter)
    } catch {
      toast.error('Action failed (admin only?)')
    } finally {
      setActingId('')
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
            <h1 className="text-2xl font-bold">Patch Management</h1>
            <select
              value={filter}
              onChange={(e) => setFilter(e.target.value as PatchStatusFilter)}
              className="bg-slate-800 border border-slate-700 rounded-md px-3 py-1.5 text-sm"
            >
              {FILTERS.map((f) => (
                <option key={f} value={f}>{f}</option>
              ))}
            </select>
          </div>

          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">Loading…</CardContent>
            </Card>
          ) : patches.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">
                <p>No {filter === 'all' ? '' : filter + ' '}patches.</p>
                <p className="text-sm mt-2">Patches are reported by agents; create one manually via the device page.</p>
              </CardContent>
            </Card>
          ) : (
            <div className="grid gap-3">
              {patches.map((p) => (
                <Card key={p.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardHeader className="pb-2 flex flex-row items-start justify-between gap-3">
                    <div className="flex flex-col min-w-0">
                      <CardTitle className="text-base truncate">{p.title}</CardTitle>
                      <div className="flex items-center gap-2 mt-1 text-xs flex-wrap">
                        <span className={`px-2 py-0.5 rounded border ${severityClass[p.severity] ?? severityClass.medium}`}>
                          {p.severity}
                        </span>
                        <span className={`px-2 py-0.5 rounded border ${statusClass[p.status] ?? statusClass.pending}`}>
                          {p.status}
                        </span>
                        {p.source && (
                          <span className="text-slate-500 font-mono">{p.source}{p.kb_id ? ' · ' + p.kb_id : ''}</span>
                        )}
                        <span className="text-slate-500">
                          {p.device_name || p.device_id.slice(0, 8)}
                        </span>
                        <span className="text-slate-500">
                          {new Date(p.created_at * 1000).toLocaleString()}
                        </span>
                        {p.cve && (
                          <span className="text-rose-400 font-mono">{p.cve}</span>
                        )}
                      </div>
                    </div>
                    {p.status === 'pending' && (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={actingId === p.id}
                        onClick={() => install(p)}
                      >
                        {actingId === p.id
                          ? 'Queuing…'
                          : p.source && p.kb_id
                          ? 'Install'
                          : 'Mark installed'}
                      </Button>
                    )}
                  </CardHeader>
                  {p.description && (
                    <CardContent>
                      <p className="text-sm text-slate-300 whitespace-pre-wrap">{p.description}</p>
                    </CardContent>
                  )}
                </Card>
              ))}
            </div>
          )}
        </main>
      </div>
    </AuthGuard>
  )
}
