'use client'

import { useState } from 'react'
import { useRouter } from 'next/navigation'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { portalAuth } from '@/lib/portal-api'

// Customer portal login. Deliberately separate from /login (admin) to
// keep auth scopes visually distinct as well as on the wire. Tenant ID
// is required because the portal_users table is keyed by (tenant_id,
// email).
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
    <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 flex items-center justify-center p-4">
      <Card className="bg-slate-900/60 border-slate-800/50 w-full max-w-md">
        <CardHeader>
          <CardTitle className="text-xl text-white">Customer portal</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="space-y-3">
            <div>
              <label className="block text-sm text-slate-400 mb-1">Tenant ID</label>
              <input
                type="text"
                value={form.tenant_id}
                onChange={(e) => setForm({ ...form, tenant_id: e.target.value })}
                className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white font-mono"
                placeholder="provided by your IT support"
              />
            </div>
            <div>
              <label className="block text-sm text-slate-400 mb-1">Email</label>
              <input
                type="email"
                value={form.email}
                onChange={(e) => setForm({ ...form, email: e.target.value })}
                className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white"
              />
            </div>
            <div>
              <label className="block text-sm text-slate-400 mb-1">Password</label>
              <input
                type="password"
                value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
                className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm text-white"
              />
            </div>
            <Button type="submit" disabled={submitting} className="w-full">
              {submitting ? 'Signing in…' : 'Sign in'}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
