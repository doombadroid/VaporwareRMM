'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { useParams, useRouter } from 'next/navigation'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Pill, severityTone, statusTone } from '@/components/ui/status'
import { ArrowLeft, Send } from 'lucide-react'
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
      toast.error('Comment failed')
    } finally {
      setPosting(false)
    }
  }

  if (loading) {
    return <div className="min-h-screen bg-[#030308] flex items-center justify-center text-white/40 text-[13px]">Loading…</div>
  }
  if (!ticket) {
    return <div className="min-h-screen bg-[#030308] flex items-center justify-center text-white/40 text-[13px]">Not found</div>
  }

  return (
    <div className="min-h-screen bg-[#030308] text-white">
      <main className="container mx-auto px-6 py-8 max-w-2xl">
        <Link
          href="/portal"
          className="inline-flex items-center gap-1.5 text-[11.5px] text-white/45 hover:text-white transition-colors mb-4"
        >
          <ArrowLeft className="w-3 h-3" />
          My tickets
        </Link>

        <header className="pb-5 mb-6 border-b border-white/[0.06]">
          <h1 className="text-xl font-semibold tracking-tight">{ticket.title}</h1>
          <div className="flex items-center gap-1.5 mt-2">
            <Pill tone={severityTone(ticket.priority)}>{ticket.priority}</Pill>
            <Pill tone={statusTone(ticket.status)}>{ticket.status}</Pill>
            <span className="text-[11.5px] text-white/35 ml-2">
              opened {new Date(ticket.created_at * 1000).toLocaleString()}
            </span>
          </div>
        </header>

        {ticket.description && (
          <div className="mb-6 border border-white/[0.06] bg-white/[0.01] rounded-lg px-4 py-3">
            <p className="text-[13px] text-white/85 whitespace-pre-wrap leading-relaxed">{ticket.description}</p>
          </div>
        )}

        <h2 className="text-[10.5px] uppercase tracking-[0.14em] text-white/40 font-medium mb-3">
          Updates ({comments.length})
        </h2>

        {comments.length === 0 ? (
          <p className="text-[13px] text-white/45 text-center py-6">No updates yet.</p>
        ) : (
          <ul className="space-y-2 mb-6">
            {comments.map((c) => (
              <li key={c.id} className="px-4 py-3 rounded-lg border border-white/[0.06] bg-white/[0.01]">
                <p className="text-[11px] text-white/35">{new Date(c.created_at * 1000).toLocaleString()}</p>
                <p className="text-[13px] text-white/85 mt-1.5 whitespace-pre-wrap leading-relaxed">{c.body}</p>
              </li>
            ))}
          </ul>
        )}

        <div className="border-t border-white/[0.06] pt-4 space-y-2">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="Add an update…"
            rows={3}
            maxLength={32 * 1024}
            className="w-full bg-white/[0.04] border border-white/[0.08] rounded-md px-3 py-2 text-[13px] text-white placeholder:text-white/30 focus:outline-none focus:border-white/[0.2]"
          />
          <div className="flex justify-end">
            <Button size="sm" onClick={post} disabled={posting || !draft.trim()}>
              <Send className="w-3.5 h-3.5 mr-1.5" />
              {posting ? 'Posting…' : 'Post update'}
            </Button>
          </div>
        </div>
      </main>
    </div>
  )
}
