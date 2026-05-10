'use client'

import { useMemo, useState } from 'react'
import type { NetworkNode } from '@/lib/api'

interface TopologyGraphProps {
  nodes: NetworkNode[]
}

const MAX_NODES = 500
const SIZE = 600
const CENTER = SIZE / 2
const RING_RADIUS = SIZE * 0.42
const NODE_RADIUS = 6

// TopologyGraph renders devices as a hub-and-spoke radial layout. Server
// is the center; each device is placed around a ring at angle (i / N) * 2π.
// Edge color reflects Tailscale state (connected = green, no-tailscale =
// slate, disconnected = amber). Static layout keeps it dependency-free —
// no physics sim, no D3, no vis-network.
export default function TopologyGraph({ nodes }: TopologyGraphProps) {
  const [hover, setHover] = useState<string | null>(null)

  const { rendered, truncated } = useMemo(() => {
    const cap = nodes.slice(0, MAX_NODES)
    const items = cap.map((n, i) => {
      const angle = (i / Math.max(cap.length, 1)) * Math.PI * 2 - Math.PI / 2
      return {
        node: n,
        x: CENTER + Math.cos(angle) * RING_RADIUS,
        y: CENTER + Math.sin(angle) * RING_RADIUS,
      }
    })
    return { rendered: items, truncated: nodes.length > MAX_NODES }
  }, [nodes])

  if (rendered.length === 0) return null

  const hovered = hover ? rendered.find((r) => r.node.id === hover) : null

  return (
    <div className="relative">
      <svg viewBox={`0 0 ${SIZE} ${SIZE}`} className="w-full h-auto">
        {/* edges from center to each node */}
        {rendered.map(({ node, x, y }) => {
          const stroke = !node.tailscale_installed
            ? 'rgba(148,163,184,0.18)'
            : node.tailscale_connected
            ? 'rgba(16,185,129,0.45)'
            : 'rgba(245,158,11,0.45)'
          return (
            <line
              key={`e-${node.id}`}
              x1={CENTER}
              y1={CENTER}
              x2={x}
              y2={y}
              stroke={stroke}
              strokeWidth={hover === node.id ? 2 : 1}
            />
          )
        })}

        {/* hub */}
        <circle cx={CENTER} cy={CENTER} r={14} fill="#1e293b" stroke="#60a5fa" strokeWidth={2} />
        <text x={CENTER} y={CENTER + 4} textAnchor="middle" fontSize="10" fill="#60a5fa" fontFamily="monospace">
          server
        </text>

        {/* nodes */}
        {rendered.map(({ node, x, y }) => {
          const fill = node.status === 'online' ? '#10b981' : '#f43f5e'
          return (
            <g
              key={node.id}
              onMouseEnter={() => setHover(node.id)}
              onMouseLeave={() => setHover(null)}
              style={{ cursor: 'pointer' }}
            >
              <circle
                cx={x}
                cy={y}
                r={hover === node.id ? NODE_RADIUS + 2 : NODE_RADIUS}
                fill={fill}
                stroke="#0f172a"
                strokeWidth={2}
              />
            </g>
          )
        })}
      </svg>

      {hovered && (
        <div className="absolute top-2 right-2 bg-slate-900/95 border border-slate-700 rounded-lg p-3 max-w-xs text-xs shadow-xl">
          <p className="font-medium text-white truncate">{hovered.node.hostname || hovered.node.id.slice(0, 8)}</p>
          <p className="text-slate-400 mt-1">
            <span className={hovered.node.status === 'online' ? 'text-emerald-400' : 'text-rose-400'}>
              {hovered.node.status}
            </span>
            {hovered.node.ip_address && <> · {hovered.node.ip_address}</>}
          </p>
          {hovered.node.tailscale_installed && (
            <p className="text-slate-500 mt-1">
              tailscale: {hovered.node.tailscale_connected ? 'connected' : 'disconnected'}
              {hovered.node.tailscale_ip && <> · {hovered.node.tailscale_ip}</>}
              {hovered.node.tailscale_peers > 0 && <> · {hovered.node.tailscale_peers} peer{hovered.node.tailscale_peers === 1 ? '' : 's'}</>}
            </p>
          )}
        </div>
      )}

      {truncated && (
        <p className="text-xs text-amber-400 text-center mt-2">
          Showing first {MAX_NODES} of {nodes.length} nodes.
        </p>
      )}
    </div>
  )
}
