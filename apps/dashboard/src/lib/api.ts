import axios from 'axios';
import { toast } from 'sonner';
import { API_BASE_URL } from './config';

// Create axios instance with base configuration
const api = axios.create({
  baseURL: API_BASE_URL,
  headers: {
    'Content-Type': 'application/json',
  },
  withCredentials: true, // send httpOnly cookies
});

// Read a cookie by name
function getCookie(name: string): string | null {
  if (typeof document === 'undefined') return null;
  const match = document.cookie.match(new RegExp('(^| )' + name + '=([^;]+)'));
  return match ? decodeURIComponent(match[2]) : null;
}

// Add CSRF token to state-changing requests.
// Auth is handled by the httpOnly auth_token cookie (sent automatically via withCredentials).
api.interceptors.request.use((config) => {
  const method = config.method?.toUpperCase();
  if (method && method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
    const csrf = getCookie('csrf_token');
    if (csrf) {
      config.headers['X-CSRF-Token'] = csrf;
    }
  }
  return config;
});

// Handle auth errors + global error toast
api.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      const url: string = error.config?.url || '';
      // Don't redirect to /login if the request itself was a login/auth attempt —
      // let the calling component show its own inline error.
      const isAuthRequest =
        url.includes('/auth/login') ||
        url.includes('/auth/forgot-password') ||
        url.includes('/auth/reset-password') ||
        url.includes('/auth/login/totp');
      if (!isAuthRequest && typeof window !== 'undefined' && window.location.pathname !== '/login') {
        localStorage.removeItem('auth_expiry');
        window.location.href = '/login';
      }
    } else if (error.response?.status >= 500) {
      toast.error(error.response?.data?.error || 'Server error. Please try again.');
    }
    return Promise.reject(error);
  }
);

// Types
export interface Device {
  id: string;
  name: string;
  hostname: string;
  ip_address: string;
  mac_address: string;
  os_name: string;
  os_version: string;
  agent_version: string;
  status: string;
  last_seen: number;
  created_at: number;
  cpu?: string;
  memory?: number;
  disk_size?: number;
  tags?: string[];
  sunshine?: {
    installed: boolean;
    running: boolean;
    port: number;
  };
  tailscale?: {
    installed: boolean;
    connected: boolean;
    ip?: string;
    hostname?: string;
    peers?: number;
    backend_state?: string;
  };
}

export interface SunshineStatus {
  device_id: string;
  hostname: string;
  device_ip: string;
  sunshine: {
    installed: boolean;
    running: boolean;
    port: number;
  } | null;
  moonlight_url: string;
  web_url: string;
  moonlight_web_url?: string;
}

export interface TailscaleStatus {
  device_id: string;
  hostname: string;
  device_ip: string;
  tailscale: {
    installed: boolean;
    connected: boolean;
    ip?: string;
    hostname?: string;
    peers?: number;
    backend_state?: string;
  } | null;
}

export interface TailscaleInstallRequest {
  auth_key?: string;
  exit_node?: boolean;
}

export interface TailscaleAuthKeyRequest {
  reusable?: boolean;
  ephemeral?: boolean;
  tags?: string[];
}

export interface TailscaleAuthKeyResponse {
  auth_key: string;
  message: string;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export interface LoginResponse {
  token: string;
  user_id: string;
  email: string;
  name: string;
  requires_totp?: boolean;
  totp_challenge?: string;
}

export interface CommandRequest {
  type: string;
  command: string;
}

export interface DeviceStats {
  total: number;
  online: number;
  offline: number;
  maintenance: number;
}

export interface Ticket {
  id: string;
  title: string;
  description: string;
  status: 'open' | 'in_progress' | 'pending' | 'resolved' | 'closed';
  priority: 'low' | 'medium' | 'high' | 'critical';
  device_id?: string;
  device_name?: string;
  assigned_to?: string;
  created_at: number;
  updated_at: number;
  due_date?: number;
  category: string;
}

export interface Alert {
  id: string;
  device_id: string;
  device_name: string;
  type: 'cpu' | 'memory' | 'disk' | 'network' | 'security' | 'offline' | 'update' | 'custom';
  severity: 'info' | 'warning' | 'critical';
  message: string;
  resolved: boolean;
  created_at: number;
  resolved_at?: number;
}

export interface SystemHealth {
  cpu_usage: number;
  memory_usage: number;
  disk_usage: number;
  network_latency: number;
  uptime_hours: number;
  total_devices: number;
  online_devices: number;
  offline_devices: number;
  alert_count: number;
  ticket_count: number;
}

export interface DashboardOverview {
  device_stats: DeviceStats;
  pending_tickets: Ticket[];
  active_alerts: Alert[];
  system_health: SystemHealth;
  resource_history: Array<{ time: string; cpu: number; memory: number; disk: number }>;
}

// API Functions
export const auth = {
  login: (data: LoginRequest) =>
    api.post<LoginResponse>('/auth/login', data).then((res) => res.data),
};

export const totpApi = {
  status: () =>
    api.get<{ enabled: boolean }>('/auth/totp/status').then((res) => res.data),
  setup: () =>
    api.post<{ uri: string; secret: string; backup_codes: string[] }>('/auth/totp/setup').then((res) => res.data),
  enable: (code: string) =>
    api.post('/auth/totp/enable', { code }).then((res) => res.data),
  disable: (code: string) =>
    api.post('/auth/totp/disable', { code }).then((res) => res.data),
  verify: (totp_challenge: string, code: string) =>
    api.post<LoginResponse>('/auth/login/totp', { totp_challenge, code }).then((res) => res.data),
};

// Tenant types
export interface CurrentUser {
  id: string;
  email: string;
  name: string;
  role: 'super_admin' | 'admin' | 'user';
  created_at: number;
  tenant_id: string;
  tenant_name: string;
  impersonating?: boolean;
  original_role?: string;
  original_tenant_id?: string;
  tenant_in_grace?: boolean;
  grace_deadline?: number; // Unix seconds when access is hard-blocked
}

export interface Tenant {
  id: string;
  name: string;
  slug?: string;
  plan: string;
  status: 'active' | 'suspended';
  has_registration_key: boolean;
  max_devices: number;
  max_users: number;
  device_count: number;
  user_count: number;
  created_at: number;
  updated_at?: number;
}

export interface CreateTenantRequest {
  name: string;
  slug?: string;
  plan?: string;
  max_devices?: number;
  max_users?: number;
}

export const usersApi = {
  me: () => api.get<CurrentUser>('/users/me').then((res) => res.data),
};

export const tenantsApi = {
  list: () =>
    api.get<{ tenants: Tenant[] }>('/admin/tenants/').then((res) => res.data.tenants),
  create: (data: CreateTenantRequest) =>
    api.post<Tenant>('/admin/tenants/', data).then((res) => res.data),
  update: (id: string, data: Partial<Pick<Tenant, 'name' | 'plan' | 'status' | 'max_devices' | 'max_users'>>) =>
    api.put(`/admin/tenants/${id}`, data).then((res) => res.data),
  remove: (id: string) =>
    api.delete(`/admin/tenants/${id}`).then((res) => res.data),
  rotateRegistrationSecret: (id: string) =>
    api.post<{
      registration_secret: string;
      message: string;
      install_commands: { linux: string; macos: string; windows: string };
      server_url: string;
    }>(`/admin/tenants/${id}/registration-secret`).then((res) => res.data),
  impersonate: (id: string) =>
    api.post<{ message: string; tenant_id: string }>(`/admin/tenants/${id}/impersonate`).then((res) => res.data),
  endImpersonation: () =>
    api.post('/auth/end-impersonation').then((res) => res.data),
};

export interface InviteRequest {
  email: string;
  role?: 'admin' | 'user';
  tenant_id?: string; // super_admin only
}

export const invitesApi = {
  list: () =>
    api.get<{ invites: Array<{ id: string; tenant_id: string; email: string; role: string; status: string; expires_at: number; created_at: number }> }>('/invites').then((res) => res.data.invites),
  create: (data: InviteRequest) =>
    api.post<{ id: string; email: string; role: string; accept_url?: string; warning?: string }>('/invites', data).then((res) => res.data),
  revoke: (id: string) =>
    api.delete(`/invites/${id}`).then((res) => res.data),
};

export const devices = {
  getAll: () =>
    api.get<{ data: Device[]; total: number; limit: number; offset: number; has_more: boolean }>('/devices/')
      .then((res) => res.data.data || []),

  getById: (id: string) =>
    api.get<Device>(`/devices/${id}`).then((res) => res.data),

  create: (data: Partial<Device>) =>
    api.post<Device>('/devices/', data).then((res) => res.data),

  update: (id: string, data: Partial<Device>) =>
    api.put<Device>(`/devices/${id}`, data).then((res) => res.data),

  delete: (id: string) =>
    api.delete(`/devices/${id}`).then((res) => res.data),

  bulkDelete: (ids: string[]) =>
    api.post('/devices/bulk-delete', { ids }).then((res) => res.data),

  exportCSV: () =>
    api.get('/devices/export?format=csv', { responseType: 'blob' }).then((res) => res.data),

  sendCommand: (id: string, command: CommandRequest) =>
    api.post(`/devices/${id}/command`, command).then((res) => res.data),

  // Sunshine/Remote Desktop functions
  getSunshineStatus: (id: string) =>
    api.get<SunshineStatus>(`/devices/${id}/sunshine`).then((res) => res.data),

  installSunshine: (id: string) =>
    api.post(`/devices/${id}/sunshine/install`).then((res) => res.data),

  getSunshinePIN: (id: string) =>
    api.get<{ pin: string; device_id: string }>(`/devices/${id}/sunshine/pin`).then((res) => res.data),

  getSunshineProxyUrl: (id: string) =>
    `${API_BASE_URL}/devices/${id}/sunshine/proxy`,

  // Tailscale functions
  getTailscaleStatus: (id: string) =>
    api.get<TailscaleStatus>(`/devices/${id}/tailscale`).then((res) => res.data),

  installTailscale: (id: string, data?: TailscaleInstallRequest) =>
    api.post(`/devices/${id}/tailscale/install`, data).then((res) => res.data),

  generateTailscaleAuthKey: (id: string, data?: TailscaleAuthKeyRequest) =>
    api.post<TailscaleAuthKeyResponse>(`/devices/${id}/tailscale/auth-key`, data).then((res) => res.data),
};

export const health = {
  check: () =>
    axios.get('/health').then((res) => res.data),
};

// Dashboard API
// Branding types
export interface BrandingConfig {
  app_name: string;
  icon_url: string;
  company_name: string;
  primary_color: string;
}

export interface InstallLinks {
  app_name: string;
  company_name: string;
  icon_url: string;
  primary_color: string;
  server_url: string;
  install_options: Array<{
    name: string;
    command?: string;
    url?: string;
    platform: string;
  }>;
}

export const branding = {
  get: () =>
    api.get<BrandingConfig>('/branding/').then((res) => res.data),

  update: (data: Partial<BrandingConfig>) =>
    api.put<BrandingConfig>('/branding/', data).then((res) => res.data),

  getInstallLinks: () =>
    api.get<InstallLinks>('/branding/install-links').then((res) => res.data || { app_name: '', company_name: '', icon_url: '', primary_color: '', server_url: '', install_options: [] }),

  getInstallScript: () =>
    `${API_BASE_URL}/branding/agent-install?format=script`,
};

export const dashboard = {
  getOverview: () =>
    api.get<DashboardOverview>('/dashboard/overview').then((res) => res.data || {
      device_stats: { total: 0, online: 0, offline: 0, maintenance: 0 },
      system_health: { total_devices: 0, online_devices: 0, offline_devices: 0, alert_count: 0, ticket_count: 0, cpu_usage: 0, memory_usage: 0, disk_usage: 0, network_latency: 0, uptime_hours: 0 },
      active_alerts: [],
      pending_tickets: [],
      resource_history: [],
    }),
};

export default api;
