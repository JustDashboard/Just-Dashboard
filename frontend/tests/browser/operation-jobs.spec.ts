import { expect, test, type Page } from "@playwright/test"
import { mockShowcase } from "./proxy-fixtures"
import { mockIntrusion } from "./security-intrusion-fixture"
import { ebpf, openMaturity } from "./network-traffic-maturity-fixture"
import {
  BRIDGE,
  ENGINE,
  OFFICE,
  mockRemoteDesktop,
  overview,
  streamView,
} from "./remote-desktop-fixture"
import { recordProgress } from "./progress-recording"

test.use({ viewport: { width: 1280, height: 900 } })
test.describe.configure({ timeout: process.env.JD_PROGRESS_RECORD === "1" ? 90_000 : 30_000 })

async function job(page: Page, id: string, kind: string, title: string, path?: string) {
  const record = {
    id,
    kind,
    title,
    status: "running",
    startedAt: new Date().toISOString(),
    startedBy: "operator",
    lines: 0,
  }
  let socket: { send: (message: string) => void } | undefined
  await page.routeWebSocket(new RegExp(`/jobs/${id}/stream`), (ws) => {
    socket = ws
  })
  await page.route(`**/api/v1/jobs/${id}`, (route) =>
    route.fulfill({ json: { job: record, lines: [] } }),
  )
  await page.route("**/api/v1/jobs/", (route) => route.fulfill({ json: [record] }))
  if (path)
    await page.route(
      (url) => url.pathname === `/api/v1${path}`,
      (route) =>
        route.request().method() === "GET" ? route.fallback() : route.fulfill({ json: record }),
    )
  return async () => {
    await expect.poll(() => Boolean(socket)).toBe(true)
    socket!.send(
      JSON.stringify({
        type: "job",
        data: { ...record, status: "succeeded", exitCode: 0, endedAt: new Date().toISOString() },
      }),
    )
  }
}

test("progress: certificate jobs keep their working reading during quiet output", async ({
  page,
}) => {
  await mockShowcase(page)
  const finish = await job(page, "certificate-progress", "certbot.renewal", "Renew certificates")
  await page.goto("/proxy/certificates")
  const start = page.getByRole("button", { name: "Renew certificates", exact: true })
  await start.scrollIntoViewIfNeeded()
  await recordProgress(
    page,
    "proxy-certificate-job",
    () => start.click(),
    page.locator("[data-slot=operation-progress]"),
    finish,
  )
})

test("progress: Suricata rule jobs keep their working reading during quiet output", async ({
  page,
}) => {
  await mockIntrusion(page)
  const finish = await job(
    page,
    "suricata-progress",
    "suricata.rules",
    "Update Suricata rules",
    "/security/suricata/rules/update",
  )
  await page.goto("/security/intrusion")
  const start = page.getByRole("button", { name: "Update rules", exact: true })
  await start.scrollIntoViewIfNeeded()
  await recordProgress(
    page,
    "security-suricata-job",
    () => start.click(),
    page.locator("[data-slot=operation-progress]"),
    finish,
  )
})

test("progress: installing a network tool keeps its working reading during quiet output", async ({
  page,
}) => {
  await openMaturity(page, [], {
    "/network/ebpf": { ...ebpf, installed: false, package: "bpftool" },
  })
  const finish = await job(
    page,
    "network-install-progress",
    "packages.install",
    "Install bpftool",
    "/packages/install",
  )
  // This recording watches the installer; unrelated live charts need no
  // samples and would spend its screenshot budget redrawing off screen.
  await page.route("**/api/v1/network/links", (route) => route.fulfill({ json: [] }))
  await page.route("**/api/v1/network/traffic/live**", (route) =>
    route.fulfill({ json: { now: 0, sampledAt: 0, stepSeconds: 2, series: {}, tcp: [] } }),
  )
  await page.goto("/network/traffic")
  const start = page.getByRole("button", { name: "Install bpftool", exact: true })
  await start.scrollIntoViewIfNeeded()
  await recordProgress(
    page,
    "network-install-job",
    () => start.click(),
    page.locator("[data-slot=operation-progress]"),
    finish,
  )
})

test("progress: Remote Desktop install keeps its working reading during quiet output", async ({
  page,
}) => {
  await mockRemoteDesktop(page, {
    overview: overview({
      engine: { ...ENGINE, state: "missing", pulled: false, address: undefined },
      computers: [],
      sessions: [],
    }),
  })
  const finish = await job(
    page,
    "remote-install-progress",
    "remote-desktop.install",
    "Install Remote Desktop",
    "/remote-desktop/engine",
  )
  await page.goto("/remote-desktop")
  await recordProgress(
    page,
    "remote-desktop-install-job",
    () => page.getByRole("button", { name: "Install Remote Desktop", exact: true }).click(),
    page.locator("[data-slot=operation-progress]"),
    finish,
  )
})

test("progress: Stream bridge install keeps its working reading during quiet output", async ({
  page,
}) => {
  await mockRemoteDesktop(page, {
    stream: streamView({ bridge: { ...BRIDGE, state: "missing", address: undefined } }),
  })
  const finish = await job(
    page,
    "stream-install-progress",
    "remote-desktop.stream.install",
    "Install Stream mode",
    "/remote-desktop/stream",
  )
  await page.goto(`/remote-desktop/${OFFICE}?mode=stream`)
  await recordProgress(
    page,
    "remote-desktop-stream-job",
    () => page.getByRole("button", { name: "Install Stream mode", exact: true }).click(),
    page.locator("[data-slot=operation-progress]"),
    finish,
  )
})

test("progress: applying SSH settings keeps its job visible through quiet output", async ({
  page,
}) => {
  await mockIntrusion(page)
  const finish = await job(page, "ssh-progress", "ssh.apply", "Apply SSH settings", "/ssh/config")
  await page.goto("/security/ssh")
  await page.getByRole("textbox", { name: "Sessions per connection" }).fill("4")
  await page.getByRole("button", { name: "Test and apply", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Apply SSH changes", exact: true })
  await recordProgress(
    page,
    "security-ssh-job",
    () => dialog.getByRole("button", { name: "Test and apply", exact: true }).click(),
    page.locator("[data-slot=operation-progress]"),
    finish,
  )
})
