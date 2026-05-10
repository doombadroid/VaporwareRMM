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
  uptime_hours: number;
  total_devices: number;
  online_devices: number;
  offline_devices: number;
  alert_count: number;
  ticket_count: number;
}

export interface RecentActivityEntry {
  action: string;
  resource_type: string;
  created_at: number;
}

export interface SLAMetrics {
  window_days: number;
  online_pct: number;
  resolution_rate_pct: number;
  resolved_count: number;
  created_count: number;
  avg_response_minutes: number;
}

export interface DashboardOverview {
  device_stats: DeviceStats;
  pending_tickets: Ticket[];
  active_alerts: Alert[];
  system_health: SystemHealth;
  resource_history: Array<{ time: string; cpu: number; memory: number; disk: number }>;
  recent_activity?: RecentActivityEntry[];
  sla?: SLAMetrics;
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
      device_stats: { total: 0, online: 0, offline: 0 },
      system_health: { total_devices: 0, online_devices: 0, offline_devices: 0, alert_count: 0, ticket_count: 0, cpu_usage: 0, memory_usage: 0, disk_usage: 0, uptime_hours: 0 },
      active_alerts: [],
      pending_tickets: [],
      resource_history: [],
      recent_activity: [],
    }),
};

export const ticketsApi = {
  list: () =>
    api.get<{ tickets: Ticket[] }>('/tickets').then((r) => r.data?.tickets ?? []),
  get: (id: string) =>
    api.get<{ ticket: Ticket }>(`/tickets/${id}`).then((r) => r.data?.ticket),
  create: (data: { title: string; description?: string; priority?: string; device_id?: string; category?: string }) =>
    api.post<{ id: string }>('/tickets', data).then((r) => r.data),
  update: (id: string, data: Partial<Pick<Ticket, 'title' | 'description' | 'status' | 'priority' | 'assigned_to'>>) =>
    api.put(`/tickets/${id}`, data).then((r) => r.data),
  remove: (id: string) =>
    api.delete(`/tickets/${id}`).then((r) => r.data),
};

export const alertsApi = {
  list: (includeResolved = false) =>
    api.get<{ alerts: Alert[] }>(`/alerts${includeResolved ? '?include_resolved=1' : ''}`).then((r) => r.data?.alerts ?? []),
  resolve: (id: string) =>
    api.post(`/alerts/${id}/resolve`).then((r) => r.data),
};

// ── Patches (Stage 2) ────────────────────────────────────────────────

export interface Patch {
  id: string;
  device_id: string;
  device_name?: string;
  title: string;
  description: string;
  severity: 'low' | 'medium' | 'high' | 'critical';
  status: 'pending' | 'installed' | 'failed';
  installed_at?: number;
  created_at: number;
}

export type PatchStatusFilter = 'pending' | 'installed' | 'failed' | 'all';

export const patchesApi = {
  list: (status: PatchStatusFilter = 'pending') =>
    api.get<{ patches: Patch[] }>(`/patches?status=${encodeURIComponent(status)}`).then((r) => r.data?.patches ?? []),
  updateStatus: (id: string, status: 'installed' | 'failed' | 'pending') =>
    api.put(`/patches/${id}`, { status }).then((r) => r.data),
};

// ── Network topology (Stage 2) ───────────────────────────────────────

export interface NetworkNode {
  id: string;
  hostname: string;
  ip_address: string;
  status: string;
  last_seen: number;
  tailscale_installed: boolean;
  tailscale_connected: boolean;
  tailscale_ip?: string;
  tailscale_hostname?: string;
  tailscale_peers: number;
  tailscale_backend_state?: string;
}

export interface NetworkTopology {
  nodes: NetworkNode[];
  total: number;
  tailscale_installed: number;
  tailscale_connected: number;
}

export const networkApi = {
  getTopology: () =>
    api.get<NetworkTopology>('/network/topology').then((r) => r.data),
};

// ── AI ───────────────────────────────────────────────────────────────────

export interface AITenantSettings {
  tenant_id: string;
  ai_enabled: boolean;
  ai_billing_mode: 'absorb' | 'passthrough';
  ai_max_chat_cost_per_day_micros: number;
  ai_max_embedding_cost_per_day_micros: number;
  ai_dpa_acknowledged_at: number | null;
}

export interface AIProvider {
  id: string;
  kind: string;
  name: string;
  base_url: string;
  region: string;
  model_trust_level: 'local' | 'external' | 'self_hosted';
  enabled: boolean;
  created_at: number;
  updated_at: number;
}

export interface AIRoutingRule {
  id: string;
  task_type: 'classify' | 'reason' | 'summarize' | 'embed' | 'generate';
  preferred_provider_id: string;
  fallback_provider_id: string;
  model_name: string;
  embedding_model_name: string;
  max_cost_per_call_micros: number;
  max_input_tokens: number;
  max_output_tokens: number;
  cost_per_1k_input_micros: number;
  cost_per_1k_output_micros: number;
}

export interface AICapability {
  name: string;
  category: 'observation' | 'assistance' | 'action';
  description: string;
  stage: number;
  depends_on: string[];
  unmet_dependencies: string[];
  required_caps: { Streaming: boolean; ToolCalling: boolean; Embeddings: boolean; JSONMode: boolean; MaxContext: number };
  preferred_task_type: string;
  enabled: boolean;
  rung: 'shadow' | 'suggest' | 'act_low' | 'act_policy' | 'autonomous';
  scope_filter: { customer_ids?: string[]; device_class_includes?: string[]; device_class_excludes?: string[]; device_tag_excludes?: string[] };
  confidence_threshold: number;
  blast_radius_max_devices: number;
  blast_radius_window_minutes: number;
  kill_switch: boolean;
}

export interface AIRun {
  id: string;
  capability_id: string;
  run_type: 'chat' | 'embed' | 'tool_call';
  model_name: string;
  model_version: string;
  model_trust_level: string;
  prompt_tokens: number;
  output_tokens: number;
  cost_usd_micros: number;
  latency_ms: number;
  rung_at_call: string;
  outcome: string;
  rollback_attempted: boolean;
  rollback_succeeded: boolean;
  created_at: number;
}

export interface AIKillSwitch {
  scope: string;
  enabled: boolean;
  reason: string;
  set_by_user_id: string;
  set_at: number;
}

export const aiApi = {
  getTenant: () =>
    api.get<AITenantSettings>('/admin/ai/tenant').then((r) => r.data),
  patchTenant: (data: Partial<{ ai_enabled: boolean; ai_billing_mode: string; ai_max_chat_cost_per_day_micros: number; ai_max_embedding_cost_per_day_micros: number; acknowledge_dpa: boolean }>) =>
    api.patch('/admin/ai/tenant', data).then((r) => r.data),

  listProviders: () =>
    api.get<{ providers: AIProvider[]; kinds: string[] }>('/admin/ai/providers').then((r) => r.data),
  createProvider: (data: { kind: string; name: string; base_url?: string; api_key?: string; region?: string; model_trust_level?: string; enabled?: boolean }) =>
    api.post<{ id: string }>('/admin/ai/providers', data).then((r) => r.data),
  patchProvider: (id: string, data: Partial<{ name: string; base_url: string; api_key: string; region: string; model_trust_level: string; enabled: boolean }>) =>
    api.patch(`/admin/ai/providers/${id}`, data).then((r) => r.data),
  deleteProvider: (id: string) =>
    api.delete(`/admin/ai/providers/${id}`).then((r) => r.data),

  listRouting: () =>
    api.get<{ routing_rules: AIRoutingRule[] }>('/admin/ai/routing').then((r) => r.data.routing_rules),
  createRouting: (data: Partial<AIRoutingRule>) =>
    api.post<{ id: string }>('/admin/ai/routing', data).then((r) => r.data),
  patchRouting: (id: string, data: Partial<AIRoutingRule>) =>
    api.patch(`/admin/ai/routing/${id}`, data).then((r) => r.data),
  deleteRouting: (id: string) =>
    api.delete(`/admin/ai/routing/${id}`).then((r) => r.data),

  listCapabilities: () =>
    api.get<{ capabilities: AICapability[] }>('/admin/ai/capabilities').then((r) => r.data.capabilities),
  patchCapability: (name: string, data: Partial<Omit<AICapability, 'name' | 'category' | 'description' | 'stage' | 'depends_on' | 'unmet_dependencies' | 'required_caps' | 'preferred_task_type'>>) =>
    api.patch(`/admin/ai/capabilities/${name}`, data).then((r) => r.data),

  listRuns: (params?: { limit?: number; offset?: number; capability_id?: string }) =>
    api.get<{ runs: AIRun[]; limit: number; offset: number }>('/admin/ai/runs', { params }).then((r) => r.data),

  listKill: () =>
    api.get<{ kill_switches: AIKillSwitch[] }>('/admin/ai/kill').then((r) => r.data.kill_switches),
  setKill: (scope: string, killed: boolean, reason: string) =>
    api.put('/admin/ai/kill', { scope, killed, reason }).then((r) => r.data),

  // Stage 2 assistance entry points.
  search: (query: string, customerID?: string) =>
    api.post<{
      answer: string
      tables?: { title: string; columns: string[]; rows: string[][] }[]
      tool_log: { tool: string; args: any; result: string; success: boolean }[]
    }>('/admin/ai/assist/search', { query, customer_id: customerID }).then((r) => r.data),

  generateScript: (query: string, language: 'bash' | 'powershell', customerID?: string) =>
    api.post<{
      language: string
      code: string
      explanation: string
      danger_score: 'low' | 'medium' | 'high'
      danger_hits?: string[]
      warnings?: string[]
    }>('/admin/ai/assist/script', { query, language, customer_id: customerID }).then((r) => r.data),
};

export default api;
