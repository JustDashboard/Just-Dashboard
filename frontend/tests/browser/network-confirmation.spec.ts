import { expect, test, type Page } from "@playwright/test"
import { json, mockNetwork } from "./network-fixture"
import { mockNetworkWrites, overrides } from "./network-dns-fixture"

const pendingChange = () => ({
  id: "pending-browser-one",
  phase: "awaiting_confirmation",
  generation: "a".repeat(64),
  updatedAt: new Date().toISOString(),
  appliedAt: new Date(Date.now() - 1000).toISOString(),
  expiresAt: new Date(Date.now() + 90_000).toISOString(),
  ownerUserId: 1,
  watchdog: "armed",
  runtime: "applied",
  persistence: "written",
  boot: "enabled",
})

async function setup(page: Page, initiallyPending = false) {
  const state = {
    available: true,
    owned: initiallyPending,
    change: initiallyPending ? pendingChange() : null,
  }
  const connection = { fails: false }
  await mockNetwork(page, [], {
    overrides: { ...overrides, "/network/traffic/live": { now: 0, series: {} } },
  })
  await mockNetworkWrites(page, [])
  await page.route("**/api/v1/network/changes/current", async (route) => {
    if (connection.fails) return route.abort("connectionfailed")
    return json(route, state)
  })
  return { state, connection }
}

const banner = (page: Page) =>
  page.getByRole("complementary", { name: "Network change confirmation" })

test("a supported mutation opts in and requires a received response before explicit confirmation", async ({
  page,
}) => {
  const { state } = await setup(page)
  let applyHeader: string | undefined
  await page.route("**/api/v1/network/shaping/ens3", async (route) => {
    applyHeader = route.request().headers()["x-jd-network-apply"]
    state.change = pendingChange()
    state.owned = true
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: { "X-JD-Network-Change": state.change.id },
      body: JSON.stringify({ output: "ok" }),
    })
  })
  let releaseResponse = () => {}
  const responseGate = new Promise<void>((resolve) => {
    releaseResponse = resolve
  })
  const challenge = "received-response".padEnd(64, "a")
  let verifications = 0
  let confirmations = 0
  await page.route("**/api/v1/network/changes/*/verify", async (route) => {
    verifications += 1
    await responseGate
    return json(route, { challenge, verifiedAt: new Date().toISOString() })
  })
  await page.route("**/api/v1/network/changes/*/confirm", async (route) => {
    confirmations += 1
    expect(route.request().postDataJSON()).toEqual({ challenge })
    expect(route.request().headers()["x-jd-network-apply"]).toBeUndefined()
    state.change = { ...state.change!, phase: "confirmed", watchdog: "completed" }
    return json(route, state.change)
  })

  await page.goto("/network/traffic")
  await expect(
    page.getByRole("switch", { name: "Require network reconnection confirmation" }),
  ).toBeChecked()
  await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("button", { name: "Use cake" }).click()
  await sheet.getByLabel("Upload limit").fill("20")
  await sheet.getByRole("button", { name: "Apply", exact: true }).click()
  await expect.poll(() => applyHeader).toBe("pending")
  await expect(banner(page)).toContainText("Network change is awaiting confirmation")
  await expect(banner(page)).toContainText("Deadline:")
  const confirm = banner(page).getByRole("button", { name: "Confirm network change", exact: true })
  await expect(confirm).toBeDisabled()
  await banner(page).getByRole("button", { name: "Verify reconnection", exact: true }).click()
  await expect.poll(() => verifications).toBe(1)
  await expect(confirm).toBeDisabled()
  expect(confirmations).toBe(0)
  releaseResponse()
  await expect(confirm).toBeEnabled()
  expect(confirmations).toBe(0)
  await confirm.click()
  await expect(banner(page)).toContainText("Network change confirmed")
  expect(confirmations).toBe(1)
})

test("pending changes remain visible outside Network and expire without a success claim", async ({
  page,
}) => {
  const { state } = await setup(page, true)
  state.change!.expiresAt = new Date(Date.now() - 1000).toISOString()
  await page.goto("/account/keys")
  await expect(banner(page)).toContainText("The recovery deadline has passed")
  await expect(banner(page).getByRole("button", { name: "Confirm network change" })).toBeDisabled()
  await expect(
    page.getByRole("switch", { name: "Require network reconnection confirmation" }),
  ).toHaveCount(0)
  await expect(banner(page)).not.toContainText("Previous network settings restored")
  state.change = {
    ...state.change!,
    phase: "recovered",
    watchdog: "recovered",
    runtime: "restored",
    persistence: "restored",
  }
  await expect(banner(page)).toContainText("Previous network settings restored")
})

test("an unavailable dashboard response retains pending status and cannot enable confirmation", async ({
  page,
}) => {
  const { state, connection } = await setup(page, true)
  await page.route("**/api/v1/network/changes/*/verify", (route) => route.abort("connectionfailed"))
  await page.goto("/network/traffic")
  await expect(banner(page)).toContainText("Network change is awaiting confirmation")
  connection.fails = true
  await banner(page).getByRole("button", { name: "Verify reconnection", exact: true }).click()
  await expect(banner(page).getByRole("button", { name: "Confirm network change" })).toBeDisabled()
  await expect(banner(page)).toContainText("The last known status is shown")
  await expect(banner(page)).toContainText("Network change is awaiting confirmation")
  connection.fails = false
  state.change = {
    ...state.change!,
    phase: "degraded",
    recoveryErrors: ["Could not restore the captured queue"],
  } as typeof state.change
  await expect(banner(page)).toContainText("Network recovery needs attention")
  await expect(banner(page)).toContainText("Could not restore the captured queue")
})

test("another administrator sees pending recovery evidence without owner actions", async ({
  page,
}) => {
  const { state } = await setup(page, true)
  state.owned = false
  await page.goto("/network/traffic")
  await expect(banner(page)).toContainText(
    "The administrator who applied this change must reconnect",
  )
  await expect(banner(page).getByRole("button", { name: "Verify reconnection" })).toHaveCount(0)
  await expect(banner(page).getByRole("button", { name: "Confirm network change" })).toHaveCount(0)
})

test("verification shows the access boundary after the change and names what was lost", async ({
  page,
}) => {
  await setup(page, true)
  const challenge = "received-response".padEnd(64, "b")
  await page.route("**/api/v1/network/changes/*/verify", (route) =>
    json(route, {
      challenge,
      verifiedAt: new Date().toISOString(),
      boundary: {
        before: true,
        lost: 1,
        checks: [],
        changes: [
          {
            id: "ingress",
            title: "Caddy is the only routable listener",
            before: "held",
            after: "held",
            detail: "Caddy holds port 8443.",
            lost: false,
          },
          {
            id: "tailnet",
            title: "The tailnet path is up",
            before: "held",
            after: "broken",
            detail:
              "The allowlist admits the tailnet and no tailscale interface is up on this host.",
            lost: true,
          },
        ],
      },
    }),
  )
  await page.goto("/network/traffic")
  await banner(page).getByRole("button", { name: "Verify reconnection", exact: true }).click()
  const after = banner(page).getByLabel("Access boundary after this change")
  await expect(after).toContainText("Caddy is the only routable listener")
  await expect(after).toContainText("lost")
  await expect(after).toContainText("no tailscale interface is up")
  await expect(
    banner(page).getByRole("button", { name: "Confirm anyway", exact: true }),
  ).toBeEnabled()
})

test("a pending SSH apply is confirmed through the same banner in SSH's words", async ({
  page,
}) => {
  const { state } = await setup(page, true)
  state.change = { ...pendingChange(), subsystem: "sshd", boot: "not_applicable" } as ReturnType<
    typeof pendingChange
  >
  await page.goto("/network/traffic")
  await expect(banner(page)).toContainText("SSH change is awaiting confirmation")
  await expect(banner(page)).toContainText("restore the previous SSH configuration")
  await expect(
    banner(page).getByRole("button", { name: "Confirm SSH change", exact: true }),
  ).toBeDisabled()
  state.change = { ...state.change!, phase: "recovered", watchdog: "recovered" }
  await expect(banner(page)).toContainText("Previous SSH configuration restored")
})
