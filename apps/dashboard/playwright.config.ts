import { defineConfig, devices } from '@playwright/test';
import path from 'path';

const repoRoot = path.resolve(__dirname, '../..');

export default defineConfig({
  testDir: './e2e',
  // Specs prefixed with _ are README screenshot helpers, not regression
  // tests. They use a different ADMIN_PASSWORD and the AI variant
  // requires Postgres+pgvector. Run them via the standalone
  // playwright.screenshots.config.ts when capturing README assets.
  testIgnore: ['**/_screenshots*.spec.ts'],
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: 'http://localhost:3000',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  // Bring up Go server + Next dashboard automatically. Each gets an ephemeral
  // SQLite DB under /tmp so runs are clean and isolated.
  webServer: [
    {
      command: 'go run .',
      cwd: path.join(repoRoot, 'packages/server'),
      url: 'http://localhost:8080/health',
      reuseExistingServer: !process.env.CI,
      // Cold 'go run .' on CI takes ~55s to compile + 33 SQLite migrations
      // before /health responds. 60s left only ~5s for the healthcheck and
      // would race; 180s gives plenty of headroom for slower runners.
      timeout: 180_000,
      env: {
        SERVER_PORT: '8080',
        DATABASE_PATH: '/tmp/vaporrmm-e2e.db',
        // 48-char value safely above the new 32-char min-length gate.
        JWT_SECRET: 'e2e-secret-key-that-is-long-enough-for-tests-ok',
        ADMIN_PASSWORD: 'TestAdmin123!',
        CORS_ORIGINS: 'http://localhost:3000',
        SECRETS_ENCRYPTION_KEY: 'fmZn0pFd/f58gKeknlaECEbcMDh5oQ+nRhFB/sAMScY=',
        DISABLE_RATE_LIMIT: '1',
      },
    },
    {
      command: 'npm run dev',
      cwd: __dirname,
      url: 'http://localhost:3000',
      reuseExistingServer: !process.env.CI,
      timeout: 90_000,
      env: {
        // Same-origin /api so httpOnly cookies work in tests (Next dev rewrites proxy to Go)
        NEXT_PUBLIC_API_URL: '/api',
        API_PROXY_TARGET: 'http://localhost:8080/api',
      },
    },
  ],
});
