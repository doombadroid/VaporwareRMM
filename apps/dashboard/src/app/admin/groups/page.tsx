'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, Section, EmptyState } from '@/components/ui/page'
import { StatusDot, statusTone } from '@/components/ui/status'
import { Sheet, ConfirmDialog } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Plus, X } from 'lucide-react'
import {
  deviceGroupsApi,
  devices as devicesApi,
  type DeviceGroup,
  type DeviceGroupMember,
  type Device,
} from '@/lib/api'

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

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
  const [confirmDelete, setConfirmDelete] = useState<DeviceGroup | null>(null)

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
  useEffect(() => {
    void loadGroups()
  }, [])

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
      toast.error('Failed to create')
    } finally {
      setCreating(false)
    }
  }

  const remove = async (g: DeviceGroup) => {
    try {
      await deviceGroupsApi.remove(g.id)
      toast.success('Group deleted')
      if (activeGroup?.id === g.id) {
        setActiveGroup(null)
        setMembers([])
      }
      setGroups((p) => p.filter((x) => x.id !== g.id))
      setConfirmDelete(null)
    } catch {
      toast.error('Failed to delete')
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
      toast.error('Failed to load devices')
    }
  }

  const addMembers = async (deviceIds: string[]) => {
    if (!activeGroup || deviceIds.length === 0) return
    try {
      await deviceGroupsApi.addMembers(activeGroup.id, deviceIds)
      toast.success(`Added ${deviceIds.length}`)
      const m = await deviceGroupsApi.members(activeGroup.id)
      setMembers(m)
    } catch {
      toast.error('Failed to add')
    }
  }

  const removeMember = async (deviceId: string) => {
    if (!activeGroup) return
    try {
      await deviceGroupsApi.removeMember(activeGroup.id, deviceId)
      setMembers((p) => p.filter((m) => m.id !== deviceId))
    } catch {
      toast.error('Failed to remove')
    }
  }

  const memberIds = new Set(members.map((m) => m.id))
  const candidates = allDevices.filter((d) => !memberIds.has(d.id))

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Manage"
          title="Device groups"
          description="Slice the fleet by site, owner, or function."
          actions={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              New group
            </Button>
          }
        />

        <div className="grid grid-cols-1 lg:grid-cols-3 gap-5">
          <Section title={`Groups (${groups.length})`} className="lg:col-span-1 mb-0">
            {loading ? (
              <p className="text-[13px] text-white/45 px-1">Loading…</p>
            ) : groups.length === 0 ? (
              <EmptyState title="No groups yet." />
            ) : (
              <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
                {groups.map((g) => (
                  <li key={g.id}>
                    <button
                      onClick={() => open(g)}
                      className={`w-full text-left px-3.5 py-2.5 transition-colors ${
                        activeGroup?.id === g.id
                          ? 'bg-white/[0.06]'
                          : 'hover:bg-white/[0.02]'
                      }`}
                    >
                      <p className="text-[13px] text-white/90 font-medium truncate">{g.name}</p>
                      {g.description && (
                        <p className="text-[11.5px] text-white/40 truncate mt-0.5">{g.description}</p>
                      )}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </Section>

          <Section
            className="lg:col-span-2 mb-0"
            title={activeGroup ? activeGroup.name : 'Members'}
            description={activeGroup ? `${members.length} ${members.length === 1 ? 'device' : 'devices'}` : undefined}
            actions={
              activeGroup && (
                <div className="flex gap-2">
                  <Button size="sm" variant="outline" onClick={openPicker}>
                    Add devices
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(activeGroup)}>
                    Delete group
                  </Button>
                </div>
              )
            }
          >
            {!activeGroup ? (
              <EmptyState title="Select a group to view its members." />
            ) : members.length === 0 ? (
              <EmptyState
                title="No devices in this group."
                action={
                  <Button size="sm" onClick={openPicker}>
                    Add devices
                  </Button>
                }
              />
            ) : (
              <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
                {members.map((m) => (
                  <li key={m.id} className="flex items-center gap-3 px-4 py-2.5 hover:bg-white/[0.02]">
                    <StatusDot tone={statusTone(m.status)} />
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] text-white/90 truncate">{m.hostname || m.id.slice(0, 8)}</p>
                      <p className="text-[11px] text-white/35 mt-0.5">
                        {m.status} · last seen {m.last_seen > 0 ? new Date(m.last_seen * 1000).toLocaleString() : 'never'}
                      </p>
                    </div>
                    <button
                      onClick={() => removeMember(m.id)}
                      className="text-white/40 hover:text-rose-300 transition-colors"
                      aria-label="Remove from group"
                    >
                      <X className="w-3.5 h-3.5" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </Section>
        </div>

        <Sheet
          open={showCreate}
          onClose={() => setShowCreate(false)}
          title="New device group"
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
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                className={`w-full ${inputCls}`}
              />
            </div>
            <div>
              <label className={labelCls}>Description (optional)</label>
              <textarea
                rows={3}
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                className={`w-full ${inputCls}`}
              />
            </div>
          </div>
        </Sheet>

        <Sheet
          open={pickerOpen && !!activeGroup}
          onClose={() => setPickerOpen(false)}
          title="Add devices"
          description={activeGroup ? `Pick devices to add to "${activeGroup.name}".` : ''}
          width="lg"
        >
          {candidates.length === 0 ? (
            <EmptyState title="All devices are already in this group." />
          ) : (
            <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04]">
              {candidates.map((d) => (
                <li key={d.id}>
                  <button
                    onClick={() => addMembers([d.id])}
                    className="w-full text-left px-3.5 py-2.5 hover:bg-white/[0.02] transition-colors flex items-center gap-3"
                  >
                    <StatusDot tone={statusTone(d.status)} />
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] text-white/90 truncate">{d.hostname}</p>
                      <p className="text-[11px] text-white/35">
                        {d.os_name} · {d.status}
                      </p>
                    </div>
                    <Plus className="w-3.5 h-3.5 text-white/45" />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Sheet>

        <ConfirmDialog
          open={!!confirmDelete}
          onClose={() => setConfirmDelete(null)}
          onConfirm={() => confirmDelete && void remove(confirmDelete)}
          title="Delete device group?"
          description={`Removes "${confirmDelete?.name || ''}" and unassigns its members. The devices themselves stay.`}
          confirmLabel="Delete"
          tone="danger"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
