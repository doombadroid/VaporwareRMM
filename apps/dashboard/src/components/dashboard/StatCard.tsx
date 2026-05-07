'use client'

import { useEffect, useState } from 'react'
import { type LucideIcon, TrendingUp, TrendingDown } from 'lucide-react'

interface StatCardProps {
  title: string
  value: number
  icon: LucideIcon
  trend?: {
    direction: 'up' | 'down'
    percentage: number
  }
  progress?: number
  accent?: 'cyan' | 'violet' | 'emerald' | 'amber' | 'rose' | 'blue'
}

const accentMap = {
  cyan: {
    text: 'text-cyan-400',
    bg: 'bg-cyan-500/10',
    bar: 'bg-cyan-400',
    glow:
      'group-hover:shadow-[0_0_20px_rgba(0,240,255,0.12)] group-hover:border-cyan-500/20',
  },
  violet: {
    text: 'text-violet-400',
    bg: 'bg-violet-500/10',
    bar: 'bg-violet-400',
    glow:
      'group-hover:shadow-[0_0_20px_rgba(139,92,246,0.12)] group-hover:border-violet-500/20',
  },
  emerald: {
    text: 'text-emerald-400',
    bg: 'bg-emerald-500/10',
    bar: 'bg-emerald-400',
    glow:
      'group-hover:shadow-[0_0_20px_rgba(16,185,129,0.12)] group-hover:border-emerald-500/20',
  },
  amber: {
    text: 'text-amber-400',
    bg: 'bg-amber-500/10',
    bar: 'bg-amber-400',
    glow:
      'group-hover:shadow-[0_0_20px_rgba(245,158,11,0.12)] group-hover:border-amber-500/20',
  },
  rose: {
    text: 'text-rose-400',
    bg: 'bg-rose-500/10',
    bar: 'bg-rose-400',
    glow:
      'group-hover:shadow-[0_0_20px_rgba(244,63,94,0.12)] group-hover:border-rose-500/20',
  },
  blue: {
    text: 'text-blue-400',
    bg: 'bg-blue-500/10',
    bar: 'bg-blue-400',
    glow:
      'group-hover:shadow-[0_0_20px_rgba(96,165,250,0.12)] group-hover:border-blue-500/20',
  },
}

export default function StatCard({
  title,
  value,
  icon: Icon,
  trend,
  progress,
  accent = 'cyan',
}: StatCardProps) {
  const [displayValue, setDisplayValue] = useState(0)

  useEffect(() => {
    let raf: number
    const duration = 800
    const startTime = performance.now()
    const animate = (now: number) => {
      const elapsed = now - startTime
      const p = Math.min(elapsed / duration, 1)
      setDisplayValue(Math.floor(p * value))
      if (p < 1) raf = requestAnimationFrame(animate)
    }
    raf = requestAnimationFrame(animate)
    return () => cancelAnimationFrame(raf)
  }, [value])

  const a = accentMap[accent]

  return (
    <div
      className={`group relative bg-[#0a0a10] border border-white/[0.06] rounded-xl p-5 transition-all duration-300 hover:-translate-y-0.5 hover:border-white/[0.12] ${a.glow}`}
    >
      <div className="flex items-start justify-between mb-4">
        <div
          className={`w-10 h-10 rounded-xl ${a.bg} flex items-center justify-center`}
        >
          <Icon className={`w-5 h-5 ${a.text}`} />
        </div>
        {trend && (
          <div
            className={`flex items-center gap-1 text-xs font-medium px-2 py-1 rounded-full ${
              trend.direction === 'up'
                ? 'text-emerald-400 bg-emerald-500/10'
                : 'text-rose-400 bg-rose-500/10'
            }`}
          >
            {trend.direction === 'up' ? (
              <TrendingUp className="w-3 h-3" />
            ) : (
              <TrendingDown className="w-3 h-3" />
            )}
            {trend.percentage}%
          </div>
        )}
      </div>
      <div className="space-y-1">
        <p className="text-2xl font-bold font-mono text-white">{displayValue}</p>
        <p className="text-sm text-white/40">{title}</p>
      </div>
      {typeof progress === 'number' && (
        <div className="mt-4 w-full bg-white/[0.04] rounded-full h-1">
          <div
            className={`h-1 rounded-full transition-all duration-1000 ${a.bar}`}
            style={{ width: `${Math.min(progress, 100)}%` }}
          />
        </div>
      )}
    </div>
  )
}
