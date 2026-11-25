import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  timeout: 30_000,
  retries: 1,
  use: {
    baseURL: "http://127.0.0.1:18080",
    headless: true,
    screenshot: "on",
    video: "retain-on-failure",
    trace: "retain-on-failure",
  },
  reporter: [
    ["list"],
    ["json", { outputFile: `${process.env.MCODE_DIR}/fe_testing/playwright-results.json` }],
  ],
});
