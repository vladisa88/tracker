import { defineConfig, devices } from '@playwright/test';
export default defineConfig({
  testDir: './e2e',
  use: {baseURL: 'http://127.0.0.1:18080'},
  projects: [
    {name: 'desktop', use: {...devices['Desktop Chrome']}},
    {name: 'phone', use: {...devices['iPhone 13'], defaultBrowserType: 'chromium'}}
  ],
  webServer: {
    command: 'cd ../backend && BROWSER_FIXTURE=1 go test ./internal/app -run ^TestBrowserFixture$ -count=1 -timeout=10m',
    url: 'http://127.0.0.1:18080/health/ready',
    reuseExistingServer: false,
    timeout: 60000
  }
});
