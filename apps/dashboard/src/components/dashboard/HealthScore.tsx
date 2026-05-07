interface HealthScoreProps {
  score: number
}

export default function HealthScore({ score }: HealthScoreProps) {
  const color =
    score >= 80
      ? '#10b981'
      : score >= 60
        ? '#f59e0b'
        : score >= 40
          ? '#f97316'
          : '#f43f5e'

  const status =
    score >= 80
      ? 'All Systems Operational'
      : score >= 60
        ? 'Minor Issues Detected'
        : 'Attention Required'

  const radius = 54
  const circumference = 2 * Math.PI * radius
  const dash = (score / 100) * circumference

  return (
    <div className="relative bg-[#0a0a10] border border-white/[0.06] rounded-xl p-6 flex flex-col items-center justify-center">
      <p className="text-sm text-white/40 mb-4">Overall Health Score</p>
      <div className="relative w-40 h-40">
        <svg className="w-full h-full -rotate-90" viewBox="0 0 120 120">
          <defs>
            <linearGradient
              id={`healthGrad-${score}`}
              x1="0%"
              y1="0%"
              x2="100%"
              y2="0%"
            >
              <stop offset="0%" stopColor={color} stopOpacity="0.3" />
              <stop offset="100%" stopColor={color} stopOpacity="1" />
            </linearGradient>
            <filter id={`healthGlow-${score}`}>
              <feGaussianBlur stdDeviation="3" result="coloredBlur" />
              <feMerge>
                <feMergeNode in="coloredBlur" />
                <feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
          </defs>
          <circle
            cx="60"
            cy="60"
            r={radius}
            fill="none"
            stroke="rgba(255,255,255,0.06)"
            strokeWidth="8"
          />
          <circle
            cx="60"
            cy="60"
            r={radius}
            fill="none"
            stroke={`url(#healthGrad-${score})`}
            strokeWidth="8"
            strokeLinecap="round"
            strokeDasharray={`${dash} ${circumference}`}
            filter={`url(#healthGlow-${score})`}
            style={{ transition: 'stroke-dasharray 1s ease-out' }}
          />
        </svg>
        <div className="absolute inset-0 flex items-center justify-center">
          <div className="text-center">
            <span
              className="text-4xl font-bold font-mono"
              style={{ color }}
            >
              {score}
            </span>
            <span className="text-lg text-white/30">/100</span>
          </div>
        </div>
      </div>
      <p className="text-sm mt-4 font-medium" style={{ color }}>
        {status}
      </p>
    </div>
  )
}
