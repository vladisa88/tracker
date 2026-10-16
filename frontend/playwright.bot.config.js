import base from './playwright.config.js';
import { defineConfig } from '@playwright/test';
export default defineConfig({
  ...base,
  testDir: './e2e-bot',
  use: {...base.use, baseURL: 'http://127.0.0.1:18081'},
  webServer: {
    command: 'cd ../backend && BOT_BROWSER_FIXTURE=1 go test ./internal/app -run ^TestBotBrowserFixture$ -count=1 -timeout=10m',
    url: 'http://127.0.0.1:18081/health/ready',
    reuseExistingServer: false,
    timeout: 60000
  }
});
