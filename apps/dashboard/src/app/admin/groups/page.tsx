'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import {
  deviceGroupsApi,
  devices as devicesApi,
  type DeviceGroup,
  type DeviceGroupMember,
  type Device,
} from '@/lib/api'

export default function GroupsPage() {
  const [groups, setGroups] = useState<DeviceGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ name: '', description: '' })
  const [creating, setCreating] = useState(false)
  const [activeGroup, setActiveGroup] = useState<DeviceGroup | null>(null)
  const [members, setMembers] = useState<DeviceGroupMember[]>([])
  const [allDevices, setAllDevices] = useState<Device[]>([])
  const [pickerOpen, setPickerOpen] = useState(false)

  const loadGroups = async () => {
    setLoading(true)
    try {
      setGroups(await deviceGroupsApi.list())
    } catch {
      toast.error('Failed to load groups')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void loadGroups() }, [])

  const create = async () => {
    if (!form.name) {
      toast.error('Name required')
      return
    }
    setCreating(true)
    try {
      await deviceGroupsApi.create(form)
      toast.success('Group created')
      setForm({ name: '', description: '' })
      setShowCreate(false)
      await loadGroups()
    } catch {
      toast.error('Failed to create group (admin only?)')
    } finally {
      setCreating(false)
    }
  }

  const remove = async (g: DeviceGroup) => {
    if (!confirm(`Delete group "${g.name}"?`)) return
    try {
      await deviceGroupsApi.remove(g.id)
      toast.success('Group deleted')
      if (activeGroup?.id === g.id) {
        setActiveGroup(null)
        setMembers([])
      }
      setGroups((prev) => prev.filter((x) => x.id !== g.id))
    } catch {
      toast.error('Failed to delete group')
    }
  }

  const open = async (g: DeviceGroup) => {
    setActiveGroup(g)
    try {
      const m = await deviceGroupsApi.members(g.id)
      setMembers(m)
    } catch {
      toast.error('Failed to load members')
    }
  }

  const openPicker = async () => {
    if (!activeGroup) return
    try {
      const list = await devicesApi.getAll()
      setAllDevices(list)
      setPickerOpen(true)
    } catch {
      toast.error('Failed to load device list')
    }
  }

  const addMembers = async (deviceIds: string[]) => {
    if (!activeGroup || deviceIds.length === 0) return
    try {
      await deviceGroupsApi.addMembers(activeGroup.id, deviceIds)
      toast.success(`Added ${deviceIds.length} device${deviceIds.length === 1 ? '' : 's'}`)
      setPickerOpen(false)
      const m = await deviceGroupsApi.members(activeGroup.id)
      setMembers(m)
    } catch {
      toast.error('Failed to add members')
    }
  }

  const removeMember = async (deviceId: string) => {
    if (!activeGroup) return
    try {
      await deviceGroupsApi.removeMember(activeGroup.id, deviceId)
      setMembers((prev) => prev.filter((m) => m.id !== deviceId))
    } catch {
      toast.error('Failed to remove')
    }
  }

  const memberIds = new Set(members.map((m) => m.id))
  const candidates = allDevices.filter((d) => !memberIds.has(d.id))

  return (
    <AuthGuard>
      <DashboardShell>
        <div className="space-y-6 max-w-5xl">
          <div className="flex items-center justify-between">
            <h1 className="text-2xl font-bold">Device groups</h1>
            <Button onClick={() => setShowCreate((s) => !s)}>
              {showCreate ? 'Cancel' : 'New group'}
            </Button>
          </div>

          {showCreate && (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="space-y-3 py-4">
                <input
                  type="text"
                  placeholder="Group name"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
                />
                <textarea
                  placeholder="Description (optional)"
                  rows={2}
                  value={form.description}
                  onChange={(e) => setForm({ ...form, description: e.target.value })}
                  className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
                />
                <div className="flex justify-end">
                  <Button onClick={create} disabled={creating}>
                    {creating ? 'Creating…' : 'Create'}
                  </Button>
                </div>
              </CardContent>
            </Card>
          )}

          <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm">Groups ({groups.length})</CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                {loading ? (
                  <p className="px-4 py-6 text-center text-slate-400 text-sm">Loading…</p>
                ) : groups.length === 0 ? (
                  <p className="px-4 py-6 text-center text-slate-400 text-sm">No groups yet.</p>
                ) : (
                  <div className="divide-y divide-slate-800/50">
                    {groups.map((g) => (
                      <button
                        key={g.id}
                        onClick={() => open(g)}
                        className={`w-full text-left px-4 py-2 hover:bg-slate-800/40 ${activeGroup?.id === g.id ? 'bg-blue-500/10 border-l-2 border-blue-400' : ''}`}
                      >
                        <p className="text-sm font-medium text-slate-200 truncate">{g.name}</p>
                        {g.description && <p className="text-xs text-slate-500 truncate">{g.description}</p>}
                      </button>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>

            <Card className="lg:col-span-2 bg-slate-900/60 border-slate-800/50">
              {!activeGroup ? (
                <CardContent className="py-12 text-center text-slate-400 text-sm">
                  Select a group to view members.
                </CardContent>
              ) : (
                <>
                  <CardHeader className="pb-3 flex flex-row items-center justify-between gap-2">
                    <CardTitle className="text-sm">{activeGroup.name} ({members.length} member{members.length === 1 ? '' : 's'})</CardTitle>
                    <div className="flex gap-2">
                      <Button size="sm" onClick={openPicker}>Add members</Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="border-red-500/30 text-red-400 hover:bg-red-500/10"
                        onClick={() => remove(activeGroup)}
                      >
                        Delete group
                      </Button>
                    </div>
                  </CardHeader>
                  <CardContent className="p-0">
                    {members.length === 0 ? (
                      <p className="px-4 py-6 text-center text-slate-400 text-sm">No devices in this group.</p>
                    ) : (
                      <div className="divide-y divide-slate-800/50">
                        {members.map((m) => (
                          <div key={m.id} className="px-4 py-2 flex items-center justify-between gap-3">
                            <div className="min-w-0">
                              <p className="text-sm text-slate-200 truncate">{m.hostname || m.id.slice(0, 8)}</p>
                              <p className="text-xs text-slate-500">
                                {m.status} · last seen {m.last_seen > 0 ? new Date(m.last_seen * 1000).toLocaleString() : 'never'}
                              </p>
                            </div>
                            <Button size="sm" variant="ghost" onClick={() => removeMember(m.id)}>Remove</Button>
                          </div>
                        ))}
                      </div>
                    )}
                  </CardContent>
                </>
              )}
            </Card>
          </div>

          {pickerOpen && activeGroup && (
            <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={() => setPickerOpen(false)}>
              <div className="bg-slate-900 border border-slate-700 rounded-xl max-w-lg w-full mx-4 max-h-[80vh] overflow-hidden flex flex-col" onClick={(e) => e.stopPropagation()}>
                <div className="px-4 py-3 border-b border-slate-700 flex items-center justify-between">
                  <p className="font-medium">Add devices to &ldquo;{activeGroup.name}&rdquo;</p>
                  <button className="text-slate-400" onClick={() => setPickerOpen(false)}>×</button>
                </div>
                <div className="overflow-y-auto p-3 space-y-1">
                  {candidates.length === 0 ? (
                    <p className="text-sm text-slate-400 text-center py-6">All devices already in this group.</p>
                  ) : (
                    candidates.map((d) => (
                      <button
                        key={d.id}
                        onClick={() => addMembers([d.id])}
                        className="w-full text-left px-3 py-2 rounded hover:bg-slate-800/50"
                      >
                        <p className="text-sm">{d.hostname}</p>
                        <p className="text-xs text-slate-500">{d.os_name} · {d.status}</p>
                      </button>
                    ))
                  )}
                </div>
              </div>
            </div>
          )}
        </div>
      </DashboardShell>
    </AuthGuard>
  )
}
