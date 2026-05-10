'use client'

import { useState } from 'react'
import { useRouter } from 'next/navigation'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { portalAuth } from '@/lib/portal-api'

const inputCls = 'w-full bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-2 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]'
const labelCls = 'block text-[11px] uppercase tracking-[0.12em] text-white/40 mb-1.5'

// Customer portal login. Deliberately separate from /login (admin)
// because the auth scope is different: portal_users are tenant-scoped,
// not full users.
export default function PortalLoginPage() {
  const router = useRouter()
  const [form, setForm] = useState({ email: '', password: '', tenant_id: '' })
  const [submitting, setSubmitting] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.email || !form.password || !form.tenant_id) {
      toast.error('Fill all fields')
      return
    }
    setSubmitting(true)
    try {
      await portalAuth.login(form)
      router.push('/portal')
    } catch {
      toast.error('Login failed')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="min-h-screen bg-[#030308] flex items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 text-center">
          <p className="text-[10.5px] uppercase tracking-[0.2em] text-white/30 font-medium mb-2">vaporRMM</p>
          <h1 className="text-xl font-semibold text-white tracking-tight">Customer portal</h1>
          <p className="text-[12.5px] text-white/45 mt-1.5">Sign in to track your service requests.</p>
        </div>
        <form
          onSubmit={submit}
          className="border border-white/[0.06] bg-white/[0.01] rounded-xl p-5 space-y-4"
        >
          <div>
            <label className={labelCls}>Tenant ID</label>
            <input
              type="text"
              value={form.tenant_id}
              onChange={(e) => setForm({ ...form, tenant_id: e.target.value })}
              className={`${inputCls} font-mono`}
              placeholder="provided by your IT support"
            />
          </div>
          <div>
            <label className={labelCls}>Email</label>
            <input
              type="email"
              autoComplete="email"
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
              className={inputCls}
            />
          </div>
          <div>
            <label className={labelCls}>Password</label>
            <input
              type="password"
              autoComplete="current-password"
              value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })}
              className={inputCls}
            />
          </div>
          <Button type="submit" disabled={submitting} className="w-full">
            {submitting ? 'Signing in…' : 'Sign in'}
          </Button>
        </form>
      </div>
    </div>
  )
}
