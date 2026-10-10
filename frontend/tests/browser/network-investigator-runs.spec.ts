import { expect, test, type Page } from "@playwright/test"
import { json, mockNetwork } from "./network-fixture"
import type { DiagnosticRun } from "../../src/lib/network-diagnostics"
import type { PathRequest } from "../../src/lib/network-investigator-types"

const containerId = "a".repeat(64)
const stamp = "2026-10-08T13:00:00Z"

async function retainedPaths(page: Page) {
  await mockNetwork(page, [], {
    overrides: {
      "/network/investigate/sources": {
        containers: [{ id: containerId, name: "Application container" }],
      },
    },
  })
  let quick = 0
  await page.route("**/api/v1/network/investigate", (route) => {
    quick++
    return json(
      route,
      { error: { code: "unexpected", message: "No quick probe was requested" } },
      400,
    )
  })
  const runs = new Map<string, DiagnosticRun>()
  const requests: { name: string; investigation: PathRequest }[] = []
  let refused = true
  await page.route("**/api/v1/network/diagnostics/**", (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1/network/diagnostics/", "")
    if (path === "policy") return json(route, { maxRuns: 100, maxAgeHours: 168 })
    if (route.request().method() === "GET") {
      if (!path)
        return json(
          route,
          [...runs.values()].map((run) => ({ ...run, investigation: undefined })),
        )
      return json(route, runs.get(path))
    }
    if (path === "investigate") {
      const input = route.request().postDataJSON()
      requests.push(input)
      if (refused) {
        refused = false
        return json(
          route,
          { error: { code: "source_unavailable", message: "Source could not be read" } },
          409,
        )
      }
      const req = input.investigation as PathRequest
      const run: DiagnosticRun = {
        id: "saved-path-1",
        name: input.name,
        kind: "investigation",
        request: { tool: "", target: "" },
        investigationRequest: req,
        scope: {
          vantage: "container_network_namespace",
          source: "Application container",
          sourceAddress: req.sourceAddress,
          target: req.target,
          family: req.family,
          protocol: req.protocol,
          port: req.port,
          mark: req.mark,
          limitations: ["Provider policy remains unknown."],
        },
        status: "completed",
        outcome: "completed_with_unknowns",
        outcomeSource: "path_evidence",
        createdAt: stamp,
        startedAt: stamp,
        endedAt: stamp,
        updatedAt: stamp,
        createdBy: "operator",
        hasResult: true,
        resultTruncated: false,
        stages: [
          { id: "validation", status: "completed" },
          { id: "investigation", status: "completed" },
          { id: "recording", status: "completed" },
        ],
        investigation: {
          request: req,
          scope: {
            vantage: "container_network_namespace",
            source: "Application container",
            sourceAddress: req.sourceAddress,
            target: req.target,
            family: req.family,
            protocol: req.protocol,
            port: req.port,
            mark: req.mark,
            limitations: ["Provider policy remains unknown."],
          },
          startedAt: stamp,
          endedAt: stamp,
          addresses: [],
          comparison: "No connection was measured; the declared source and tuple were retained.",
          evidence: [
            {
              id: "provider",
              title: "Provider firewall",
              basis: "unknown",
              state: "unknown",
              scope: "Remote provider",
              owner: "Provider",
              checkedAt: stamp,
              summary: "Provider policy remains unknown.",
              facts: [],
              limitations: [],
            },
          ],
        },
      }
      runs.set(run.id, run)
      return json(route, run, 202)
    }
    return json(route, {}, 400)
  })
  return { requests, quick: () => quick }
}

for (const width of [390, 1440]) {
  test(`saving a scoped path retains its snapshot after refusal and reload at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 950 })
    const state = await retainedPaths(page)
    await page.goto("/network/investigate")
    await page.getByLabel("Source", { exact: true }).click()
    await page.getByRole("option", { name: "Application container", exact: true }).click()
    await page.getByLabel("Destination", { exact: true }).fill("private.test")
    await page.getByLabel("Family", { exact: true }).click()
    await page.getByRole("option", { name: "IPv6", exact: true }).click()
    await page.getByLabel("Protocol", { exact: true }).click()
    await page.getByRole("option", { name: "UDP", exact: true }).click()
    await page.getByLabel("Port", { exact: true }).fill("8443")
    await page.getByText("Route selectors", { exact: true }).click()
    await page.getByLabel("Source address", { exact: true }).fill("2001:db8::2")
    await page.getByLabel("Mark", { exact: true }).fill("0x1")
    await page.getByRole("button", { name: "Run and save", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Save a diagnostic run" })
    await expect(dialog).toContainText("container aaaaaaaaaaaa")
    await expect(dialog).toContainText("IPv6 · UDP")
    expect(state.requests).toHaveLength(0)
    expect(state.quick()).toBe(0)
    await dialog.getByLabel("Run name", { exact: true }).fill("Private container baseline")
    await dialog.getByRole("button", { name: "Run and save", exact: true }).click()
    await expect(dialog).toContainText("Source could not be read")
    await expect(dialog.getByLabel("Run name", { exact: true })).toHaveValue(
      "Private container baseline",
    )
    await dialog.getByRole("button", { name: "Run and save", exact: true }).click()
    await expect(page).toHaveURL(/\/network\/runs\?run=saved-path-1$/)
    await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
      "Provider policy remains unknown",
    )
    await expect(page.getByRole("region", { name: "Saved diagnostic" })).toContainText(
      "Report completed with unknowns",
    )
    expect(state.requests).toHaveLength(2)
    expect(state.requests[1]).toEqual({
      name: "Private container baseline",
      investigation: {
        sourceKind: "container",
        containerId,
        sourceAddress: "2001:db8::2",
        target: "private.test",
        family: "inet6",
        protocol: "udp",
        port: 8443,
        mark: "0x1",
        measure: false,
      },
    })
    await page.reload()
    await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
      "2001:db8::2",
    )
    await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
      "UDP / 8443",
    )
    expect(state.requests).toHaveLength(2)
    expect(state.quick()).toBe(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}
