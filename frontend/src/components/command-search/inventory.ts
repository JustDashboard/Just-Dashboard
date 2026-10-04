import { get } from "@/lib/api"
import type { BoardSummary } from "@/lib/boards"
import type {
  BackupJob,
  ComposeStack,
  Container,
  DbConnection,
  DeploymentFleet,
  GitRepo,
  PM2Inventory,
  SystemdUnit,
  VHost,
} from "@/lib/types"
import { addressIdentity, type SearchItem, type SearchKind } from "./model"

export type InventorySource = {
  kind: SearchKind
  label: string
  read: (signal: AbortSignal) => Promise<InventoryItem[]>
}

export type InventoryItem = SearchItem & {
  connection?: Pick<DbConnection, "driver" | "broken">
}

function resource(
  kind: SearchKind,
  identity: string | number,
  title: string,
  href: string,
  detail?: string,
  keywords?: string[],
): SearchItem {
  return {
    id: `${kind}:${identity}`,
    kind,
    title,
    href,
    detail,
    keywords: keywords?.filter((value) => typeof value === "string" && value.length > 0),
  }
}

// Only explicit metadata enters the index. API payloads can contain credentials,
// scripts and arbitrary content, none of which helps someone find a destination.
export const INVENTORY_SOURCES: InventorySource[] = [
  {
    kind: "project",
    label: "Projects",
    read: async (signal) => {
      const fleet = await get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal)
      return (fleet.deployments ?? []).map((project) =>
        resource(
          "project",
          project.id,
          project.name,
          `/deploy/${project.id}`,
          addressIdentity(project.endpoint) || project.environmentName,
          [String(project.id), project.environmentName, project.environmentKind, project.health],
        ),
      )
    },
  },
  {
    kind: "site",
    label: "Domains & sites",
    read: async (signal) =>
      (await get<VHost[]>("/proxy/vhosts", undefined, signal)).map((site) =>
        resource(
          "site",
          `${site.kind}:${site.path}:${site.name}`,
          site.name,
          `/proxy/sites/${encodeURIComponent(site.name)}`,
          site.serverNames.join(" · "),
          [...site.serverNames, site.kind, "domain", "proxy"],
        ),
      ),
  },
  {
    kind: "database",
    label: "Databases",
    read: async (signal) =>
      (await get<DbConnection[]>("/databases/", undefined, signal)).map((conn) => ({
        ...resource(
          "database",
          conn.id,
          conn.name,
          `/databases/${conn.id}`,
          `${conn.driver} · ${conn.database || conn.host}${conn.broken ? " · cannot be opened" : ""}`,
          ["open", String(conn.id), conn.host, conn.database, conn.driver, conn.environment],
        ),
        connection: { driver: conn.driver, broken: conn.broken },
      })),
  },
  {
    kind: "container",
    label: "Containers",
    read: async (signal) =>
      (await get<Container[]>("/docker/containers/", undefined, signal)).map((container) =>
        resource(
          "container",
          container.id,
          container.name,
          `/docker/containers/${encodeURIComponent(container.id)}`,
          `${container.state} · ${container.image}`,
          [
            container.id,
            container.composeStack ?? "",
            container.composeService ?? "",
            ...container.names,
          ],
        ),
      ),
  },
  {
    kind: "stack",
    label: "Stacks",
    read: async (signal) =>
      (await get<ComposeStack[]>("/docker/stacks/", undefined, signal)).map((stack) =>
        resource(
          "stack",
          stack.name,
          stack.name,
          `/docker/stacks/${encodeURIComponent(stack.name)}`,
          stack.summary,
          [stack.workingDir, ...stack.declared],
        ),
      ),
  },
  {
    kind: "repo",
    label: "Repositories",
    read: async (signal) => {
      const inventory = await get<{ available: boolean; repos: GitRepo[] }>(
        "/git/",
        undefined,
        signal,
      )
      if (!inventory.available) return []
      return inventory.repos.map((repo) =>
        resource(
          "repo",
          repo.path,
          repo.name,
          `/git?repo=${encodeURIComponent(repo.path)}`,
          `${repo.branch} · ${repo.path}`,
          [repo.branch, repo.path],
        ),
      )
    },
  },
  {
    kind: "service",
    label: "Services",
    read: async (signal) => {
      const inventory = await get<{ available: boolean; units: SystemdUnit[] }>(
        "/systemd/",
        undefined,
        signal,
      )
      if (!inventory.available) return []
      return inventory.units.map((unit) =>
        resource(
          "service",
          unit.name,
          unit.name,
          `/processes/services?unit=${encodeURIComponent(unit.name)}`,
          `${unit.activeState} · ${unit.description}`,
        ),
      )
    },
  },
  {
    kind: "app",
    label: "PM2 apps",
    read: async (signal) => {
      const inventory = await get<PM2Inventory>("/pm2/", undefined, signal)
      return (inventory.processes ?? []).map((app) =>
        resource(
          "app",
          `${app.daemonId}:${app.id}`,
          app.name,
          `/processes/pm2?app=${encodeURIComponent(`${app.daemonId}:${app.id}`)}`,
          `${app.user} · ${app.namespace} · ${app.status}`,
          [app.daemonId, String(app.id)],
        ),
      )
    },
  },
  {
    kind: "backup",
    label: "Backups",
    read: async (signal) =>
      (await get<BackupJob[]>("/backups/", undefined, signal)).map((job) =>
        resource(
          "backup",
          job.id,
          job.name,
          `/backups/${job.id}`,
          `${job.targetKind} · ${job.schedule || "Manual"}`,
          [String(job.id), ...job.sources],
        ),
      ),
  },
  {
    kind: "board",
    label: "Boards",
    read: async (signal) =>
      (await get<BoardSummary[]>("/boards/", undefined, signal)).map((board) =>
        resource("board", board.id, board.name, `/boards/${board.id}`, undefined, [
          String(board.id),
        ]),
      ),
  },
]
