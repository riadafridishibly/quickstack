import { defineConfig, devices } from "@playwright/test";

const port = 8390;

// `make e2e` builds bin/quickstack and creates a fresh database first.
export default defineConfig({
  testDir: "e2e",
  workers: 1,
  forbidOnly: !!process.env.CI,
  use: {
    baseURL: `http://localhost:${port}`,
    trace: "retain-on-failure",
  },
  projects: [
    // Signs the owner up once and saves the session for the other specs.
    { name: "setup", testMatch: /setup\.ts$/ },
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], storageState: "e2e/.auth/owner.json" },
      dependencies: ["setup"],
    },
  ],
  webServer: {
    command: "./bin/quickstack",
    env: {
      QUICKSTACK_ADDR: `localhost:${port}`,
      // Mail goes to /dev/mail, where the specs read invitation and reset links.
      QUICKSTACK_MAIL_OUTBOX: "1",
    },
    url: `http://localhost:${port}/healthz`,
    reuseExistingServer: false,
    stdout: "ignore",
  },
});
