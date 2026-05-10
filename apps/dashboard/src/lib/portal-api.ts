import axios from 'axios'
import { API_BASE_URL } from './config'

// portalApi is intentionally separate from the admin axios instance.
// The shared dashboard `api` client carries the admin auth_token cookie
// scoped to /api/v1; portal scope uses portal_token at /api/v1/portal.
// Mixing them would break the hard auth boundary the server-side
// PortalAuthMiddleware relies on.
const portalApi = axios.create({
  baseURL: API_BASE_URL,
  headers: { 'Content-Type': 'application/json' },
  withCredentials: true,
})

// Read a cookie by name (server sets csrf_token at /).
function readCookie(name: string): string | null {
  if (typeof document === 'undefined') return null
  const m = document.cookie.match(new RegExp('(^| )' + name + '=([^;]+)'))
  return m ? decodeURIComponent(m[2]) : null
}

// Mirror the admin axios CSRF interceptor — portalAPI runs CSRFMiddleware
// on the server. The csrf_token cookie is minted on portal login and is
// JS-readable so we can echo it via X-CSRF-Token. Server enforces strict
// double-submit equality.
portalApi.interceptors.request.use((config) => {
  const method = config.method?.toUpperCase()
  if (method && method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
    const csrf = readCookie('csrf_token')
    if (csrf) config.headers['X-CSRF-Token'] = csrf
  }
  return config
})

export interface PortalSelf {
  id: string
  email: string
  name: string
  tenant_id: string
  device_id?: string
}

export interface PortalTicket {
  id: string
  title: string
  description?: string
  status: string
  priority: string
  device_id?: string
  category?: string
  created_at: number
  updated_at: number
}

export interface PortalComment {
  id: string
  body: string
  created_at: number
}

// Login + logout live at /api/portal/* (publicAPI), authenticated calls
// at /api/v1/portal/* (portalAPI). API_BASE_URL points at /api/v1; we
// strip /v1 for the unauthenticated entry points.
const PUBLIC_BASE = API_BASE_URL.replace(/\/v1$/, '')

export const portalAuth = {
  login: (data: { email: string; password: string; tenant_id: string }) =>
    axios
      .post<PortalSelf>(`${PUBLIC_BASE}/portal/login`, data, { withCredentials: true })
      .then((r) => r.data),
  logout: () =>
    axios
      .post(`${PUBLIC_BASE}/portal/logout`, {}, { withCredentials: true })
      .then((r) => r.data),
}

export const portalApiClient = {
  me: () => portalApi.get<PortalSelf>('/portal/me').then((r) => r.data),
  listTickets: () =>
    portalApi.get<{ tickets: PortalTicket[] }>('/portal/tickets').then((r) => r.data?.tickets ?? []),
  getTicket: (id: string) =>
    portalApi.get<PortalTicket>(`/portal/tickets/${id}`).then((r) => r.data),
  createTicket: (data: { title: string; description?: string }) =>
    portalApi.post<{ id: string }>('/portal/tickets', data).then((r) => r.data),
  listComments: (id: string) =>
    portalApi.get<{ comments: PortalComment[] }>(`/portal/tickets/${id}/comments`).then((r) => r.data?.comments ?? []),
  addComment: (id: string, body: string) =>
    portalApi.post<{ id: string }>(`/portal/tickets/${id}/comments`, { body }).then((r) => r.data),
}

export default portalApi
