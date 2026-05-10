'use client'

import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import DashboardShell from '@/components/layout/DashboardShell'
import {
  customersApi,
  devices as devicesApi,
  type CustomerUser,
  type Device,
} from '@/lib/api'

export default function CustomersPage() {
  const [customers, setCustomers] = useState<CustomerUser[]>([])
  const [devices, setDevices] = useState<Device[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ email: '', name: '', password: '', device_id: '' })

  const load = async () => {
    setLoading(true)
    try {
      const [c, d] = await Promise.all([customersApi.list(), devicesApi.getAll()])
      setCustomers(c)
      setDevices(d)
    } catch {
      toast.error('Failed to load customers (admin only?)')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

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
      setCustomers((prev) => prev.map((x) => (x.id === cu.id ? { ...x, disabled: !cu.disabled } : x)))
    } catch {
      toast.error('Failed to update')
    }
  }

  const remove = async (cu: CustomerUser) => {
    if (!confirm(`Delete customer ${cu.email}?`)) return
    try {
      await customersApi.remove(cu.id)
      setCustomers((prev) => prev.filter((x) => x.id !== cu.id))
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
        <div className="max-w-4xl space-y-6">
          <div className="flex items-center justify-between">
            <h1 className="text-2xl font-bold">Customer portal users</h1>
            <Button onClick={() => setShowCreate((s) => !s)}>
              {showCreate ? 'Cancel' : 'New customer'}
            </Button>
          </div>

          {showCreate && (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardHeader className="pb-3">
                <CardTitle className="text-base">Create customer login</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                <input
                  type="email"
                  placeholder="email"
                  value={form.email}
                  onChange={(e) => setForm({ ...form, email: e.target.value })}
                  className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
                />
                <input
                  type="text"
                  placeholder="name"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
                />
                <input
                  type="password"
                  placeholder="initial password"
                  value={form.password}
                  onChange={(e) => setForm({ ...form, password: e.target.value })}
                  className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
                />
                <div>
                  <label className="block text-xs text-slate-400 mb-1">Device scope (optional)</label>
                  <select
                    value={form.device_id}
                    onChange={(e) => setForm({ ...form, device_id: e.target.value })}
                    className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-2 py-1.5 text-sm"
                  >
                    <option value="">All tenant devices</option>
                    {devices.map((d) => <option key={d.id} value={d.id}>{d.hostname}</option>)}
                  </select>
                </div>
                <div className="flex justify-end">
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
          ) : customers.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">
                <p>No customer portal users yet.</p>
                <p className="text-sm mt-2">Create one to give end users self-service ticket access.</p>
              </CardContent>
            </Card>
          ) : (
            <div className="grid gap-3">
              {customers.map((cu) => (
                <Card key={cu.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardContent className="py-4 flex items-start justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <p className="font-medium text-white truncate">{cu.name}</p>
                      <p className="text-sm text-slate-400 truncate">{cu.email}</p>
                      <p className="text-xs text-slate-500 mt-1">
                        scope: {deviceLabel(cu.device_id)}
                        {cu.last_login ? <> · last login {new Date(cu.last_login * 1000).toLocaleString()}</> : <> · never logged in</>}
                      </p>
                    </div>
                    <div className="flex flex-col gap-2 shrink-0">
                      {cu.disabled ? (
                        <span className="px-2 py-0.5 rounded border border-amber-500/30 bg-amber-500/10 text-amber-300 text-xs">disabled</span>
                      ) : (
                        <span className="px-2 py-0.5 rounded border border-emerald-500/30 bg-emerald-500/10 text-emerald-300 text-xs">active</span>
                      )}
                      <Button size="sm" variant="ghost" onClick={() => toggleDisabled(cu)}>
                        {cu.disabled ? 'Enable' : 'Disable'}
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="border-red-500/30 text-red-400 hover:bg-red-500/10"
                        onClick={() => remove(cu)}
                      >
                        Delete
                      </Button>
                    </div>
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
