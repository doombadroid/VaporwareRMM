// k6 load test — simulates dashboard users hitting the API.
// Logs in as default admin once, then loops over read-heavy endpoints.
//
// Usage:
//   k6 run -e BASE_URL=http://localhost:8080 -e USERS=20 loadtest/dashboard.js

import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const USERS = parseInt(__ENV.USERS || '20', 10);
const ADMIN_EMAIL = __ENV.ADMIN_EMAIL || 'admin@vaporrmm.local';
const ADMIN_PASSWORD = __ENV.ADMIN_PASSWORD || 'TestAdmin123!';

export const options = {
  scenarios: {
    browse: {
      executor: 'constant-vus',
      vus: USERS,
      duration: __ENV.DURATION || '60s',
      exec: 'browse',
    },
  },
  thresholds: {
    'http_req_duration{name:list_devices}': ['p(95)<300'],
    'http_req_duration{name:overview}': ['p(95)<300'],
    'http_req_failed': ['rate<0.01'],
  },
};

function authenticate() {
  const res = http.post(
    `${BASE}/api/auth/login`,
    JSON.stringify({ email: ADMIN_EMAIL, password: ADMIN_PASSWORD }),
    { headers: { 'Content-Type': 'application/json' }, tags: { name: 'login' } },
  );
  check(res, { 'login 200': (r) => r.status === 200 });
  return {
    cookie: res.cookies['auth_token']?.[0]?.value,
    csrf: res.cookies['csrf_token']?.[0]?.value,
  };
}

export function browse() {
  const auth = authenticate();
  if (!auth.cookie) return;
  const headers = {
    Cookie: `auth_token=${auth.cookie}; csrf_token=${auth.csrf}`,
    'X-CSRF-Token': auth.csrf,
  };

  const r1 = http.get(`${BASE}/api/v1/devices/`, { headers, tags: { name: 'list_devices' } });
  check(r1, { 'devices 200': (r) => r.status === 200 });

  const r2 = http.get(`${BASE}/api/v1/dashboard/overview`, { headers, tags: { name: 'overview' } });
  check(r2, { 'overview 200': (r) => r.status === 200 });

  const r3 = http.get(`${BASE}/api/v1/scripts`, { headers, tags: { name: 'scripts' } });
  check(r3, { 'scripts 200': (r) => r.status === 200 });

  sleep(1);
}
