'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { useParams, useRouter } from 'next/navigation'
import { toast } from 'sonner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { portalApiClient, type PortalTicket, type PortalComment } from '@/lib/portal-api'

export default function PortalTicketPage() {
  const params = useParams()
  const router = useRouter()
  const ticketId = params.id as string
  const [ticket, setTicket] = useState<PortalTicket | null>(null)
  const [comments, setComments] = useState<PortalComment[]>([])
  const [loading, setLoading] = useState(true)
  const [draft, setDraft] = useState('')
  const [posting, setPosting] = useState(false)

  const loadAll = async () => {
    setLoading(true)
    try {
      const [t, cs] = await Promise.all([
        portalApiClient.getTicket(ticketId),
        portalApiClient.listComments(ticketId),
      ])
      setTicket(t)
      setComments(cs)
    } catch {
      router.push('/portal')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!ticketId) return
    void loadAll()
  }, [ticketId])

  const post = async () => {
    if (!draft.trim()) return
    setPosting(true)
    try {
      await portalApiClient.addComment(ticketId, draft)
      setDraft('')
      const cs = await portalApiClient.listComments(ticketId)
      setComments(cs)
    } catch {
      toast.error('Comment failed (rate-limited?)')
    } finally {
      setPosting(false)
    }
  }

  if (loading) {
    return <div className="min-h-screen flex items-center justify-center text-slate-400 bg-slate-950">Loading…</div>
  }
  if (!ticket) {
    return <div className="min-h-screen flex items-center justify-center text-slate-400 bg-slate-950">Not found</div>
  }

  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-950 via-slate-900 to-slate-800 text-white">
      <main className="container mx-auto px-6 py-8 max-w-3xl space-y-6">
        <Link href="/portal" className="text-xs text-slate-400 hover:text-white">← My tickets</Link>
        <div>
          <h1 className="text-2xl font-bold">{ticket.title}</h1>
          <p className="text-xs text-slate-500 mt-1">
            {ticket.status} · {ticket.priority} · opened {new Date(ticket.created_at * 1000).toLocaleString()}
          </p>
        </div>
        {ticket.description && (
          <Card className="bg-slate-900/60 border-slate-800/50">
            <CardContent className="py-4">
              <p className="text-sm text-slate-300 whitespace-pre-wrap">{ticket.description}</p>
            </CardContent>
          </Card>
        )}
        <Card className="bg-slate-900/60 border-slate-800/50">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">Updates ({comments.length})</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {comments.length === 0 ? (
              <p className="text-sm text-slate-500 text-center py-4">No updates yet.</p>
            ) : (
              <div className="space-y-2">
                {comments.map((c) => (
                  <div key={c.id} className="p-3 rounded-lg border border-slate-800/50 bg-slate-800/30">
                    <p className="text-xs text-slate-500">{new Date(c.created_at * 1000).toLocaleString()}</p>
                    <p className="text-sm text-slate-200 mt-1 whitespace-pre-wrap">{c.body}</p>
                  </div>
                ))}
              </div>
            )}
            <div className="border-t border-slate-800/50 pt-3 space-y-2">
              <textarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                placeholder="Add an update…"
                rows={3}
                maxLength={32 * 1024}
                className="w-full bg-slate-800/50 border border-slate-700/50 rounded-md px-3 py-2 text-sm"
              />
              <div className="flex justify-end">
                <Button onClick={post} disabled={posting || !draft.trim()}>
                  {posting ? 'Posting…' : 'Post update'}
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>
      </main>
    </div>
  )
}
