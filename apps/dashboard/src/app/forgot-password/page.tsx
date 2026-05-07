'use client'

import { useState } from 'react'
import Link from 'next/link'
import { toast } from 'sonner'
import api from '@/lib/api'

export default function ForgotPasswordPage() {
  const [email, setEmail] = useState('')
  const [token, setToken] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [step, setStep] = useState<'request' | 'reset'>('request')
  const [loading, setLoading] = useState(false)

  const handleRequest = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)
    try {
      const res = await api.post('/auth/forgot-password', { email })
      toast.success(res.data.message)
      if (res.data.reset_token) {
        setToken(res.data.reset_token)
        setStep('reset')
      }
    } catch (err: any) {
      toast.error(err.response?.data?.error || 'Failed to send reset request')
    } finally {
      setLoading(false)
    }
  }

  const handleReset = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)
    try {
      await api.post('/auth/reset-password', { token, new_password: newPassword })
      toast.success('Password reset successfully. Please log in.')
      window.location.href = '/login'
    } catch (err: any) {
      toast.error(err.response?.data?.error || 'Failed to reset password')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-slate-950 flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <h1 className="text-2xl font-bold text-white">vaporRMM</h1>
          <p className="text-slate-400 mt-1 text-sm">
            {step === 'request' ? 'Reset your password' : 'Enter new password'}
          </p>
        </div>

        {step === 'request' ? (
          <form
            onSubmit={handleRequest}
            className="bg-slate-900 border border-slate-700 rounded-xl p-6 space-y-4"
          >
            <div>
              <label className="block text-sm text-slate-400 mb-1" htmlFor="email">
                Email
              </label>
              <input
                id="email"
                type="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="w-full bg-slate-800 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:border-blue-500"
                placeholder="admin@vaporrmm.local"
              />
            </div>

            <button
              type="submit"
              disabled={loading}
              className="w-full bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white font-medium py-2 px-4 rounded-lg text-sm transition-colors"
            >
              {loading ? 'Sending...' : 'Send reset link'}
            </button>

            <div className="text-center">
              <Link href="/login" className="text-xs text-blue-400 hover:text-blue-300">
                ← Back to login
              </Link>
            </div>
          </form>
        ) : (
          <form
            onSubmit={handleReset}
            className="bg-slate-900 border border-slate-700 rounded-xl p-6 space-y-4"
          >
            <div>
              <label className="block text-sm text-slate-400 mb-1" htmlFor="token">
                Reset Token
              </label>
              <input
                id="token"
                type="text"
                required
                value={token}
                onChange={(e) => setToken(e.target.value)}
                className="w-full bg-slate-800 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:border-blue-500"
              />
            </div>

            <div>
              <label className="block text-sm text-slate-400 mb-1" htmlFor="new-password">
                New Password
              </label>
              <input
                id="new-password"
                type="password"
                required
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                className="w-full bg-slate-800 border border-slate-600 rounded-lg px-3 py-2 text-white text-sm focus:outline-none focus:border-blue-500"
                placeholder="••••••••"
              />
            </div>

            <button
              type="submit"
              disabled={loading}
              className="w-full bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white font-medium py-2 px-4 rounded-lg text-sm transition-colors"
            >
              {loading ? 'Resetting...' : 'Reset password'}
            </button>

            <div className="text-center">
              <button
                type="button"
                onClick={() => setStep('request')}
                className="text-xs text-blue-400 hover:text-blue-300"
              >
                ← Back
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  )
}
