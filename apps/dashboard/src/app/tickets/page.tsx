'use client'

import { useState } from 'react'
import Link from 'next/link'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import AuthGuard from '@/components/AuthGuard'
import { ThemeToggle } from '@/components/ThemeToggle'

interface Ticket {
  id: string
  title: string
  status: 'open' | 'in_progress' | 'resolved' | 'closed'
  priority: 'low' | 'medium' | 'high' | 'critical'
  device_name?: string
  created_at: number
}

export default function TicketsPage() {
  const [tickets] = useState<Ticket[]>([])

  return (
    <AuthGuard>
      <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
        <header className="border-b border-slate-800/50 bg-slate-950/80 backdrop-blur-xl sticky top-0 z-50">
          <div className="container mx-auto px-6 py-3">
            <div className="flex items-center justify-between">
              <Link href="/" className="text-xl font-bold bg-gradient-to-r from-blue-400 to-purple-400 bg-clip-text text-transparent">
                vaporRMM
              </Link>
              <div className="flex items-center gap-3">
                <ThemeToggle />
                <Link href="/">
                  <Button variant="ghost" size="sm" className="text-slate-400 hover:text-white">← Dashboard</Button>
                </Link>
              </div>
            </div>
          </div>
        </header>
        <main className="container mx-auto px-6 py-8">
          <h1 className="text-2xl font-bold mb-6">Tickets</h1>
          {tickets.length === 0 ? (
            <Card className="bg-slate-900/60 border-slate-800/50">
              <CardContent className="py-12 text-center text-slate-400">
                <p>No tickets yet.</p>
                <p className="text-sm mt-2">Tickets appear when agents send help requests.</p>
              </CardContent>
            </Card>
          ) : (
            <div className="grid gap-4">
              {tickets.map(t => (
                <Card key={t.id} className="bg-slate-900/60 border-slate-800/50">
                  <CardHeader className="pb-2">
                    <CardTitle className="text-base">{t.title}</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <div className="flex items-center gap-4 text-sm text-slate-400">
                      <span>{t.status}</span>
                      <span>{t.priority}</span>
                      {t.device_name && <span>{t.device_name}</span>}
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </main>
      </div>
    </AuthGuard>
  )
}
