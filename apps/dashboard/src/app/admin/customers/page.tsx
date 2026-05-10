'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import { PageHeader, EmptyState } from '@/components/ui/page'
import { Pill } from '@/components/ui/status'
import { Sheet, ConfirmDialog } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'
import {
  customersApi,
  devices as devicesApi,
  type CustomerUser,
  type Device,
} from '@/lib/api'

const inputCls = 'bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-1.5 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

export default function CustomersPage() {
  const [customers, setCustomers] = useState<CustomerUser[]>([])
  const [devices, setDevices] = useState<Device[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<CustomerUser | null>(null)
  const [form, setForm] = useState({ email: '', name: '', password: '', device_id: '' })

  const load = async () => {
    setLoading(true)
    try {
      const [c, d] = await Promise.all([customersApi.list(), devicesApi.getAll()])
      setCustomers(c)
      setDevices(d)
    } catch {
      toast.error('Failed to load')
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  const create = async () => {
    if (!form.email || !form.name || !form.password) {
      toast.error('Fill all required fields')
      return
    }
    setCreating(true)
    try {
      await customersApi.create({
        email: form.email,
        name: form.name,
        password: form.password,
        device_id: form.device_id || undefined,
      })
      toast.success('Customer created')
      setShowCreate(false)
      setForm({ email: '', name: '', password: '', device_id: '' })
      await load()
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || 'Failed to create'
      toast.error(msg)
    } finally {
      setCreating(false)
    }
  }

  const toggleDisabled = async (cu: CustomerUser) => {
    try {
      await customersApi.update(cu.id, { disabled: !cu.disabled })
      setCustomers((p) => p.map((x) => (x.id === cu.id ? { ...x, disabled: !cu.disabled } : x)))
    } catch {
      toast.error('Failed to update')
    }
  }

  const remove = async (cu: CustomerUser) => {
    try {
      await customersApi.remove(cu.id)
      setCustomers((p) => p.filter((x) => x.id !== cu.id))
      setConfirmDelete(null)
    } catch {
      toast.error('Failed to delete')
    }
  }

  const deviceLabel = (id?: string) => {
    if (!id) return 'all tenant devices'
    return devices.find((d) => d.id === id)?.hostname || id.slice(0, 8)
  }

  return (
    <AuthGuard>
      <DashboardShell>
        <PageHeader
          eyebrow="Manage"
          title="Customer portal users"
          description="Self-service access for end users — see only their device and tickets."
          actions={
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="w-3.5 h-3.5 mr-1.5" />
              New customer
            </Button>
          }
        />

        {loading ? (
          <p className="text-[13px] text-white/45">Loading…</p>
        ) : customers.length === 0 ? (
          <EmptyState
            title="No customer portal users yet."
            hint="Create one to give end users self-service ticket access."
            action={
              <Button size="sm" onClick={() => setShowCreate(true)}>
                Create first customer
              </Button>
            }
          />
        ) : (
          <ul className="border border-white/[0.06] rounded-lg overflow-hidden divide-y divide-white/[0.04] bg-white/[0.01]">
            {customers.map((cu) => (
              <li key={cu.id} className="flex items-start gap-3 px-4 py-3 hover:bg-white/[0.02]">
                <div className="flex-1 min-w-0">
                  <p className="text-[13.5px] font-medium text-white truncate">{cu.name}</p>
                  <p className="text-[12px] text-white/55 truncate">{cu.email}</p>
                  <p className="text-[11px] text-white/35 mt-1">
                    scope: {deviceLabel(cu.device_id)}
                    {cu.last_login
                      ? <> · last login {new Date(cu.last_login * 1000).toLocaleString()}</>
                      : <> · never logged in</>}
                  </p>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  {cu.disabled ? <Pill tone="warn">disabled</Pill> : <Pill tone="success">active</Pill>}
                  <Button size="sm" variant="ghost" onClick={() => toggleDisabled(cu)}>
                    {cu.disabled ? 'Enable' : 'Disable'}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(cu)}>
                    Delete
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}

        <Sheet
          open={showCreate}
          onClose={() => setShowCreate(false)}
          title="New customer portal user"
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
              <label className={labelCls}>Email</label>
              <input type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} className={`w-full ${inputCls}`} />
            </div>
            <div>
              <label className={labelCls}>Name</label>
              <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className={`w-full ${inputCls}`} />
            </div>
            <div>
              <label className={labelCls}>Initial password</label>
              <input type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} className={`w-full ${inputCls}`} />
            </div>
            <div>
              <label className={labelCls}>Device scope (optional)</label>
              <select value={form.device_id} onChange={(e) => setForm({ ...form, device_id: e.target.value })} className={`w-full ${inputCls}`}>
                <option value="">All tenant devices</option>
                {devices.map((d) => (
                  <option key={d.id} value={d.id}>{d.hostname}</option>
                ))}
              </select>
            </div>
          </div>
        </Sheet>

        <ConfirmDialog
          open={!!confirmDelete}
          onClose={() => setConfirmDelete(null)}
          onConfirm={() => confirmDelete && void remove(confirmDelete)}
          title="Delete customer?"
          description={`Removes portal access for ${confirmDelete?.email || ''}.`}
          confirmLabel="Delete"
        />
      </DashboardShell>
    </AuthGuard>
  )
}
