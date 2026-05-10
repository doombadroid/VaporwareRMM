'use client'

import Link from 'next/link'
import { ChevronRight } from 'lucide-react'
import type { ReactNode } from 'react'

interface PageHeaderProps {
  title: string
  // Single-line lede above the title; usually a section name.
  eyebrow?: string
  // One short sentence under the title. Don't restate the title.
  description?: string
  // Up to ~3 trailing actions; live in a flex row at the right.
  actions?: ReactNode
  // Optional breadcrumbs above the eyebrow. Each crumb either an
  // `href` (linked) or just a `label` (the leaf, current page).
  breadcrumbs?: { href?: string; label: string }[]
  // Hairline separator below the header. Default true; index pages
  // with a filter bar under the header skip the separator.
  separator?: boolean
}

export function PageHeader({
  title,
  eyebrow,
  description,
  actions,
  breadcrumbs,
  separator = true,
}: PageHeaderProps) {
  return (
    <header className={`mb-6 ${separator ? 'pb-5 border-b border-white/[0.06]' : ''}`}>
      {breadcrumbs && breadcrumbs.length > 0 && (
        <nav className="flex items-center gap-1 text-[11px] text-white/35 mb-1.5">
          {breadcrumbs.map((c, i) => (
            <span key={i} className="flex items-center gap-1">
              {i > 0 && <ChevronRight className="w-3 h-3 text-white/20" />}
              {c.href ? (
                <Link href={c.href} className="hover:text-white/70 transition-colors">
                  {c.label}
                </Link>
              ) : (
                <span className="text-white/55">{c.label}</span>
              )}
            </span>
          ))}
        </nav>
      )}
      <div className="flex items-end justify-between gap-4 flex-wrap">
        <div className="min-w-0">
          {eyebrow && (
            <p className="text-[11px] uppercase tracking-[0.16em] text-white/35 mb-1.5 font-medium">
              {eyebrow}
            </p>
          )}
          <h1 className="text-xl font-semibold text-white tracking-tight">{title}</h1>
          {description && (
            <p className="text-[13px] text-white/45 mt-1.5 max-w-xl leading-relaxed">{description}</p>
          )}
        </div>
        {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
      </div>
    </header>
  )
}

interface SectionProps {
  title?: string
  description?: string
  actions?: ReactNode
  children: ReactNode
  className?: string
}

// Section is the lighter sibling of PageHeader: in-page heading +
// optional actions. Use when a page has multiple zones (devices,
// software, files all in the same view) instead of nested cards.
export function Section({ title, description, actions, children, className }: SectionProps) {
  return (
    <section className={`mb-8 last:mb-0 ${className ?? ''}`}>
      {(title || description || actions) && (
        <div className="flex items-end justify-between gap-3 mb-3">
          <div className="min-w-0">
            {title && (
              <h2 className="text-[13px] font-semibold text-white/90 uppercase tracking-[0.08em]">
                {title}
              </h2>
            )}
            {description && (
              <p className={`text-[12px] text-white/40 ${title ? 'mt-1' : ''}`}>{description}</p>
            )}
          </div>
          {actions && <div className="shrink-0">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  )
}

// EmptyState — one short slate-400 sentence per DESIGN.md. Optional
// action.
export function EmptyState({
  title,
  hint,
  action,
}: {
  title: string
  hint?: string
  action?: ReactNode
}) {
  return (
    <div className="border border-white/[0.06] rounded-lg bg-white/[0.01] py-12 px-6 text-center">
      <p className="text-sm text-white/55">{title}</p>
      {hint && <p className="text-[12px] text-white/30 mt-1.5">{hint}</p>}
      {action && <div className="mt-4 flex justify-center">{action}</div>}
    </div>
  )
}

// Standardize the loading state. Skeleton rows over a spinner — the
// spatial structure persists, the eye doesn't have to re-find the
// table after data lands.
export function SkeletonRows({ count = 5 }: { count?: number }) {
  return (
    <div className="border border-white/[0.06] rounded-lg overflow-hidden">
      {Array.from({ length: count }).map((_, i) => (
        <div
          key={i}
          className="h-11 border-b border-white/[0.04] last:border-0 px-4 flex items-center gap-3"
        >
          <div className="h-2 w-2 rounded-full bg-white/[0.08]" />
          <div className="h-2 w-32 rounded bg-white/[0.08]" />
          <div className="h-2 w-20 rounded bg-white/[0.06] ml-auto" />
        </div>
      ))}
    </div>
  )
}

interface KbdProps {
  children: ReactNode
}

export function Kbd({ children }: KbdProps) {
  return (
    <kbd className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] border border-white/[0.06] text-white/55">
      {children}
    </kbd>
  )
}
