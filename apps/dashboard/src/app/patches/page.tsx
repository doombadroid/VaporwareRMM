'use client'

import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Pill, severityTone, statusTone, StatusDot, Code } from '@/components/ui/status'
import { FilterBar, FilterChip } from '@/components/ui/data-table'
import { Button } from '@/components/ui/button'
import { patchesApi, type Patch, type PatchStatusFilter } from '@/lib/api'

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

  useEffect(() => {
    void load(filter)
  }, [filter])

  const install = async (p: Patch) => {
    setActingId(p.id)
    try {
      if (p.source && p.kb_id) {
        await patchesApi.install(p.id)
        toast.success('Install queued')
      } else {
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

  const counts = useMemo(() => ({ shown: patches.length }), [patches])

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Manage"
          title="Patches"
          description="OS and third-party updates discovered by agents."
          separator={false}
        />

        <FilterBar>
          {FILTERS.map((f) => (
            <FilterChip
              key={f}
              label={f}
              active={filter === f}
              onClick={() => setFilter(f)}
              count={filter === f ? counts.shown : undefined}
            />
          ))}
        </FilterBar>

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : patches.length === 0 ? (
          <EmptyState
            title={`No ${filter === 'all' ? '' : filter + ' '}patches.`}
            hint="Patches are reported by agents on heartbeat."
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {patches.map((p) => (
              <li
                key={p.id}
                className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02] transition-colors"
              >
                <div className="flex flex-col gap-1.5 shrink-0 pt-0.5">
                  <Pill tone={severityTone(p.severity)}>{p.severity}</Pill>
                </div>
                <div className="min-w-0 flex-1">
                  <p className="text-[13.5px] text-white/90 font-medium truncate">{p.title}</p>
                  <div className="flex items-center gap-2 mt-1 text-[11px] text-white/35 flex-wrap">
                    <span className="inline-flex items-center gap-1">
                      <StatusDot tone={statusTone(p.status)} />
                      {p.status}
                    </span>
                    <span>·</span>
                    <span>{p.device_name || p.device_id.slice(0, 8)}</span>
                    {p.source && (
                      <>
                        <span>·</span>
                        <Code>
                          {p.source}
                          {p.kb_id ? ' ' + p.kb_id : ''}
                        </Code>
                      </>
                    )}
                    {p.cve && (
                      <>
                        <span>·</span>
                        <span className="font-mono text-rose-300">{p.cve}</span>
                      </>
                    )}
                    <span>·</span>
                    <span>{new Date(p.created_at * 1000).toLocaleString()}</span>
                  </div>
                  {p.description && (
                    <p className="text-[12px] text-white/55 mt-1.5 whitespace-pre-wrap line-clamp-2">
                      {p.description}
                    </p>
                  )}
                </div>
                {p.status === 'pending' && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={actingId === p.id}
                    onClick={() => install(p)}
                  >
                    {actingId === p.id ? 'Queuing…' : p.source && p.kb_id ? 'Install' : 'Mark installed'}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </DashboardShell>
    </AuthGuard>
  )
}
