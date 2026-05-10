'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Sheet, ConfirmDialog } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'
import { maintenanceApi, deviceGroupsApi, type MaintenanceWindow, type DeviceGroup } from '@/lib/api'

const DOW_LABELS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const COMMON_TZ = [
  'UTC',
  'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles',
  'Europe/London', 'Europe/Paris', 'Europe/Berlin',
  'Asia/Tokyo', 'Asia/Shanghai', 'Asia/Kolkata',
  'Australia/Sydney',
]

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

function describeCron(cron: string): string {
  const parts = cron.trim().split(/\s+/)
  if (parts.length !== 3) return cron
  const [m, h, d] = parts.map((p) => parseInt(p))
  if ([m, h, d].some(Number.isNaN)) return cron
  const dayName = DOW_LABELS[d] || `day ${d}`
  return `${dayName} ${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
}

export default function MaintenanceWindowsPage() {
  const [windows, setWindows] = useState<MaintenanceWindow[]>([])
  const [groups, setGroups] = useState<DeviceGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<MaintenanceWindow | null>(null)
  const [form, setForm] = useState({
    name: '',
    group_id: '',
    minute: 0,
    hour: 2,
    dow: 0,
    duration_minutes: 60,
    timezone: 'UTC',
  })

  const loadAll = async () => {
    setLoading(true)
    try {
      const [w, g] = await Promise.all([maintenanceApi.list(), deviceGroupsApi.list()])
      setWindows(w)
      setGroups(g)
    } catch {
      toast.error('Failed to load')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void loadAll()
  }, [])

  const create = async () => {
    if (!form.name) {
      toast.error('Name required')
      return
    }
    setCreating(true)
    try {
      await maintenanceApi.create({
        name: form.name,
        group_id: form.group_id || undefined,
        weekly_cron: `${form.minute} ${form.hour} ${form.dow}`,
        duration_minutes: form.duration_minutes,
        timezone: form.timezone,
      })
      toast.success('Window created')
      setShowCreate(false)
      setForm({ name: '', group_id: '', minute: 0, hour: 2, dow: 0, duration_minutes: 60, timezone: 'UTC' })
      await loadAll()
    } catch {
      toast.error('Failed to create')
    } finally {
      setCreating(false)
    }
  }

  const remove = async (w: MaintenanceWindow) => {
    try {
      await maintenanceApi.remove(w.id)
      setWindows((p) => p.filter((x) => x.id !== w.id))
      setConfirmDelete(null)
    } catch {
      toast.error('Failed to delete')
    }
  }

  const groupName = (id?: string) => {
    if (!id) return 'all devices'
    return groups.find((g) => g.id === id)?.name || id.slice(0, 8)
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Automation"
          title="Maintenance windows"
          description="Schedule patch installs and reboots inside agreed quiet hours."
          actions={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              New window
            </Button>
          }
        />

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : windows.length === 0 ? (
          <EmptyState
            title="No maintenance windows yet."
            hint="Schedule patch installs across a group during agreed quiet hours."
            action={
              <Button size="sm" onClick={() => setShowCreate(true)}>
                Create first window
              </Button>
            }
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {windows.map((w) => (
              <li key={w.id} className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02]">
                <div className="flex-1 min-w-0">
                  <p className="text-[13.5px] font-medium text-white truncate">{w.name}</p>
                  <p className="text-[12px] text-white/65 mt-1">
                    {describeCron(w.weekly_cron)} ({w.timezone}) · {w.duration_minutes} min · {groupName(w.group_id)}
                  </p>
                  <p className="text-[11px] text-white/40 mt-1">
                    {w.enabled ? <span className="text-emerald-300/85">enabled</span> : <span>disabled</span>}
                    {w.last_run_at
                      ? <> · last fired {new Date(w.last_run_at * 1000).toLocaleString()}</>
                      : <> · never fired</>}
                  </p>
                </div>
                <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(w)}>
                  Delete
                </Button>
              </li>
            ))}
          </ul>
        )}

        <Sheet
          open={showCreate}
          onClose={() => setShowCreate(false)}
          title="New maintenance window"
          description="Weekly recurrence in target timezone."
          footer={
            <>
              <Button variant="ghost" size="sm" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button size="sm" onClick={create} disabled={creating}>
                {creating ? 'Creating…' : 'Create'}
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <div>
              <label className={labelCls}>Name</label>
              <input
                placeholder="e.g. Weekly patch night"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                className={`w-full ${inputCls}`}
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className={labelCls}>Day of week</label>
                <select
                  value={form.dow}
                  onChange={(e) => setForm({ ...form, dow: parseInt(e.target.value) })}
                  className={`w-full ${inputCls}`}
                >
                  {DOW_LABELS.map((label, i) => (
                    <option key={i} value={i}>{label}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className={labelCls}>Group (optional)</label>
                <select
                  value={form.group_id}
                  onChange={(e) => setForm({ ...form, group_id: e.target.value })}
                  className={`w-full ${inputCls}`}
                >
                  <option value="">All devices</option>
                  {groups.map((g) => (
                    <option key={g.id} value={g.id}>{g.name}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className={labelCls}>Hour (0-23)</label>
                <input type="number" min={0} max={23} value={form.hour} onChange={(e) => setForm({ ...form, hour: parseInt(e.target.value) || 0 })} className={`w-full ${inputCls}`} />
              </div>
              <div>
                <label className={labelCls}>Minute (0-59)</label>
                <input type="number" min={0} max={59} value={form.minute} onChange={(e) => setForm({ ...form, minute: parseInt(e.target.value) || 0 })} className={`w-full ${inputCls}`} />
              </div>
              <div>
                <label className={labelCls}>Duration (min)</label>
                <input type="number" min={1} max={1440} value={form.duration_minutes} onChange={(e) => setForm({ ...form, duration_minutes: parseInt(e.target.value) || 60 })} className={`w-full ${inputCls}`} />
              </div>
              <div>
                <label className={labelCls}>Timezone</label>
                <select
                  value={form.timezone}
                  onChange={(e) => setForm({ ...form, timezone: e.target.value })}
                  className={`w-full ${inputCls}`}
                >
                  {COMMON_TZ.map((tz) => (
                    <option key={tz} value={tz}>{tz}</option>
                  ))}
                </select>
              </div>
            </div>
            <p className="text-[12px] text-white/45 bg-white/[0.02] border border-white/[0.06] rounded-md px-3 py-2">
              Schedule: {DOW_LABELS[form.dow]} {String(form.hour).padStart(2, '0')}:{String(form.minute).padStart(2, '0')} ({form.timezone}) for {form.duration_minutes} min
            </p>
          </div>
        </Sheet>

        <ConfirmDialog
          open={!!confirmDelete}
          onClose={() => setConfirmDelete(null)}
          onConfirm={() => confirmDelete && void remove(confirmDelete)}
          title="Delete maintenance window?"
          description={`Removes "${confirmDelete?.name || ''}" and stops scheduled execution.`}
          confirmLabel="Delete"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
