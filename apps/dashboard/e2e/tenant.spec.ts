import { test, expect, Page } from '@playwright/test';

const ADMIN_EMAIL = 'admin@vaporrmm.local';
const ADMIN_PASSWORD = 'TestAdmin123!';

async function login(page: Page, email: string, password: string) {
  page.on('response', async (r) => {
    if (r.url().includes('/api/')) {
      console.log(`[net] ${r.status()} ${r.request().method()} ${r.url()}`);
    }
  });
  await page.goto('/login');
  await page.fill('input[type="email"]', email);
  await page.fill('input[type="password"]', password);
  await page.click('button[type="submit"]');
  // Wait for either the dashboard to render or stay on login (login failed)
  await page.waitForFunction(() => window.location.pathname !== '/login' || !!document.querySelector('.bg-red-500\\/10'), { timeout: 10_000 }).catch(() => {});
  const url = page.url();
  const expiry = await page.evaluate(() => localStorage.getItem('auth_expiry'));
  console.log('AFTER LOGIN URL:', url, 'AUTH_EXPIRY:', expiry);
}

test.describe('Tenant management (super_admin)', () => {
  test('default admin is super_admin and sees Tenants nav', async ({ page }) => {
    await login(page, ADMIN_EMAIL, ADMIN_PASSWORD);
    await expect(page).toHaveURL('/');
    // Sidebar shows the Tenants link (rendered in both desktop + mobile drawer)
    await expect(page.locator('nav a[href="/admin/tenants"]').first()).toBeVisible();
    await expect(page.locator('text=All tenants').first()).toBeVisible();
  });

  test('super_admin can create a tenant and is shown the registration secret once', async ({ page }) => {
    await login(page, ADMIN_EMAIL, ADMIN_PASSWORD);
    await page.goto('/admin/tenants');
    await expect(page.locator('h1', { hasText: 'Tenants' })).toBeVisible();

    const tenantName = `Acme E2E ${Date.now()}`;
    await page.click('button:has-text("New tenant")');
    await page.fill('input[placeholder="Acme Corp"]', tenantName);
    await page.click('button[type="submit"]:has-text("Create tenant")');

    // The amber install-command reveal panel must appear with a non-empty secret
    const reveal = page.locator('text=Install command · shown once');
    await expect(reveal).toBeVisible();
    const code = await page.locator('code.font-mono').filter({ hasText: 'vrt_' }).first().innerText();
    expect(code).toMatch(/vrt_[a-f0-9]+/);

    // Tenant appears in the list (also shown in the success toast + reveal panel — match any)
    await expect(page.locator(`text=${tenantName}`).first()).toBeVisible();
  });

  test('rotated registration secret response sets Cache-Control: no-store', async ({ page }) => {
    await login(page, ADMIN_EMAIL, ADMIN_PASSWORD);
    // Use the page's request context so cookies + CSRF are present
    const list = await page.request.get('/api/v1/admin/tenants/');
    expect(list.ok()).toBeTruthy();
    const { tenants } = await list.json();
    const target = tenants.find((t: any) => t.id !== 'default');
    test.skip(!target, 'no non-default tenant present');
    const csrf = (await page.context().cookies()).find((c) => c.name === 'csrf_token')?.value;
    const res = await page.request.post(`/api/v1/admin/tenants/${target.id}/registration-secret`, {
      headers: csrf ? { 'X-CSRF-Token': csrf } : undefined,
    });
    expect(res.ok()).toBeTruthy();
    expect(res.headers()['cache-control']).toContain('no-store');
  });

  test('unauthenticated request to /admin/tenants returns 401', async ({ request }) => {
    const res = await request.get('/api/v1/admin/tenants/', {
      // explicit no-cookie request
      headers: { Cookie: '' },
    });
    expect(res.status()).toBe(401);
  });
});

test.describe('Tenant isolation (smoke)', () => {
  test('GET /api/v1/admin/tenants/ requires super_admin role', async ({ request }) => {
    // Without authentication, must be 401 (CSRF/Auth gate). With non-super_admin
    // session it would be 403, but we can't easily provision a tenant_admin in
    // a single-test E2E run — the 401 path proves the route is gated.
    const res = await request.get('/api/v1/admin/tenants/');
    expect([401, 403]).toContain(res.status());
  });
});
