'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { maintenanceApi, deviceGroupsApi, type MaintenanceWindow, type DeviceGroup } from '@/lib/api'

const DOW_LABELS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

// Common IANA timezones presented as a starter list. Operator can type
// any valid IANA name; server validates with time.LoadLocation.
const COMMON_TZ = [
  'UTC',
  'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles',
  'Europe/London', 'Europe/Paris', 'Europe/Berlin',
  'Asia/Tokyo', 'Asia/Shanghai', 'Asia/Kolkata',
  'Australia/Sydney',
]

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
  const [form, setForm] = useState({
    name: '',
    group_id: '',
    minute: 0,
    hour: 2,
    dow: 0, // Sunday
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

  useEffect(() => { void loadAll() }, [])

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
      toast.error('Failed to create (admin only?)')
    } finally {
      setCreating(false)
    }
  }

  const remove = async (w: MaintenanceWindow) => {
    if (!confirm(`Delete window "${w.name}"?`)) return
    try {
      await maintenanceApi.remove(w.id)
      setWindows((prev) => prev.filter((x) => x.id !== w.id))
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
        <div className="max-w-4xl space-y-6">
          <div className="flex items-center justify-between">
            <h1 className="text-2xl font-bold">Maintenance windows</h1>
            <Button onClick={() => setShowCreate((s) => !s)}>
              {showCreate ? 'Cancel' : 'New window'}
            </Button>
          </div>

          {showCreate && (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3">
                <CardTitle className="text-base">Create window</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                <input
                  type="text"
                  placeholder="Name (e.g. Weekly patch night)"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
                />
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-xs text-slate-400 mb-1">Day of week</label>
                    <select
                      value={form.dow}
                      onChange={(e) => setForm({ ...form, dow: parseInt(e.target.value) })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-1.5 text-sm"
                    >
                      {DOW_LABELS.map((label, i) => <option key={i} value={i}>{label}</option>)}
                    </select>
                  </div>
                  <div>
                    <label className="block text-xs text-slate-400 mb-1">Group (optional)</label>
                    <select
                      value={form.group_id}
                      onChange={(e) => setForm({ ...form, group_id: e.target.value })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-1.5 text-sm"
                    >
                      <option value="">All devices</option>
                      {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
                    </select>
                  </div>
                  <div>
                    <label className="block text-xs text-slate-400 mb-1">Hour (0-23)</label>
                    <input
                      type="number"
                      min={0}
                      max={23}
                      value={form.hour}
                      onChange={(e) => setForm({ ...form, hour: parseInt(e.target.value) || 0 })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-1.5 text-sm"
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-slate-400 mb-1">Minute (0-59)</label>
                    <input
                      type="number"
                      min={0}
                      max={59}
                      value={form.minute}
                      onChange={(e) => setForm({ ...form, minute: parseInt(e.target.value) || 0 })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-1.5 text-sm"
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-slate-400 mb-1">Duration (minutes, max 1440)</label>
                    <input
                      type="number"
                      min={1}
                      max={1440}
                      value={form.duration_minutes}
                      onChange={(e) => setForm({ ...form, duration_minutes: parseInt(e.target.value) || 60 })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-1.5 text-sm"
                    />
                  </div>
                  <div>
                    <label className="block text-xs text-slate-400 mb-1">Timezone</label>
                    <select
                      value={form.timezone}
                      onChange={(e) => setForm({ ...form, timezone: e.target.value })}
                      className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-1.5 text-sm"
                    >
                      {COMMON_TZ.map((tz) => <option key={tz} value={tz}>{tz}</option>)}
                    </select>
                  </div>
                </div>
                <p className="text-xs text-slate-500">
                  Schedule: {DOW_LABELS[form.dow]} {String(form.hour).padStart(2, '0')}:{String(form.minute).padStart(2, '0')} ({form.timezone}) for {form.duration_minutes} min
                </p>
                <div className="flex justify-end gap-2 pt-2 border-t border-slate-800/50">
                  <Button variant="ghost" onClick={() => setShowCreate(false)}>Cancel</Button>
                  <Button onClick={create} disabled={creating}>
                    {creating ? 'Creating…' : 'Create'}
                  </Button>
                </div>
              </CardContent>
            </Card>
          )}

          {loading ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">Loading…</CardContent>
            </Card>
          ) : windows.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">
                <p>No maintenance windows.</p>
                <p className="text-sm mt-2">Create one to schedule patch installs across a group.</p>
              </CardContent>
            </Card>
          ) : (
            <div className="grid gap-3">
              {windows.map((w) => (
                <Card key={w.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardContent className="py-4 flex items-start justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <p className="font-medium text-white truncate">{w.name}</p>
                      <p className="text-sm text-slate-300 mt-1">
                        {describeCron(w.weekly_cron)} ({w.timezone}) · {w.duration_minutes} min · {groupName(w.group_id)}
                      </p>
                      <p className="text-xs text-slate-500 mt-1">
                        {w.enabled ? <span className="text-emerald-400">enabled</span> : <span>disabled</span>}
                        {w.last_run_at ? <> · last fired {new Date(w.last_run_at * 1000).toLocaleString()}</> : <> · never fired</>}
                      </p>
                    </div>
                    <Button
                      size="sm"
                      variant="outline"
                      className="border-red-500/30 text-red-400 hover:bg-red-500/10"
                      onClick={() => remove(w)}
                    >
                      Delete
                    </Button>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
