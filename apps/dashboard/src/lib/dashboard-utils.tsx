import { Flame, HardDrive, Globe, Lock, WifiOff, RefreshCw, Pin, Cpu } from 'lucide-react'
import type { ReactNode } from 'react'
import type { Ticket, Alert } from './api'

export const formatTimeAgo = (timestamp: number): string => {
  const diffMs = Date.now() - timestamp
  const diffMin = Math.floor(diffMs / 60000)
  if (diffMin < 1) return 'Just now'
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  return `${Math.floor(diffHr / 24)}d ago`
}

export const getStatusColor = (status: Ticket['status']): string => {
  switch (status) {
    case 'open':
      return 'bg-cyan-500/10 text-cyan-400 border-cyan-500/20'
    case 'in_progress':
      return 'bg-amber-500/10 text-amber-400 border-amber-500/20'
    case 'pending':
      return 'bg-orange-500/10 text-orange-400 border-orange-500/20'
    case 'resolved':
      return 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20'
    default:
      return 'bg-white/5 text-white/40 border-white/10'
  }
}

export const getPriorityColor = (priority: Ticket['priority']): string => {
  switch (priority) {
    case 'critical':
      return 'bg-rose-500/10 text-rose-400 border-rose-500/20'
    case 'high':
      return 'bg-orange-500/10 text-orange-400 border-orange-500/20'
    case 'medium':
      return 'bg-amber-500/10 text-amber-400 border-amber-500/20'
    case 'low':
      return 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20'
  }
}

export const getAlertSeverityColor = (severity: Alert['severity']): string => {
  switch (severity) {
    case 'critical':
      return 'bg-rose-500/5 border-rose-500/20'
    case 'warning':
      return 'bg-amber-500/5 border-amber-500/20'
    case 'info':
      return 'bg-cyan-500/5 border-cyan-500/20'
  }
}

export const getSeverityIconColor = (severity: Alert['severity']): string => {
  switch (severity) {
    case 'critical':
      return 'text-rose-400'
    case 'warning':
      return 'text-amber-400'
    case 'info':
      return 'text-cyan-400'
  }
}

export const getAlertTypeIcon = (type: Alert['type']): ReactNode => {
  const cls = 'w-4 h-4'
  switch (type) {
    case 'cpu':
      return <Flame className={`${cls} text-rose-400`} />
    case 'memory':
      return <Cpu className={`${cls} text-violet-400`} />
    case 'disk':
      return <HardDrive className={`${cls} text-amber-400`} />
    case 'network':
      return <Globe className={`${cls} text-cyan-400`} />
    case 'security':
      return <Lock className={`${cls} text-rose-400`} />
    case 'offline':
      return <WifiOff className={`${cls} text-rose-400`} />
    case 'update':
      return <RefreshCw className={`${cls} text-emerald-400`} />
    default:
      return <Pin className={`${cls} text-white/40`} />
  }
}

export const getHealthScoreColor = (score: number): string => {
  if (score >= 80) return 'text-emerald-400'
  if (score >= 60) return 'text-amber-400'
  if (score >= 40) return 'text-orange-400'
  return 'text-rose-400'
}

export const getHealthScoreGaugeColor = (score: number): string => {
  if (score >= 80) return '#10b981'
  if (score >= 60) return '#f59e0b'
  if (score >= 40) return '#f97316'
  return '#f43f5e'
}
