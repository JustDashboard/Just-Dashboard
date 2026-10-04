import type { Page } from "@playwright/test"
import type { WorkloadCandidate } from "../../src/lib/workload-import"
import { json, user } from "./deploy-fixture"

export const betBot: WorkloadCandidate = {
  key: "stack:bet-bot",
  kind: "stack",
  name: "bet-bot",
  resourceId: "bet-bot",
  state: "partial",
  running: 2,
  total: 4,
  services: [
    {
      name: "api",
      resourceId: "bot-api",
      state: "running",
      health: "unhealthy",
      image: "betbot/api:latest",
      ports: [{ hostIp: "127.0.0.1", hostPort: 3000, containerPort: 3000, protocol: "tcp" }],
    },
    { name: "postgres", resourceId: "bot-postgres", state: "running", image: "postgres:16" },
    { name: "worker", resourceId: "bot-worker", state: "exited", image: "betbot/worker:latest" },
    {
      name: "scheduler",
      resourceId: "bot-scheduler",
      state: "exited",
      image: "betbot/scheduler:latest",
    },
  ],
  sourcePath: "/srv/bet-bot/compose.yml",
  managerUrl: "/docker/stacks/bet-bot",
  configurationAvailable: true,
  warnings: ["Two services are stopped; importing keeps them stopped."],
  digest: "reviewed-digest",
}

export const hostApp: WorkloadCandidate = {
  key: "process:1234",
  kind: "process",
  name: "next-server",
  resourceId: "1234",
  state: "running",
  running: 1,
  total: 1,
  services: [
    {
      name: "next-server",
      resourceId: "1234",
      state: "running",
      pid: 1234,
      ports: [{ hostIp: "::", hostPort: 3001, containerPort: 3001, protocol: "tcp" }],
    },
  ],
  managerUrl: "/processes",
  configurationAvailable: false,
  warnings: ["No process manager was found. Restart configuration must be supplied separately."],
  digest: "host-digest",
}

export async function mockWorkloadImport(page: Page, items = [betBot, hostApp]) {
  let inspectCount = 0
  const registrations: Record<string, unknown>[] = []
  const calls: { path: string; method: string }[] = []
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    calls.push({ path, method: request.method() })
    if (path === "/auth/session") return json(route, user)
    if (path === "/dashboard/update") return json(route, { current: "0.7.0", latest: "0.7.0" })
    if (path === "/deploy/" && url.searchParams.get("view") === "fleet") {
      return json(route, {
        deployments: [],
        activeWork: [],
        slots: { heavyUsed: 0, heavyCapacity: 2, lightUsed: 0, lightCapacity: 4 },
      })
    }
    if (path === "/deploy/import/discovery") {
      return json(route, {
        checkedAt: new Date().toISOString(),
        items,
        silences: ["PM2 is unavailable for this account."],
      })
    }
    if (path === "/deploy/import/inspect") {
      inspectCount += 1
      const key = request.postDataJSON().key
      const item = items.find((item) => item.key === key)
      return json(route, item)
    }
    if (path === "/deploy/import/register") {
      registrations.push(request.postDataJSON())
      return json(route, { projectId: 77, environmentId: 78, created: true })
    }
    return json(route, [])
  })
  return { inspections: () => inspectCount, registrations, calls }
}
