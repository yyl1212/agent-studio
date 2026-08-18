import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: 'http://127.0.0.1:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: 'cd ../api && CGO_ENABLED=0 DATABASE_URL=postgres://agent:agent@127.0.0.1:5432/agent_studio?sslmode=disable MODEL_PROVIDER=mock HTTP_ADDR=127.0.0.1:8080 go run ./cmd/server',
      url: 'http://127.0.0.1:8080/readyz',
      reuseExistingServer: true,
      timeout: 120_000,
    },
    {
      command: 'corepack pnpm@10.34.5 dev --host 127.0.0.1',
      url: 'http://127.0.0.1:5173/workflows',
      reuseExistingServer: true,
      timeout: 120_000,
    },
  ],
})
