// Screenshot capture for the README. Not part of the regular test run.
//
//   npx playwright test --config=playwright.screenshots.config.ts \
//     --project chromium --reporter=list
import { test, Page } from '@playwright/test'
import path from 'path'

const ADMIN_EMAIL = 'admin@vaporrmm.local'
const ADMIN_PASSWORD = 'ReadmeDemo123!'
const SHOTS_DIR = path.resolve(__dirname, '../../../docs/screenshots')

async function login(page: Page) {
  await page.goto('/login')
  await page.evaluate(() => localStorage.setItem('setup_completed', 'true'))
  await page.fill('input[type="email"]', ADMIN_EMAIL)
  await page.fill('input[type="password"]', ADMIN_PASSWORD)
  await page.click('button[type="submit"]')
  await page.waitForURL((url) => url.pathname === '/', { timeout: 30_000 })
  await page.waitForTimeout(2000)
}

test.describe.configure({ mode: 'serial' })

test('01 login', async ({ page }) => {
  await page.goto('/login')
  await page.waitForTimeout(1000)
  await page.screenshot({ path: `${SHOTS_DIR}/01-login.png`, fullPage: false })
})

test('02 dashboard', async ({ page }) => {
  await login(page)
  await page.screenshot({ path: `${SHOTS_DIR}/02-dashboard.png`, fullPage: false })
})

test('03 devices', async ({ page }) => {
  await login(page)
  await page.goto('/agents')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: `${SHOTS_DIR}/03-devices.png`, fullPage: false })
})

test('04 tickets', async ({ page }) => {
  await login(page)
  await page.goto('/tickets')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: `${SHOTS_DIR}/04-tickets.png`, fullPage: false })
})

test('05 alerts', async ({ page }) => {
  await login(page)
  await page.goto('/alerts')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: `${SHOTS_DIR}/05-alerts.png`, fullPage: false })
})

test('06 patches', async ({ page }) => {
  await login(page)
  await page.goto('/patches')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: `${SHOTS_DIR}/06-patches.png`, fullPage: false })
})

test('07 network map', async ({ page }) => {
  await login(page)
  await page.goto('/network')
  await page.waitForTimeout(2000)
  await page.screenshot({ path: `${SHOTS_DIR}/07-network.png`, fullPage: false })
})

test('08 command palette', async ({ page }) => {
  await login(page)
  await page.keyboard.press('Meta+K')
  await page.waitForTimeout(600)
  await page.keyboard.type('dev', { delay: 80 })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${SHOTS_DIR}/08-command-palette.png`, fullPage: false })
})

test('09 settings security', async ({ page }) => {
  await login(page)
  await page.goto('/settings')
  await page.waitForTimeout(800)
  await page.click('text=Security').catch(() => {})
  await page.waitForTimeout(600)
  await page.screenshot({ path: `${SHOTS_DIR}/09-settings-security.png`, fullPage: false })
})

test('10 audit log', async ({ page }) => {
  await login(page)
  await page.goto('/admin/audit')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: `${SHOTS_DIR}/10-audit.png`, fullPage: false })
})

test('11 tenants', async ({ page }) => {
  await login(page)
  await page.goto('/admin/tenants')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: `${SHOTS_DIR}/11-tenants.png`, fullPage: false })
})
