// k6 load test — N agents heartbeating in steady state.
// Each VU lazily registers on its first iteration, then heartbeats.
//
// Usage:
//   k6 run -e BASE_URL=http://localhost:8080 -e AGENTS=100 -e DURATION=30s loadtest/agents.js
//
// Targets:
//   - p95 heartbeat latency < 200ms
//   - error rate < 1%

import http from 'k6/http';
import { check, sleep } from 'k6';
import { randomString } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const AGENTS = parseInt(__ENV.AGENTS || '100', 10);
const HEARTBEAT_INTERVAL_MS = parseInt(__ENV.HEARTBEAT_INTERVAL_MS || '2000', 10);
const REG_SECRET = __ENV.REGISTRATION_SECRET || '';

export const options = {
  scenarios: {
    agents: {
      executor: 'constant-vus',
      vus: AGENTS,
      duration: __ENV.DURATION || '30s',
    },
  },
  thresholds: {
    'http_req_duration{name:heartbeat}': ['p(95)<200'],
    'http_req_failed{name:heartbeat}': ['rate<0.01'],
    'http_req_failed{name:register}': ['rate<0.01'],
  },
};

let myToken = null;

function register() {
  const token = `tok-${randomString(32)}`;
  const hostname = `loadhost-${__VU}-${randomString(6)}`;
  const headers = { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` };
  if (REG_SECRET) headers['X-Registration-Secret'] = REG_SECRET;
  const res = http.post(
    `${BASE}/agent/register`,
    JSON.stringify({
      hostname,
      os: 'linux',
      os_version: 'ubuntu-22.04',
      local_ip: `10.0.${__VU}.1`,
      mac_address: 'aa:bb:cc:dd:ee:ff',
      cpu: 'load-test',
      agent_version: '1.0.0-loadtest',
    }),
    { headers, tags: { name: 'register' } },
  );
  check(res, { 'register 200': (r) => r.status === 200 });
  if (res.status === 200) myToken = token;
}

export default function () {
  if (!myToken) {
    register();
    if (!myToken) {
      sleep(0.5);
      return;
    }
  }
  const res = http.post(
    `${BASE}/agent/heartbeat`,
    JSON.stringify({
      status: 'online',
      cpu_usage: Math.random() * 60,
      memory_usage: Math.random() * 70,
      disk_usage: Math.random() * 50,
    }),
    {
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${myToken}` },
      tags: { name: 'heartbeat' },
    },
  );
  check(res, { 'heartbeat 2xx': (r) => r.status >= 200 && r.status < 300 });
  sleep(HEARTBEAT_INTERVAL_MS / 1000);
}
