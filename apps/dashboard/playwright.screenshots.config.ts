// Standalone playwright config for the README screenshot specs.
// Assumes the server + dashboard are already running locally — does NOT
// boot anything via webServer. Use with the e2e/_screenshots*.spec.ts
// files only:
//
//   npx playwright test e2e/_screenshots_ai.spec.ts \
//     --config=playwright.screenshots.config.ts --project chromium \
//     --reporter=list
import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  timeout: 120_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: 'http://localhost:3000',
    trace: 'off',
    screenshot: 'off',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
})
