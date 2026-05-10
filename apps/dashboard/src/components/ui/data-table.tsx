'use client'

import { type ReactNode } from 'react'

export interface Column<T> {
  key: string
  header: string
  // Cell render. Receives the row; returns the cell content.
  render: (row: T) => ReactNode
  // Optional CSS width hint. Use sparingly; the table is fluid by
  // default and most columns size to content.
  width?: string
  // Right-align numerics.
  align?: 'left' | 'right'
  // Show in mono. Use for IDs, IPs, hostnames, anything verbatim.
  mono?: boolean
  // Mark a column as the row's primary handle. The first such column
  // is what the row click navigates from when `onRowClick` is set.
  primary?: boolean
}

interface DataTableProps<T> {
  rows: T[]
  columns: Column<T>[]
  // Stable key extractor.
  rowKey: (row: T) => string
  onRowClick?: (row: T) => void
  // Renders inside the table when rows is empty. If null, the caller
  // is rendering its own EmptyState above.
  empty?: ReactNode
  // Compact mode tightens row height for very dense tables (audit log).
  dense?: boolean
}

// DataTable. Single component for every "list of things" page. Avoids
// the ad-hoc Card + grid pattern that DESIGN.md explicitly bans. Rows
// are clickable when onRowClick is set; otherwise the row is data-only.
export function DataTable<T>({
  rows,
  columns,
  rowKey,
  onRowClick,
  empty,
  dense = false,
}: DataTableProps<T>) {
  const rowH = dense ? 'h-9' : 'h-11'
  return (
    <div className="border border-white/[0.06] rounded-lg overflow-hidden bg-white/[0.01]">
      <div className="overflow-x-auto">
        <table className="w-full text-[13px]">
          <thead className="bg-white/[0.02]">
            <tr className="border-b border-white/[0.06] text-[10.5px] uppercase tracking-[0.12em] text-white/40 font-medium">
              {columns.map((c) => (
                <th
                  key={c.key}
                  className={`text-${c.align ?? 'left'} px-3 ${dense ? 'py-1.5' : 'py-2'} font-medium`}
                  style={c.width ? { width: c.width } : undefined}
                >
                  {c.header}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td
                  colSpan={columns.length}
                  className="text-center py-12 text-[13px] text-white/40"
                >
                  {empty ?? 'Nothing here yet.'}
                </td>
              </tr>
            ) : (
              rows.map((row) => (
                <tr
                  key={rowKey(row)}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
                  className={`${rowH} border-b border-white/[0.04] last:border-0 ${onRowClick ? 'cursor-pointer hover:bg-white/[0.03]' : ''} transition-colors`}
                >
                  {columns.map((c) => (
                    <td
                      key={c.key}
                      className={`px-3 text-${c.align ?? 'left'} ${c.mono ? 'font-mono text-[12px]' : ''} ${c.align === 'right' ? 'tabular-nums' : ''} text-white/85`}
                    >
                      {c.render(row)}
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

interface FilterChipProps {
  label: string
  active: boolean
  onClick: () => void
  count?: number
}

export function FilterChip({ label, active, onClick, count }: FilterChipProps) {
  return (
    <button
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[12px] transition-colors ${
        active
          ? 'bg-white/[0.08] text-white border border-white/[0.12]'
          : 'bg-white/[0.02] text-white/55 border border-white/[0.05] hover:text-white/85 hover:border-white/[0.08]'
      }`}
    >
      <span>{label}</span>
      {typeof count === 'number' && (
        <span className={`text-[10.5px] tabular-nums ${active ? 'text-white/60' : 'text-white/35'}`}>
          {count}
        </span>
      )}
    </button>
  )
}

interface FilterBarProps {
  children: ReactNode
}

export function FilterBar({ children }: FilterBarProps) {
  return (
    <div className="flex flex-wrap items-center gap-2 mb-4 pb-4 border-b border-white/[0.04]">
      {children}
    </div>
  )
}
