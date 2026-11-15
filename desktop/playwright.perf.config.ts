import { defineConfig } from '@playwright/test'

// The frame budget runs apart from the e2e tests: it is slower, and its
// numbers mean something only when nothing else loads the Mac.
export default defineConfig({
  testDir: './test',
  testMatch: /.*\.perf\.ts/,
  timeout: 300_000,
  workers: 1,
  reporter: 'list',
  outputDir: 'test-results/perf',
})
