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