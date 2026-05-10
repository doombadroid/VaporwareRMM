// Standalone config for README screenshot capture. Boots its own
// server + dashboard on isolated ports (3033 / 8093) so it does not
// collide with any locally running dev stack.
//
//   npx playwright test --config=playwright.screenshots.config.ts \
//     --project chromium --reporter=list
import { defineConfig, devices } from '@playwright/test'
import path from 'path'

const repoRoot = path.resolve(__dirname, '../..')
const SERVER_PORT = '8093'
const DASH_PORT = '3033'

export default defineConfig({
  testDir: './e2e',
  testMatch: ['**/_screenshots.spec.ts'],
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  timeout: 180_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: `http://localhost:${DASH_PORT}`,
    trace: 'off',
    screenshot: 'off',
    viewport: { width: 1440, height: 900 },
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: [
    {
      command: 'go run .',
      cwd: path.join(repoRoot, 'packages/server'),
      url: `http://localhost:${SERVER_PORT}/health`,
      reuseExistingServer: false,
      timeout: 240_000,
      env: {
        SERVER_PORT,
        DATABASE_PATH: '/tmp/vaporrmm-shots.db',
        JWT_SECRET: 'shots-secret-key-that-is-long-enough-for-tests-ok',
        ADMIN_PASSWORD: 'ReadmeDemo123!',
        CORS_ORIGINS: `http://localhost:${DASH_PORT}`,
        SECRETS_ENCRYPTION_KEY: 'fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY=',
        DISABLE_RATE_LIMIT: '1',
      },
    },
    {
      command: `npm run dev -- --port ${DASH_PORT}`,
      cwd: __dirname,
      url: `http://localhost:${DASH_PORT}`,
      reuseExistingServer: false,
      timeout: 120_000,
      env: {
        NEXT_PUBLIC_API_URL: '/api',
        API_PROXY_TARGET: `http://localhost:${SERVER_PORT}/api`,
        PORT: DASH_PORT,
      },
    },
  ],
})
