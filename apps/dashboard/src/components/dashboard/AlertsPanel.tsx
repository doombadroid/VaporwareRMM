import { Button } from '@/components/ui/button'
import {
  getAlertSeverityColor,
  getSeverityIconColor,
  getAlertTypeIcon,
  formatTimeAgo,
} from '@/lib/dashboard-utils'
import type { Alert } from '@/lib/api'

interface AlertsPanelProps {
  alerts: Alert[]
}

export default function AlertsPanel({ alerts }: AlertsPanelProps) {
  return (
    <div className="bg-[#0a0a10] border border-white/[0.06] rounded-xl">
      <div className="p-5 border-b border-white/[0.06] flex items-center justify-between">
        <div>
          <h3 className="text-base font-semibold text-white">Active Alerts</h3>
          <p className="text-xs text-white/40">
            {alerts?.length || 0} alerts requiring review
          </p>
        </div>
        <Button
          size="sm"
          variant="ghost"
          className="text-xs text-cyan-400 hover:text-cyan-300 hover:bg-cyan-500/10"
        >
          View All &rarr;
        </Button>
      </div>
      <div className="p-5 space-y-3">
        {(alerts || []).map((alert, i) => (
          <div
            key={alert.id}
            className={`group flex items-start gap-3 p-3 rounded-lg border-l-2 transition-all hover:bg-white/[0.02] ${getAlertSeverityColor(alert.severity)}`}
            style={{ animationDelay: `${i * 50}ms` }}
          >
            <span className="mt-0.5">{getAlertTypeIcon(alert.type)}</span>
            <div className="flex-1 min-w-0">
              <div className="flex items-center justify-between mb-1">
                <span className="text-sm font-medium text-white">
                  {alert.device_name}
                </span>
                <span
                  className={`text-[10px] font-bold uppercase tracking-wider ${getSeverityIconColor(alert.severity)}`}
                >
                  {alert.severity}
                </span>
              </div>
              <p className="text-xs text-white/40 mb-2 line-clamp-2">
                {alert.message}
              </p>
              <div className="flex items-center justify-between">
                <span className="text-[10px] text-white/30 font-mono">
                  {formatTimeAgo(alert.created_at)}
                </span>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-6 text-[10px] text-white/40 hover:text-white hover:bg-white/[0.06]"
                >
                  Investigate
                </Button>
              </div>
            </div>
          </div>
        ))}
        {(!alerts || alerts.length === 0) && (
          <p className="text-sm text-white/30 text-center py-8">
            No active alerts
          </p>
        )}
      </div>
    </div>
  )
}
