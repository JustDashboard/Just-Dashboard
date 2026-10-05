import type { Page } from "@playwright/test"
import type { WorkloadCandidate } from "../../src/lib/workload-import"
import type { DeploymentDraft, DeploymentConfiguration } from "../../src/lib/types"
import { json, user, mockNewProject, now } from "./deploy-fixture"

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

export function recoveredWorkloadDraft(item = betBot, name = item.name): DeploymentDraft {
  return {
    id: "recovered-workload-draft",
    ownerUsername: "operator",
    currentStep: "configuration",
    revision: 5,
    environmentKeys: ["DATABASE_PASSWORD"],
    data: {
      intent: { name, profile: "compose" },
      source: {
        kind: "compose",
        mode: "compose_local",
        localPath: "/srv/bet-bot",
        composeFiles: [
          {
            path: "compose.yml",
            order: 0,
            content: "services:\n  api:\n    image: betbot/api:latest\n",
          },
        ],
      },
      configuration: {
        build: { method: "compose", primaryService: "api" },
        runtime: {
          internalPort: 3000,
          hostPort: 3000,
          bindAddress: "127.0.0.1",
          strategy: "stop_first",
          mounts: [
            { source: "bet-bot_db", target: "/var/lib/postgresql/data", ownership: "linked" },
          ],
        },
        variables: [
          { name: "DATABASE_PASSWORD", sensitivity: "secret", scopes: ["runtime"], required: true },
        ],
        dependencies: [
          {
            kind: "storage",
            resourceKind: "volume",
            resourceId: "bet-bot_db",
            ownership: "linked",
            config: { backup: false },
          },
        ],
        checks: [],
        domains: [],
      },
      adoption: {
        key: item.key,
        digest: item.digest,
        kind: item.kind,
        resourceId: item.resourceId,
        manager: "docker",
        name: item.name,
        warnings: ["Deploy changes stops the original runtime before its replacement starts."],
        blockers: [],
        serviceCount: item.total,
        runningCount: item.running,
        ...(item.kind === "stack" ? { scope: "all_services" as const, excludedServices: [] } : {}),
      },
    },
    findings: [],
    planPreview: "",
    updatedAt: now,
    expiresAt: "2027-01-01T00:00:00Z",
  }
}

export async function mockWorkloadImport(
  page: Page,
  items = [betBot, hostApp],
  initialDraft?: DeploymentDraft,
) {
  await mockNewProject(page)
  let inspectCount = 0
  const recoveries: Record<string, unknown>[] = []
  const adoptions: Record<string, unknown>[] = []
  const configurationSaves: Record<string, unknown>[] = []
  let draft = initialDraft ?? recoveredWorkloadDraft(items[0])
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
    if (path === "/deploy/import/recover") {
      const body = request.postDataJSON()
      recoveries.push(body)
      draft = recoveredWorkloadDraft(
        items.find((item) => item.key === body.key),
        body.name,
      )
      if (body.scope === "existing_services") {
        draft.data.adoption!.scope = "existing_services"
        draft.data.adoption!.excludedServices = ["optional-worker", "unstarted-cache"]
      }
      return json(route, draft)
    }
    if (path === `/deploy/drafts/${draft.id}`) {
      if (request.method() === "PUT") {
        const body = request.postDataJSON() as {
          configuration?: DeploymentConfiguration
          intent?: DeploymentDraft["data"]["intent"]
        }
        configurationSaves.push(body)
        draft = {
          ...draft,
          revision: draft.revision + 1,
          data: {
            ...draft.data,
            ...(body.configuration ? { configuration: body.configuration } : {}),
            ...(body.intent ? { intent: body.intent } : {}),
          },
        }
      }
      return json(route, draft)
    }
    if (path === `/deploy/drafts/${draft.id}/preflight`) {
      draft = { ...draft, revision: draft.revision + 1 }
      return json(route, {
        draft,
        preflight: {
          revision: draft.revision,
          findings: [
            {
              code: "adoption_warning_1",
              severity: "warning",
              title: "Review recovered runtime behavior",
              measured: "Deploy changes stops the original runtime before its replacement starts.",
              means:
                "Adoption records the current runtime. A later Deploy applies the reviewed recipe.",
            },
            ...(draft.data.adoption?.excludedServices?.length
              ? [
                  {
                    code: "adoption_warning_2",
                    severity: "warning",
                    title: "Review excluded Compose services",
                    measured:
                      "optional-worker and unstarted-cache have no container and are excluded.",
                    means:
                      "Deploy changes will not create these services. All running and stopped containers remain included.",
                  },
                ]
              : []),
          ],
          expectedDowntime: true,
          preview: "",
          digest: "plan-digest",
          plan: { actions: [] },
        },
      })
    }
    if (path === "/deploy/import/adopt") {
      adoptions.push(request.postDataJSON())
      return json(route, { projectId: 77, environmentId: 78, created: true })
    }
    return route.fallback()
  })
  return { inspections: () => inspectCount, recoveries, adoptions, configurationSaves, calls }
}
