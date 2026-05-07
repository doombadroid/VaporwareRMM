'use client'

import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { X, CheckCircle } from 'lucide-react'
import { toast } from 'sonner'
import api from '@/lib/api'

interface CreateTicketModalProps {
  open: boolean
  onClose: () => void
  onCreated?: () => void
}

export default function CreateTicketModal({ open, onClose, onCreated }: CreateTicketModalProps) {
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [priority, setPriority] = useState<'low' | 'medium' | 'high' | 'critical'>('medium')
  const [submitting, setSubmitting] = useState(false)

  if (!open) return null

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!title.trim()) {
      toast.error('Title is required')
      return
    }
    setSubmitting(true)
    try {
      await api.post('/tickets', { title, description, priority })
      toast.success('Ticket created')
      setTitle('')
      setDescription('')
      onClose()
      onCreated?.()
    } catch (err: any) {
      toast.error(err.response?.data?.error || 'Failed to create ticket')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm" onClick={onClose}>
      <div className="bg-[#0a0a10] border border-white/[0.08] rounded-2xl shadow-2xl max-w-md w-full mx-4" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between p-6 border-b border-white/[0.06]">
          <div>
            <h2 className="text-lg font-semibold text-white">New Ticket</h2>
            <p className="text-sm text-white/40">Create a support ticket</p>
          </div>
          <button onClick={onClose} className="text-white/40 hover:text-white transition-colors">
            <X className="w-5 h-5" />
          </button>
        </div>
        <form onSubmit={handleSubmit} className="p-6 space-y-4">
          <div>
            <label className="block text-sm font-medium text-white/60 mb-1">Title</label>
            <input
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/20 focus:outline-none focus:border-cyan-500/40"
              placeholder="e.g. Disk space warning on server-01"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-white/60 mb-1">Description</label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={3}
              className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white placeholder:text-white/20 focus:outline-none focus:border-cyan-500/40 resize-none"
              placeholder="Additional details..."
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-white/60 mb-1">Priority</label>
            <select
              value={priority}
              onChange={(e) => setPriority(e.target.value as any)}
              className="w-full bg-white/[0.04] border border-white/[0.08] rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:border-cyan-500/40"
            >
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </select>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button type="button" variant="ghost" onClick={onClose} className="text-sm">
              Cancel
            </Button>
            <Button type="submit" disabled={submitting} className="text-sm bg-cyan-600 hover:bg-cyan-500 text-white">
              {submitting ? 'Creating...' : 'Create Ticket'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}
