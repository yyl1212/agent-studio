import { defineConfig, devices } from '@playwright/test'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const configDirectory = dirname(fileURLToPath(import.meta.url))
const nodeIndexCacheDirectory = resolve(configDirectory, 'e2e/fixtures/node-index')

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
      command: 'node e2e/fixtures/slow-webhook.mjs',
      url: 'http://127.0.0.1:8090/healthz',
      reuseExistingServer: false,
      timeout: 30_000,
    },
    {
      command: `AGENT_STUDIO_NODE_INDEX_CACHE_DIR=${JSON.stringify(nodeIndexCacheDirectory)} sh e2e/fixtures/run-e2e-runtime.sh`,
      url: 'http://127.0.0.1:8080/readyz',
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command: 'corepack pnpm@10.34.5 dev --host 127.0.0.1',
      url: 'http://127.0.0.1:5173/workflows',
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
})
