'use client'

import { useState } from 'react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { auth, totpApi } from '@/lib/api'

export default function LoginPage() {
  const router = useRouter()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [totpChallenge, setTotpChallenge] = useState('')
  const [totpCode, setTotpCode] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await auth.login({ email, password })
      if (res.requires_totp && res.totp_challenge) {
        setTotpChallenge(res.totp_challenge)
        setLoading(false)
        return
      }
      finishLogin(res.token)
    } catch {
      setError('Invalid email or password')
    } finally {
      setLoading(false)
    }
  }

  const handleTotpSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await totpApi.verify(totpChallenge, totpCode)
      finishLogin(res.token)
    } catch {
      setError('Invalid authenticator code')
    } finally {
      setLoading(false)
    }
  }

  const finishLogin = (token: string) => {
    try {
      const payload = JSON.parse(atob(token.split('.')[1]))
      if (payload.exp) localStorage.setItem('auth_expiry', String(payload.exp * 1000))
    } catch { /* ignore parse errors */ }
    router.push('/')
  }

  return (
    <div className="min-h-screen bg-slate-950 flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <h1 className="text-2xl font-bold text-white">vaporRMM</h1>
          <p className="text-slate-400 mt-1 text-sm">
            {totpChallenge ? 'Enter your authenticator code' : 'Sign in to your dashboard'}
          </p>
        </div>

        {!totpChallenge ? (
          <form
            onSubmit={handleSubmit}
            className="bg-slate-900 border border-slate-700 rounded-xl p-6 space-y-4"
          >
            {error && (
              <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg px-4 py-3">
                {error}
              </div>
            )}
            <div>
              <label className="block text-sm text-slate-400 mb-1" htmlFor="email">Email</label>
              <input
                id="email"
                type="email"
                required
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="w-full bg-slate-800 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:border-blue-500"
                placeholder="admin@vaporrmm.local"
              />
            </div>
            <div>
              <label className="block text-sm text-slate-400 mb-1" htmlFor="password">Password</label>
              <input
                id="password"
                type="password"
                required
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="w-full bg-slate-800 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:border-blue-500"
                placeholder="••••••••"
              />
            </div>
            <button
              type="submit"
              disabled={loading}
              className="w-full bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white font-medium py-2 px-4 rounded-lg text-sm transition-colors"
            >
              {loading ? 'Signing in...' : 'Sign in'}
            </button>
            <div className="text-center">
              <Link href="/forgot-password" className="text-xs text-blue-400 hover:text-blue-300">
                Forgot password?
              </Link>
            </div>
          </form>
        ) : (
          <form
            onSubmit={handleTotpSubmit}
            className="bg-slate-900 border border-slate-700 rounded-xl p-6 space-y-4"
          >
            {error && (
              <div className="bg-red-500/10 border border-red-500/30 text-red-400 text-sm rounded-lg px-4 py-3">
                {error}
              </div>
            )}
            <div>
              <label className="block text-sm text-slate-400 mb-1" htmlFor="totp">Authenticator Code</label>
              <input
                id="totp"
                type="text"
                inputMode="numeric"
                pattern="[0-9]{6}"
                maxLength={6}
                required
                autoFocus
                autoComplete="one-time-code"
                value={totpCode}
                onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ''))}
                className="w-full bg-slate-800 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:border-blue-500 text-center text-xl tracking-widest"
                placeholder="000000"
              />
            </div>
            <button
              type="submit"
              disabled={loading || totpCode.length !== 6}
              className="w-full bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white font-medium py-2 px-4 rounded-lg text-sm transition-colors"
            >
              {loading ? 'Verifying...' : 'Verify'}
            </button>
            <button
              type="button"
              onClick={() => { setTotpChallenge(''); setTotpCode(''); setError('') }}
              className="w-full text-slate-400 hover:text-white text-sm py-1"
            >
              ← Back to login
            </button>
          </form>
        )}
      </div>
    </div>
  )
}
