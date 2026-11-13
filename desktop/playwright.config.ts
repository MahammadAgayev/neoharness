import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './test',
  testMatch: /.*\.e2e\.ts/,
  timeout: 120_000,
  workers: 1,
  reporter: 'list',
})
