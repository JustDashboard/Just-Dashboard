import type { Page } from "@playwright/test"
import { DETAIL, mockStackDetail as mockDetail } from "./stack-detail-fixture"

/**
 * The server's other stacks, for the rail beside a stack's page: `shop` (the
 * one `stack-detail-fixture` opens) among a monitoring stack that is partly
 * up, an analytics stack that runs, a mail server stopped on purpose and a
 * wiki that was never deployed. The list's order is running first by name,
 * then stopped, then never deployed.
 */

type Service = { name: string; image: string; state?: string; health?: string }

function stack(name: string, services: Service[], over: Record<string, unknown> = {}) {
  const running = services.filter((s) => (s.state ?? "running") === "running").length
  return {
    name,
    workingDir: `/srv/${name}`,
    configFiles: [`/srv/${name}/compose.yaml`],
    services: services.map((s) => ({
      name: s.name,
      container: `${name}-${s.name}`.padEnd(64, "0"),
      state: s.state ?? "running",
      status: (s.state ?? "running") === "running" ? "Up 3 days" : "Exited (0) 2 days ago",
      image: s.image,
      health: s.health,
      ports: [],
    })),
    running,
    total: services.length,
    managed: true,
    declared: services.map((s) => s.name),
    declaredSource: "compose",
    containers: services.length,
    deployed: true,
    orphans: [],
    state: running === services.length ? "running" : running > 0 ? "partial" : "stopped",
    summary: "",
    ...over,
  }
}

export const STACKS_FOR_RAIL = [
  stack("analytics", [
    { name: "clickhouse", image: "clickhouse/clickhouse-server:24.3-alpine" },
    { name: "plausible", image: "ghcr.io/plausible/community-edition:v2.1.4" },
  ]),
  stack("monitoring", [
    { name: "grafana", image: "grafana/grafana:11.1.0" },
    { name: "prometheus", image: "prom/prometheus:v2.53.0" },
    { name: "alertmanager", image: "prom/alertmanager:v0.27.0", state: "exited" },
  ]),
  // The list's own copy of the stack on screen: its summary, without the checkout.
  { ...DETAIL, git: undefined },
  stack("mailserver", [
    {
      name: "mailserver",
      image: "ghcr.io/docker-mailserver/docker-mailserver:14.0",
      state: "exited",
    },
  ]),
  stack(
    "wiki",
    [
      { name: "wiki", image: "requarks/wiki:2", state: "missing" },
      { name: "db", image: "postgres:16-alpine", state: "missing" },
    ],
    { deployed: false, running: 0, containers: 0, state: "not-deployed", services: [] },
  ),
]

/** `shop` as a stack whose compose file was never brought up. */
export const UNDEPLOYED = {
  ...DETAIL,
  services: DETAIL.services.map((s) => ({
    ...s,
    container: "",
    state: "missing",
    status: "",
    missing: true,
  })),
  running: 0,
  containers: 0,
  deployed: false,
  state: "not-deployed",
  summary: "Not deployed · 6 services defined",
  git: undefined,
}

/** `mockStackDetail`, with the stack list the page's rail reads. */
export async function mockStackDetail(
  page: Page,
  options: Parameters<typeof mockDetail>[1] & { undeployed?: boolean; stacks?: object[] } = {},
) {
  const { undeployed, stacks, ...rest } = options
  const mocks = await mockDetail(
    page,
    undeployed ? { ...rest, containers: [], detail: UNDEPLOYED } : rest,
  )
  await page.route("**/api/v1/docker/stacks/", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(stacks ?? STACKS_FOR_RAIL),
    }),
  )
  return mocks
}
