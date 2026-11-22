import type { Page, Route } from "@playwright/test"
import type {
  GitHubConnection,
  GitHubCreateOptions,
  GitHubRepository,
  GitHubSetup,
} from "../../src/lib/github-types"
import {
  apiError,
  connections as baseConnections,
  installations,
  iso,
  json,
  localRepos,
  mockGitHub,
  now,
  repositories,
  setup as baseSetup,
  type GitHubMockOptions,
} from "./github-fixture-data"

/**
 * The GitHub list page's routes over the shared fixture: the sign-in flows,
 * the App's setup, creating and cloning a repository, refreshing access.
 * Every mutation is recorded in `calls` (method, path and body), so a spec
 * asserts what the page asked for, not only what it drew.
 */

export type Call = { method: string; path: string; body?: unknown }

/** The App with this spec server's own address registered, so the web sign-in starts. */
export function setupFor(origin: string): GitHubSetup {
  return {
    ...baseSetup,
    app: { ...baseSetup.app!, callbackUrls: [`${origin}/github/callback`] },
  }
}

/** Forty more repositories than the fixture, so Show more has something to add. */
export function manyRepositories(count = 40): GitHubRepository[] {
  const extra = Array.from({ length: count }, (_, index) => ({
    ...repositories[1],
    id: 20_000 + index,
    name: `tool-${String(index).padStart(2, "0")}`,
    fullName: `mira-dev/tool-${String(index).padStart(2, "0")}`,
    description: undefined,
    pushedAt: iso(now - (100 + index) * 86_400_000),
    updatedAt: iso(now - (100 + index) * 86_400_000),
  }))
  return [...repositories, ...extra]
}

export const createOptions: GitHubCreateOptions = {
  owners: [
    {
      login: "mira-dev",
      type: "User",
      installationId: 11,
      repositorySelection: "all",
      canCreate: true,
    },
    {
      login: "northwind",
      type: "Organization",
      installationId: 12,
      repositorySelection: "selected",
      canCreate: true,
      note: "Installed on selected repositories: add the new one on GitHub to see it here.",
    },
    {
      login: "acme-labs",
      type: "Organization",
      installationId: 13,
      repositorySelection: "all",
      canCreate: false,
      note: "The App was not granted administration: write on acme-labs.",
    },
  ],
  gitignoreTemplates: ["Go", "Node", "Python"],
  licenses: [
    { key: "mit", name: "MIT License", spdxId: "MIT" },
    { key: "apache-2.0", name: "Apache License 2.0", spdxId: "Apache-2.0" },
  ],
}

export type PageMockOptions = Omit<GitHubMockOptions, "handle"> & {
  /** Answers `/github/connections/{id}/repositories` before the shared fixture does. */
  list?: (route: Route, connection: number, url: URL) => Promise<boolean> | boolean
  device?: "complete" | "disabled"
  /** What the create route answers: a success, or GitHub's refusal. */
  create?: "ok" | "unreachable" | "name-taken"
  /** The clone job's end. */
  clone?: "succeeded" | "failed"
  handle?: GitHubMockOptions["handle"]
}

export async function mockGitHubPage(page: Page, options: PageMockOptions = {}) {
  const calls: Call[] = []
  let deviceReads = 0
  let cloned = false
  const conns: GitHubConnection[] = options.connections ?? baseConnections

  await page.routeWebSocket(/\/api\/v1\/jobs\/[^/]+\/stream/, (ws) => {
    const job = cloneJob("running")
    ws.send(
      JSON.stringify({
        type: "output",
        data: [
          { seq: 1, stream: "stderr", text: "Cloning into 'api'...", at: iso(Date.now()) },
          {
            seq: 2,
            stream: "stderr",
            text: "Receiving objects: 100% (42/42), done.",
            at: iso(Date.now()),
          },
        ],
      }),
    )
    setTimeout(() => {
      cloned = options.clone !== "failed"
      ws.send(
        JSON.stringify({
          type: "job",
          data: {
            ...job,
            status: options.clone === "failed" ? "failed" : "succeeded",
            error:
              options.clone === "failed"
                ? "fatal: destination path '/srv/api' already exists"
                : undefined,
            endedAt: iso(Date.now()),
            lines: 2,
          },
        }),
      )
    }, 300)
  })

  await mockGitHub(page, {
    ...options,
    connections: conns,
    handle: async (route, path, url) => {
      const request = route.request()
      const method = request.method()
      if (method !== "GET") {
        calls.push({ method, path, body: request.postDataJSON?.() ?? undefined })
      }
      if (options.handle && (await options.handle(route, path, url))) return true
      if (path === "/git/" && cloned) {
        await json(route, {
          available: true,
          repos: [
            ...localRepos,
            {
              ...localRepos[0],
              path: "/srv/api",
              name: "api",
              remote: "https://github.com/northwind/api.git",
            },
          ],
        })
        return true
      }
      const list = path.match(/^\/github\/connections\/(\d+)\/repositories$/)
      if (list && method === "GET" && options.list) {
        if (await options.list(route, Number(list[1]), url)) return true
      }
      if (path === "/github/app/manifest" && method === "POST") {
        await json(route, {
          action: "https://github.com/settings/apps/new?state=manifest-state",
          state: "manifest-state",
          manifest: { name: "Just Dashboard Repos" },
        })
        return true
      }
      if (path === "/github/app/callback-urls" && method === "POST") {
        const body = request.postDataJSON() as { origin: string }
        await json(route, setupFor(body.origin))
        return true
      }
      if (path === "/github/connections/authorize" && method === "POST") {
        await json(route, {
          url: "https://github.com/login/oauth/authorize?client_id=Iv1.fixture&state=s1",
        })
        return true
      }
      if (path === "/github/connections/device" && method === "POST") {
        if (options.device === "disabled") {
          await apiError(
            route,
            409,
            "github_device_flow_disabled",
            "Code sign-in is off for this App: tick Enable Device Flow in its settings on GitHub.",
          )
          return true
        }
        await json(route, {
          id: "flow-1",
          userCode: "WDJB-MJHT",
          verificationUri: "https://github.com/login/device",
          expiresIn: 899,
          interval: 1,
        })
        return true
      }
      if (path === "/github/connections/device/flow-1") {
        deviceReads++
        await json(
          route,
          deviceReads < 2 ? { status: "pending" } : { status: "complete", connection: conns[0] ?? baseConnections[0] },
        )
        return true
      }
      if (path === "/github/callback" && method === "POST") {
        await json(route, { kind: "connection", connection: conns[0] ?? baseConnections[0] })
        return true
      }
      if (/^\/github\/connections\/\d+\/refresh$/.test(path) && method === "POST") {
        await json(route, conns[0])
        return true
      }
      if (/^\/github\/connections\/\d+\/create-options$/.test(path)) {
        await json(route, createOptions)
        return true
      }
      if (/^\/github\/connections\/\d+\/repositories$/.test(path) && method === "POST") {
        const body = request.postDataJSON() as { owner: string; name: string }
        if (options.create === "name-taken") {
          await apiError(route, 422, "github_validation", "name already exists on this account")
          return true
        }
        const repository = {
          ...repositories[1],
          id: 31_000,
          name: body.name,
          fullName: `${body.owner}/${body.name}`,
        }
        await json(route, {
          repository,
          reachable: options.create !== "unreachable",
          notice:
            options.create === "unreachable"
              ? "northwind installs the App on selected repositories: add it there to see it here."
              : undefined,
        })
        return true
      }
      if (/\/clone$/.test(path) && method === "POST") {
        await json(route, { jobId: "clone-1", target: "/srv/api" }, 202)
        return true
      }
      if (path === "/jobs/clone-1") {
        await json(route, { job: cloneJob("running"), lines: [] })
        return true
      }
      if (/^\/github\/connections\/\d+$/.test(path) && method === "DELETE") {
        await route.fulfill({ status: 204 })
        return true
      }
      return false
    },
  })
  return { calls }
}

function cloneJob(status: string) {
  return {
    id: "clone-1",
    kind: "github.clone",
    title: "Clone northwind/api",
    target: "/srv/api",
    status,
    exitCode: 0,
    startedAt: iso(Date.now()),
    lines: 0,
    capability: "service.control",
  }
}

export { installations, repositories }
