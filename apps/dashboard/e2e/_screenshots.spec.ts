// Screenshot capture for the README. Not part of the regular test run.
// Run via: npx playwright test e2e/_screenshots.spec.ts --reporter=list
import { test, expect, Page } from '@playwright/test';
import path from 'path';

const ADMIN_EMAIL = 'admin@vaporrmm.local';
const ADMIN_PASSWORD = 'ReadmeDemo123!';
const SHOTS_DIR = path.resolve(__dirname, '../../../docs/screenshots');

async function login(page: Page) {
  await page.goto('/login');
  // Skip the first-run setup wizard for cleaner screenshots
  await page.evaluate(() => localStorage.setItem('setup_completed', 'true'));
  await page.fill('input[type="email"]', ADMIN_EMAIL);
  await page.fill('input[type="password"]', ADMIN_PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL('http://localhost:3000/');
  await page.waitForTimeout(1500);
}

test.use({ viewport: { width: 1440, height: 900 } });

test('login page', async ({ page }) => {
  await page.goto('/login');
  await page.waitForTimeout(800);
  await page.screenshot({ path: `${SHOTS_DIR}/01-login.png`, fullPage: false });
});

test('dashboard overview', async ({ page }) => {
  await login(page);
  await page.screenshot({ path: `${SHOTS_DIR}/02-dashboard.png`, fullPage: false });
});

test('tenants admin', async ({ page }) => {
  await login(page);
  await page.goto('/admin/tenants');
  await page.waitForTimeout(1500);
  // If Acme isn't there yet, create it. Skip if it already exists from a prior run.
  const acme = page.locator('text=Acme Corp').first();
  if (!(await acme.isVisible().catch(() => false))) {
    await page.click('button:has-text("New tenant")');
    await page.waitForTimeout(300);
    await page.fill('input[placeholder="Acme Corp"]', 'Acme Corp');
    await page.click('button[type="submit"]:has-text("Create tenant")');
    await page.waitForTimeout(1800);
    // Dismiss the registration-secret reveal panel for a cleaner shot
    await page.click('text=I\'ve saved it, dismiss').catch(() => {});
    await page.waitForTimeout(400);
  }
  // Reload to clear any toast / reveal artifacts
  await page.reload();
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${SHOTS_DIR}/03-tenants.png`, fullPage: false });
});

test('settings security', async ({ page }) => {
  await login(page);
  await page.goto('/settings');
  await page.waitForTimeout(800);
  // Click the Security tab
  await page.click('text=Security').catch(() => {});
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${SHOTS_DIR}/04-settings-security.png`, fullPage: false });
});
