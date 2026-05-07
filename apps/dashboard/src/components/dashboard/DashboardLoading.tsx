export default function DashboardLoading() {
  return (
    <div className="flex items-center justify-center py-32">
      <div className="text-center">
        <div className="w-12 h-12 border-2 border-cyan-500/30 border-t-cyan-400 rounded-full animate-spin mx-auto mb-4" />
        <p className="text-white/40 animate-pulse font-mono text-sm">Loading dashboard...</p>
      </div>
    </div>
  )
}
