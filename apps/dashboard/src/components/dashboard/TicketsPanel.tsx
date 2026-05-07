import Link from 'next/link'
import { Monitor, User } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  getStatusColor,
  getPriorityColor,
  formatTimeAgo,
} from '@/lib/dashboard-utils'
import type { Ticket } from '@/lib/api'

interface TicketsPanelProps {
  tickets: Ticket[]
}

export default function TicketsPanel({ tickets }: TicketsPanelProps) {
  return (
    <div className="bg-[#0a0a10] border border-white/[0.06] rounded-xl">
      <div className="p-5 border-b border-white/[0.06] flex items-center justify-between">
        <div>
          <h3 className="text-base font-semibold text-white">
            Pending Tickets
          </h3>
          <p className="text-xs text-white/40">
            {tickets?.length || 0} tickets requiring attention
          </p>
        </div>
        <Link href="/tickets">
          <Button
            size="sm"
            variant="ghost"
            className="text-xs text-cyan-400 hover:text-cyan-300 hover:bg-cyan-500/10"
          >
            View All &rarr;
          </Button>
        </Link>
      </div>
      <div className="p-5 space-y-3">
        {(tickets || []).map((ticket, i) => (
          <Link href={`/tickets/${ticket.id}`} key={ticket.id}>
            <div
              className="group p-3 rounded-lg border border-white/[0.04] hover:border-white/[0.10] hover:bg-white/[0.02] transition-all cursor-pointer"
              style={{ animationDelay: `${i * 50}ms` }}
            >
              <div className="flex items-start justify-between gap-3 mb-2">
                <div className="flex items-start gap-2 flex-1 min-w-0">
                  <span className="text-[10px] font-mono text-white/30 mt-0.5">
                    {ticket.id}
                  </span>
                  <h4 className="text-sm font-medium text-white group-hover:text-cyan-400 transition-colors truncate">
                    {ticket.title}
                  </h4>
                </div>
                <span
                  className={`px-2 py-0.5 rounded-full text-[10px] font-medium border flex-shrink-0 ${getPriorityColor(ticket.priority)}`}
                >
                  {(ticket.priority || '').toUpperCase()}
                </span>
              </div>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-3 text-xs text-white/30">
                  {ticket.device_name && (
                    <span className="flex items-center gap-1">
                      <Monitor className="w-3 h-3" />
                      {ticket.device_name}
                    </span>
                  )}
                  {ticket.assigned_to && (
                    <span className="flex items-center gap-1">
                      <User className="w-3 h-3" />
                      {ticket.assigned_to}
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  <span
                    className={`px-2 py-0.5 rounded-full text-[10px] font-medium border ${getStatusColor(ticket.status)}`}
                  >
                    {(ticket.status || '').replace('_', ' ')}
                  </span>
                  <span className="text-[10px] text-white/20 font-mono">
                    {formatTimeAgo(ticket.created_at)}
                  </span>
                </div>
              </div>
            </div>
          </Link>
        ))}
        {(!tickets || tickets.length === 0) && (
          <p className="text-sm text-white/30 text-center py-8">
            No pending tickets
          </p>
        )}
      </div>
    </div>
  )
}
