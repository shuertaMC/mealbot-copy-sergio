import { defineConfig } from "@playwright/test";
import { existsSync, readdirSync } from "node:fs";
import { join } from "node:path";

const mcodeDir = process.env.MCODE_DIR;

if (!mcodeDir) {
  throw new Error("MCODE_DIR must be set so Playwright can write the canonical JSON report");
}

function sandboxChromium(): string | undefined {
  if (process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH) {
    return process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
  }
  if (!process.env.SANDBOX_ROOT) {
    return undefined;
  }

  const browsersDir = join(process.env.SANDBOX_ROOT, ".agent-browser", "browsers");
  if (!existsSync(browsersDir)) {
    return undefined;
  }

  const chromeDirectory = readdirSync(browsersDir)
    .filter((entry) => entry.startsWith("chrome-"))
    .sort()
    .at(-1);
  if (!chromeDirectory) {
    return undefined;
  }

  const executable = join(browsersDir, chromeDirectory, "chrome");
  return existsSync(executable) ? executable : undefined;
}

export default defineConfig({
  testDir: ".",
  timeout: 30_000,
  retries: 1,
  use: {
    baseURL: process.env.TARGET_BASE_URL ?? "http://127.0.0.1:18080",
    headless: true,
    launchOptions: {
      executablePath: sandboxChromium(),
      args: ["--no-sandbox", "--disable-dev-shm-usage"],
    },
    screenshot: "on",
    video: "retain-on-failure",
    trace: "retain-on-failure",
  },
  reporter: [
    ["list"],
    ["json", { outputFile: `${mcodeDir}/fe_testing/playwright-results.json` }],
  ],
});
