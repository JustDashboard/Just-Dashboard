import { defineConfig, devices } from "@playwright/test"

const baseURL = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43117"
const externallyManaged = Boolean(process.env.JD_BROWSER_BASE_URL)
const crossBrowser = process.env.JD_BROWSER_PROJECTS === "all"

export default defineConfig({
  testDir: "./tests/browser",
  /*
    Every spec mocks its own API through `page.route`, so nothing here shares
    state but the server — which serves a build and holds none. The suite ran
    one worker at a time anyway, and thirty specs against a cold server is the
    five to ten minutes that stopped anybody running it during a change.

    Serialised on CI, where the box is smaller and a flake costs a re-run.
  */
  fullyParallel: !process.env.CI,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : "50%",
  reporter: process.env.CI ? [["line"], ["html", { open: "never" }]] : "list",
  outputDir: "test-results",
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  /*
    A server already listening on the port is used as-is. Booting a fresh one
    per invocation is most of what a single-spec run cost, and the cost was
    paid on every iteration of a change — so the suite was run once at the end,
    which is the opposite of what it is for. `bun run start --port 43117` in
    another terminal now makes each run the tests themselves.

    Never on CI: there, a leftover server would be a stale build, and the whole
    point of the run is that it is the build in the diff.
  */
  webServer: externallyManaged
    ? undefined
    : {
        command: "bun run start --hostname 127.0.0.1 --port 43117",
        url: baseURL,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
    ...(crossBrowser
      ? [
          { name: "firefox", use: { ...devices["Desktop Firefox"] } },
          { name: "webkit", use: { ...devices["Desktop Safari"] } },
        ]
      : []),
  ],
})
