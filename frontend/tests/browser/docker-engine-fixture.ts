import type { Page } from "@playwright/test"
import { DOCKER_CONTAINERS, DOCKER_EVENTS, mockDockerHost, type DockerHost } from "./docker-fixture"

/**
 * What the Docker overview's picture reads beside the containers and the
 * compose projects of `docker-fixture`: the host's networks and its volumes.
 *
 * Three compose networks and Docker's own bridge carrying the two containers
 * started by hand; a network left over from a project taken down; `host` and
 * `none`, which carry nothing and are not drawn. Five volumes: the shop's
 * database and uploads, Grafana's and n8n's data, and one nothing mounts any
 * more, which is what a prune would delete.
 */

const MB = 1024 * 1024
const GB = 1024 * MB
const ago = (days: number) => new Date(Date.now() - days * 86_400_000).toISOString()

const names = (stack: string | undefined) =>
  DOCKER_CONTAINERS.filter((c) => c.composeStack === stack).map((c) => c.name)

function network(
  id: string,
  name: string,
  usedBy: string[],
  owner: { kind: string; project?: string },
  over: Record<string, unknown> = {},
) {
  return {
    id: id.padEnd(64, "0"),
    name,
    driver: name === "host" ? "host" : name === "none" ? "null" : "bridge",
    scope: "local",
    internal: false,
    attachable: false,
    ipv6: false,
    created: ago(40),
    labels: owner.project ? { "com.docker.compose.project": owner.project } : {},
    subnets: [],
    containers: usedBy.length,
    usedBy,
    membersKnown: true,
    owner,
    ...over,
  }
}

export const ENGINE_NETWORKS = [
  network("1a2b3c4d5e6f", "shop_default", names("shop"), { kind: "compose", project: "shop" }),
  network("2b3c4d5e6f1a", "monitoring_default", names("monitoring"), {
    kind: "compose",
    project: "monitoring",
  }),
  network("3c4d5e6f1a2b", "automation_default", names("automation"), {
    kind: "compose",
    project: "automation",
  }),
  network("4d5e6f1a2b3c", "bridge", names(undefined), { kind: "system" }),
  network("5e6f1a2b3c4d", "wiki_default", [], { kind: "compose", project: "wiki" }),
  network("6f1a2b3c4d5e", "host", [], { kind: "system" }),
  network("7a1b2c3d4e5f", "none", [], { kind: "system" }),
]

function volume(name: string, size: number, users: string[], project?: string) {
  return {
    name,
    driver: "local",
    mountpoint: `/var/lib/docker/volumes/${name}/_data`,
    createdAt: ago(60),
    scope: "local",
    labels: project ? { "com.docker.compose.project": project } : {},
    size,
    refCount: users.length,
    inUse: users.length > 0,
    usedBy: users.map((user) => {
      const c = DOCKER_CONTAINERS.find((each) => each.name === user)!
      return {
        id: c.id,
        name: c.name,
        state: c.state,
        destination: "/data",
        readOnly: false,
        stack: c.composeStack,
      }
    }),
  }
}

export const ENGINE_VOLUMES = [
  volume("shop_pgdata", 2.1 * GB, ["shop-db"], "shop"),
  volume("shop_uploads", 640 * MB, ["shop-api", "shop-web"], "shop"),
  volume("monitoring_grafana", 180 * MB, ["monitoring-grafana"], "monitoring"),
  volume("automation_n8n", 96 * MB, ["automation-n8n"], "automation"),
  volume("wiki_pages", 310 * MB, [], "wiki"),
]

/**
 * The host's events, with the worker's kill for memory noted where Docker
 * notes it: a moment before the exit it caused. `docker-fixture` puts the two
 * two seconds apart, which the overview's Recent block reads as one and the
 * Containers table as a stop — and the overview's verdict opens that table.
 */
export const ENGINE_EVENTS = DOCKER_EVENTS.map((event, index, all) =>
  event.action === "oom"
    ? { ...event, time: new Date(Date.parse(all[index - 1].time) - 300).toISOString() }
    : event,
)

export type EngineHost = DockerHost & {
  networks?: object[]
  volumes?: object[]
  /** Answer the networks and volumes reads with this status instead: an account they refuse. */
  refuse?: number
}

/** `mockDockerHost`, with the two reads the overview's picture adds. */
export async function mockDockerEngine(page: Page, host: EngineHost = {}) {
  const mocks = await mockDockerHost(page, { ...host, events: host.events ?? ENGINE_EVENTS })
  const answer = (body: unknown) => ({
    status: host.refuse ?? 200,
    contentType: "application/json",
    body: JSON.stringify(
      host.refuse ? { error: { code: "forbidden", message: "Not permitted" } } : body,
    ),
  })
  await page.route("**/api/v1/docker/networks/", (route) =>
    route.fulfill(answer(host.networks ?? ENGINE_NETWORKS)),
  )
  await page.route("**/api/v1/docker/volumes/", (route) =>
    route.fulfill(answer(host.volumes ?? ENGINE_VOLUMES)),
  )
  return mocks
}
