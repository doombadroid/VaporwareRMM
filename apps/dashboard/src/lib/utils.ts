import { type ClassValue, clsx } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// Mirrors the server-side brandAppNameRe in
// packages/server/internal/handlers/branding.go. Keep in sync — the
// server validates uploaded app_name against the same shape (it
// becomes a systemd unit name and /etc/<app_name> path component).
export const BRAND_APP_NAME_REGEX = /^[A-Za-z0-9._-]{1,64}$/

// brandAppNameError returns a user-facing error string for an
// invalid app_name value, or "" when the value is acceptable. Empty
// strings are treated as acceptable (the field is optional; the
// server falls back to "vaporrmm" when blank).
export function brandAppNameError(value: string): string {
  if (!value) return ""
  if (value.length > 64) return "Must be 64 characters or fewer."
  if (!BRAND_APP_NAME_REGEX.test(value)) {
    return "Letters, numbers, dashes, underscores, and periods only — no spaces or symbols."
  }
  return ""
}

// formatBytes turns a byte count into a human-readable string with
// one decimal place. Picks the right unit between MB / GB / TB; falls
// through to "B" for values under 1 MB and to "—" (em-dash) for
// zero / null / undefined. The em-dash is more conventional than
// "N/A" for missing-data table cells and less alarming for the
// common case where a device hasn't reported yet.
export function formatBytes(n?: number | null): string {
  if (!n || n <= 0) return "—"
  const TB = 1e12
  const GB = 1e9
  const MB = 1e6
  if (n >= TB) return `${(n / TB).toFixed(1)} TB`
  if (n >= GB) return `${(n / GB).toFixed(1)} GB`
  if (n >= MB) return `${(n / MB).toFixed(1)} MB`
  return `${n} B`
}

// formatOSVersion picks the most operationally useful version string
// for display: kernel_version (uname -r on Linux, kernel build on
// Windows, Darwin version on macOS) when present, otherwise
// os_version (the distro/release string, which on Linux is the
// near-useless /etc/os-release VERSION_ID like "2.18" on Gentoo).
// Agents that pre-date the kernel-version reporting commit return
// only os_version; the fallback keeps the display populated until
// every host has re-registered.
export function formatOSVersion(
  osName: string | undefined,
  osVersion: string | undefined,
  kernelVersion: string | undefined,
): string {
  const name = osName?.trim() ?? ""
  const kernel = kernelVersion?.trim() ?? ""
  const version = osVersion?.trim() ?? ""
  const v = kernel || version
  if (!name && !v) return ""
  if (!name) return v
  if (!v) return name
  return `${name} ${v}`
}

// slugifyAppName converts a human display name into a value that
// satisfies BRAND_APP_NAME_REGEX. Returns "" if no safe characters
// remain, so callers can refuse to overwrite a manually-edited
// app_name with an empty derivation.
//
// Rules (matches the task spec):
//   - lowercase
//   - whitespace → dash
//   - drop any character outside [a-z0-9._-]
//   - collapse runs of dashes
//   - trim leading/trailing dashes
//   - truncate to 64 chars
export function slugifyAppName(input: string): string {
  if (!input) return ""
  let s = input.toLowerCase()
  s = s.replace(/\s+/g, "-")
  s = s.replace(/[^a-z0-9._-]+/g, "")
  s = s.replace(/-+/g, "-")
  s = s.replace(/^-+|-+$/g, "")
  if (s.length > 64) s = s.slice(0, 64)
  // After truncating, a trailing dash could leak back in if the cut
  // landed mid-run. Trim again.
  s = s.replace(/-+$/g, "")
  return s
}