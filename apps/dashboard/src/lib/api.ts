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

// Add auth token and CSRF token to requests
api.interceptors.request.use((config) => {
  const token = typeof window !== 'undefined' ? localStorage.getItem('auth_token') : null;
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  // Include CSRF token for state-changing requests
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
      if (typeof window !== 'undefined') {
        localStorage.removeItem('auth_token');
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

export const devices = {
  getAll: () =>
    api.get<Device[]>('/devices/').then((res) => res.data || []),

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
