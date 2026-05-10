'use client'

import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import type { ReactNode } from 'react'

interface SheetProps {
  open: boolean
  onClose: () => void
  title: string
  description?: string
  children: ReactNode
  footer?: ReactNode
  width?: 'sm' | 'md' | 'lg'
}

const widthClass = {
  sm: 'max-w-sm',
  md: 'max-w-md',
  lg: 'max-w-lg',
}

// Sheet — right-side slide-in panel for non-destructive deep views.
// Use this instead of a modal when the user is mid-task and needs to
// glance at a record without leaving the list. Modals reserved for
// destructive confirms only.
export function Sheet({ open, onClose, title, description, children, footer, width = 'md' }: SheetProps) {
  useEffect(() => {
    if (!open) return
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [open, onClose])

  if (!open) return null
  return (
    <div className="fixed inset-0 z-50">
      <div
        className="absolute inset-0 bg-black/50 transition-opacity"
        onClick={onClose}
        aria-hidden="true"
      />
      <div
        className={`absolute right-0 top-0 bottom-0 w-full ${widthClass[width]} bg-[#0a0a10] border-l border-white/[0.08] shadow-2xl flex flex-col`}
      >
        <header className="px-5 py-4 border-b border-white/[0.06] flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-white">{title}</h2>
            {description && (
              <p className="text-[12px] text-white/45 mt-0.5">{description}</p>
            )}
          </div>
          <button
            onClick={onClose}
            className="text-white/40 hover:text-white shrink-0"
            aria-label="Close"
          >
            <X className="w-4 h-4" />
          </button>
        </header>
        <div className="flex-1 overflow-y-auto px-5 py-4">{children}</div>
        {footer && (
          <footer className="px-5 py-3 border-t border-white/[0.06] flex items-center justify-end gap-2">
            {footer}
          </footer>
        )}
      </div>
    </div>
  )
}

interface ConfirmDialogProps {
  open: boolean
  onClose: () => void
  onConfirm: () => void
  title: string
  description: string
  // Resource name the user must type to confirm. When provided, the
  // confirm button stays disabled until the input matches exactly.
  // DESIGN.md rule: destructive confirms must name the resource.
  confirmTypedValue?: string
  confirmLabel?: string
  tone?: 'danger' | 'warn'
  busy?: boolean
}

export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmTypedValue,
  confirmLabel = 'Confirm',
  tone = 'danger',
  busy = false,
}: ConfirmDialogProps) {
  const [typed, setTyped] = useState('')

  // Reset typed state every time the dialog reopens so a previous
  // typed value doesn't pre-confirm the next deletion.
  useEffect(() => {
    if (open) setTyped('')
  }, [open])

  if (!open) return null
  const ready = !confirmTypedValue || typed === confirmTypedValue
  const buttonClass =
    tone === 'danger'
      ? 'bg-rose-500 hover:bg-rose-400 text-white'
      : 'bg-amber-500 hover:bg-amber-400 text-amber-950'
  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center px-4 bg-black/60">
      <div className="w-full max-w-sm bg-[#0b0b12] border border-white/[0.08] rounded-xl shadow-2xl">
        <div className="px-5 py-4 border-b border-white/[0.06]">
          <h2 className="text-sm font-semibold text-white">{title}</h2>
          <p className="text-[12px] text-white/55 mt-1.5 leading-relaxed">{description}</p>
        </div>
        {confirmTypedValue && (
          <div className="px-5 py-3">
            <label className="block text-[11px] text-white/45 mb-1.5">
              Type <span className="font-mono text-white/85">{confirmTypedValue}</span> to confirm.
            </label>
            <input
              autoFocus
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              className="w-full bg-white/[0.04] border border-white/[0.08] rounded-md px-2.5 py-1.5 text-[13px] font-mono text-white placeholder:text-white/25 focus:outline-none focus:border-white/[0.2]"
            />
          </div>
        )}
        <div className="px-5 py-3 border-t border-white/[0.06] flex justify-end gap-2">
          <button
            onClick={onClose}
            className="px-3 py-1.5 rounded-md text-[12px] text-white/65 hover:text-white hover:bg-white/[0.04]"
          >
            Cancel
          </button>
          <button
            disabled={!ready || busy}
            onClick={onConfirm}
            className={`px-3 py-1.5 rounded-md text-[12px] font-medium disabled:opacity-50 disabled:cursor-not-allowed ${buttonClass}`}
          >
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
