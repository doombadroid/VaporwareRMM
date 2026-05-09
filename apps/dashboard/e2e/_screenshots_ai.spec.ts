// AI screenshot capture for the README. Not part of the regular test run.
// Requires Postgres + pgvector reachable at $DATABASE_URL — the AI tab is
// hidden on SQLite. Boot order:
//
//   docker compose -f docker-compose.test.yml up -d postgres-test
//   cd packages/server && DATABASE_URL=... ADMIN_PASSWORD=... go run .
//   cd apps/dashboard && ./node_modules/.bin/next dev
//   cd apps/dashboard && npx playwright test e2e/_screenshots_ai.spec.ts \
//     --project chromium --reporter=list
//
// The spec logs in, programmatically enables AI for the default tenant,
// adds a fake provider + routing rule via the admin API (so the dashboard
// renders the populated state, not the empty-state placeholders), then
// captures the AI panel + the new Assistance card.
import { test, expect, Page } from '@playwright/test';
import path from 'path';

const ADMIN_EMAIL = 'admin@vaporrmm.local';
const ADMIN_PASSWORD = process.env.AI_SCREENSHOT_ADMIN_PASSWORD || 'ScreenshotAdmin123!';
const SHOTS_DIR = path.resolve(__dirname, '../../../docs/screenshots');
const API_BASE = 'http://localhost:8080/api/v1';

async function login(page: Page) {
  // Hit the API directly. page.request shares the BrowserContext cookie jar
  // so auth_token + csrf_token cookies become live in the browser too.
  // BUT: the cookies come back with Domain=localhost on port 8080, while
  // page navigation lands on port 3000 — same host but axios's withCredentials
  // sends cookies cross-origin via the dashboard's API client (CORS already
  // allows it). What the BROWSER needs is the cookies set on
  // localhost:3000's domain so AuthGuard can read auth_expiry. We extract the
  // login response token and seed everything explicitly.
  const resp = await page.request.post('http://localhost:8080/api/auth/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
  });
  if (!resp.ok()) {
    throw new Error(`login failed: ${resp.status()} ${await resp.text()}`);
  }
  // page.request shares the BrowserContext cookie jar — auth_token + csrf_token
  // are now set for :8080. Dashboard fetches with withCredentials send them.
  // AuthGuard checks localStorage.auth_expiry (MILLISECONDS, not seconds — the
  // earlier mistake here is what kept bouncing us back to /login).
  await page.goto('/');
  await page.evaluate(() => {
    localStorage.setItem('setup_completed', 'true');
    localStorage.setItem('auth_expiry', String(Date.now() + 24 * 60 * 60 * 1000));
  });
}

// seedAI configures the AI surface so screenshots show populated state
// instead of empty placeholders. Idempotent — runs through the dashboard's
// own auth cookie so CSRF + tenant scope are correct.
async function seedAI(page: Page) {
  // Pull CSRF token from cookie
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === 'csrf_token')?.value ?? '';

  // Enable AI for the default tenant + acknowledge DPA so the banner doesn't
  // dominate the screenshot.
  await page.request.patch(`${API_BASE}/admin/ai/tenant`, {
    data: {
      ai_enabled: true,
      ai_billing_mode: 'absorb',
      ai_max_chat_cost_per_day_micros: 5_000_000, // $5/day
      ai_max_embedding_cost_per_day_micros: 1_000_000, // $1/day
      acknowledge_dpa: true,
    },
    headers: csrf ? { 'X-CSRF-Token': csrf } : {},
  });

  // Provider list — skip if one already exists from a previous screenshot run.
  const providersResp = await page.request.get(`${API_BASE}/admin/ai/providers`);
  const { providers } = await providersResp.json();
  let providerId: string | null = providers?.[0]?.id ?? null;
  if (!providerId) {
    const create = await page.request.post(`${API_BASE}/admin/ai/providers`, {
      data: {
        kind: 'openai',
        name: 'Production OpenAI',
        base_url: 'https://api.openai.com/v1',
        api_key: 'sk-screenshot-placeholder',
        region: 'us',
        model_trust_level: 'external',
        enabled: true,
      },
      headers: csrf ? { 'X-CSRF-Token': csrf } : {},
    });
    const created = await create.json();
    providerId = created.id;
  }

  // Routing rule for the `reason` task type so the alert_dedup capability
  // would have somewhere to send classify calls.
  const rrResp = await page.request.get(`${API_BASE}/admin/ai/routing`);
  const { routing_rules } = await rrResp.json();
  if (!routing_rules || routing_rules.length === 0) {
    await page.request.post(`${API_BASE}/admin/ai/routing`, {
      data: {
        task_type: 'reason',
        preferred_provider_id: providerId,
        model_name: 'gpt-4o-2024-11-20',
        max_cost_per_call_micros: 200_000, // $0.20
        cost_per_1k_input_micros: 2_500, // $0.0025/1k
        cost_per_1k_output_micros: 10_000, // $0.01/1k
      },
      headers: csrf ? { 'X-CSRF-Token': csrf } : {},
    });
  }
}

test.use({ viewport: { width: 1440, height: 1100 } });

test('ai admin page', async ({ page }) => {
  await login(page);
  await seedAI(page);
  await page.goto('/admin/ai');
  await page.waitForTimeout(1500);
  // Tenant + Kill switches + Providers + Routing + Capabilities + Assistance + Runs.
  await page.screenshot({ path: `${SHOTS_DIR}/05-ai-admin.png`, fullPage: true });
});

test('ai assistance panel', async ({ page }) => {
  await login(page);
  await seedAI(page);
  await page.goto('/admin/ai');
  await page.waitForTimeout(1200);
  // Scroll to the Assistance card before screenshotting.
  await page.evaluate(() => {
    const card = Array.from(document.querySelectorAll('h3, h2'))
      .find((el) => el.textContent?.includes('Assistance')) as HTMLElement | undefined;
    if (card) card.scrollIntoView({ behavior: 'instant', block: 'center' });
  });
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${SHOTS_DIR}/06-ai-assistance.png`, fullPage: false });
});
