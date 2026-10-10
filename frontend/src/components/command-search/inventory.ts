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
import {
  containerProduct,
  hasProductLogo,
  hostProduct,
  pm2Product,
  unitProduct,
} from "@/components/product-logo"
import { projectProduct } from "@/components/deploy/vocabulary"
import { plural, relativeTime } from "@/lib/format"
import { addressIdentity, type SearchItem, type SearchKind } from "./model"

export type InventorySource = {
  kind: SearchKind
  label: string
  read: (signal: AbortSignal) => Promise<InventoryItem[]>
}

/** One line of what the preview says about a resource: a label and its value. */
export type Fact = { label: string; value: string; mono?: boolean }

export type InventoryItem = SearchItem & {
  connection?: Pick<DbConnection, "driver" | "broken">
  /**
   * The product the resource is, as a key into `public/logos/`: forty rows of
   * one grey glyph are found by reading, and Postgres, nginx and n8n are found
   * by their marks before their names are read.
   */
  product?: string
  /** A running state in Docker's, systemd's or PM2's own word, for `Status`. */
  state?: string
  /**
   * What the preview lists under the name, from the same explicitly chosen
   * fields as the rest of the row — never a payload's secrets, environment,
   * notes or remote addresses.
   */
  facts?: Fact[]
  /**
   * The names other resources can know this one by — a domain, a container
   * name, a folder — so the preview can say what it is wired to: the site
   * serving a project's domain, the stack and repository in one folder, the
   * backup covering it. Exact values only; a guess at a relation would be the
   * one fact in the preview that is not true.
   */
  links?: string[]
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

/** The facts that have a value: a fixture or an older backend may leave any of them out. */
function facts(...lines: (Fact | false | undefined)[]): Fact[] {
  return lines.filter((line): line is Fact => !!line && !!line.value)
}

function domainLinks(names: string[]) {
  return names.filter(Boolean).map((name) => `domain:${name.toLowerCase()}`)
}

/** A product only when there is a mark to draw: a key with no file is a guess. */
function product(id: string | undefined) {
  return hasProductLogo(id) ? id : undefined
}

/** Where a backup is written, when that is somebody's service and not this disk. */
const BACKUP_TARGETS: Partial<Record<BackupJob["targetKind"], string>> = { b2: "backblaze" }
const BACKUP_TARGET_NAMES: Record<BackupJob["targetKind"], string> = {
  local: "This server",
  s3: "S3 bucket",
  b2: "Backblaze B2",
}

// Only explicit metadata enters the index. API payloads can contain credentials,
// scripts and arbitrary content, none of which helps someone find a destination.
export const INVENTORY_SOURCES: InventorySource[] = [
  {
    kind: "project",
    label: "Projects",
    read: async (signal) => {
      const fleet = await get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal)
      return (fleet.deployments ?? []).map((project) => ({
        ...resource(
          "project",
          project.id,
          project.name,
          `/deploy/${project.id}`,
          addressIdentity(project.endpoint) || project.environmentName,
          [String(project.id), project.environmentName, project.environmentKind, project.health],
        ),
        product: product(projectProduct(project)),
        facts: facts(
          { label: "Address", value: addressIdentity(project.endpoint), mono: true },
          { label: "Environment", value: project.environmentName },
          { label: "Health", value: project.health },
        ),
        links: domainLinks([addressIdentity(project.endpoint).split("/")[0]]),
      }))
    },
  },
  {
    kind: "site",
    label: "Domains & sites",
    read: async (signal) =>
      (await get<VHost[]>("/proxy/vhosts", undefined, signal)).map((site) => ({
        ...resource(
          "site",
          `${site.kind}:${site.path}:${site.name}`,
          site.name,
          `/proxy/sites/${encodeURIComponent(site.name)}`,
          site.serverNames.join(" · "),
          [...site.serverNames, site.kind, "domain", "proxy"],
        ),
        product: product(site.kind),
        facts: facts(
          { label: "Served by", value: site.kind === "caddy" ? "Caddy" : "nginx" },
          { label: "Domains", value: site.serverNames.join("\n"), mono: true },
          site.enabled === false && { label: "State", value: "Disabled" },
        ),
        links: domainLinks(site.serverNames),
      })),
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
        product: product(conn.flavor) ?? product(conn.driver),
        facts: facts(
          { label: "Engine", value: conn.driver },
          { label: "Host", value: [conn.host, conn.port].filter(Boolean).join(":"), mono: true },
          { label: "Database", value: conn.database, mono: true },
          { label: "Environment", value: conn.environment },
          conn.readOnly && { label: "Access", value: "Protected — read only" },
          conn.broken && { label: "Problem", value: "Cannot be opened" },
        ),
        // A connection made from a container names it; one typed by hand reaches
        // a container on the Docker network by its name, which is its host.
        links: [conn.origin?.match(/^docker:(.+)$/)?.[1], conn.host]
          .filter(Boolean)
          .map((name) => `container:${name}`),
      })),
  },
  {
    kind: "container",
    label: "Containers",
    read: async (signal) =>
      (await get<Container[]>("/docker/containers/", undefined, signal)).map((container) => ({
        ...resource(
          "container",
          container.id,
          container.name,
          `/docker/containers/${encodeURIComponent(container.id)}`,
          container.image,
          [
            container.id,
            container.state,
            container.composeStack ?? "",
            container.composeService ?? "",
            ...container.names,
          ],
        ),
        product: containerProduct(container),
        state: container.state,
        facts: facts(
          { label: "Image", value: container.image, mono: true },
          { label: "Status", value: container.status },
          { label: "Health", value: container.health ?? "" },
          {
            label: "Compose",
            value: [container.composeStack, container.composeService].filter(Boolean).join(" · "),
          },
        ),
        links: [
          ...container.names.map((name) => `container:${name.replace(/^\//, "")}`),
          ...(container.composeStack ? [`stack:${container.composeStack}`] : []),
        ],
      })),
  },
  {
    kind: "stack",
    label: "Stacks",
    read: async (signal) =>
      (await get<ComposeStack[]>("/docker/stacks/", undefined, signal)).map((stack) => ({
        ...resource(
          "stack",
          stack.name,
          stack.name,
          `/docker/stacks/${encodeURIComponent(stack.name)}`,
          stack.summary,
          [stack.workingDir, ...stack.declared],
        ),
        product: "docker-compose",
        facts: facts(
          { label: "Status", value: stack.summary },
          { label: "Folder", value: stack.workingDir, mono: true },
          { label: "Services", value: stack.declared.join(", ") },
        ),
        links: [`stack:${stack.name}`, `folder:${stack.workingDir}`],
      })),
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
      return inventory.repos.map((repo) => ({
        ...resource(
          "repo",
          repo.path,
          repo.name,
          `/git?repo=${encodeURIComponent(repo.path)}`,
          `${repo.branch} · ${repo.path}`,
          [repo.branch, repo.path],
        ),
        product: hostProduct(repo.remote) ?? "git",
        facts: facts(
          {
            label: "Branch",
            value: repo.upstream ? `${repo.branch} → ${repo.upstream}` : repo.branch,
            mono: true,
          },
          { label: "Folder", value: repo.path, mono: true },
          { label: "Last commit", value: repo.subject ?? "" },
          typeof repo.changes === "number" && {
            label: "Working tree",
            value: repo.changes ? plural(repo.changes, "change") : "Clean",
          },
        ),
        links: [`folder:${repo.path}`],
      }))
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
      return inventory.units.map((unit) => ({
        ...resource(
          "service",
          unit.name,
          unit.name,
          `/processes/services?unit=${encodeURIComponent(unit.name)}`,
          unit.description,
          [unit.activeState],
        ),
        product: unitProduct(unit.name),
        state: unit.activeState,
        facts: facts(
          { label: "State", value: [unit.activeState, unit.subState].filter(Boolean).join(" · ") },
          { label: "At boot", value: unit.unitFileState },
          { label: "Restarts", value: unit.restarts ? String(unit.restarts) : "" },
        ),
      }))
    },
  },
  {
    kind: "app",
    label: "PM2 apps",
    read: async (signal) => {
      const inventory = await get<PM2Inventory>("/pm2/", undefined, signal)
      return (inventory.processes ?? []).map((app) => ({
        ...resource(
          "app",
          `${app.daemonId}:${app.id}`,
          app.name,
          `/processes/pm2?app=${encodeURIComponent(`${app.daemonId}:${app.id}`)}`,
          `${app.user} · ${app.namespace}`,
          [app.daemonId, String(app.id), app.status],
        ),
        product: pm2Product(app.interpreter) ?? "pm2",
        state: app.status,
        facts: facts(
          { label: "Runs as", value: app.user },
          { label: "Namespace", value: app.namespace },
          { label: "Mode", value: app.execMode },
          { label: "Restarts", value: app.restarts ? String(app.restarts) : "" },
        ),
      }))
    },
  },
  {
    kind: "backup",
    label: "Backups",
    read: async (signal) =>
      (await get<BackupJob[]>("/backups/", undefined, signal)).map((job) => ({
        ...resource(
          "backup",
          job.id,
          job.name,
          `/backups/${job.id}`,
          `${job.targetKind} · ${job.schedule || "Manual"}`,
          [String(job.id), ...job.sources],
        ),
        product: BACKUP_TARGETS[job.targetKind],
        facts: facts(
          { label: "Writes to", value: BACKUP_TARGET_NAMES[job.targetKind] },
          { label: "Schedule", value: job.schedule || "Manual", mono: !!job.schedule },
          { label: "Covers", value: job.sources.join("\n"), mono: true },
          {
            label: "Last success",
            value: job.lastSuccessAt ? relativeTime(job.lastSuccessAt) : "",
          },
        ),
        links: job.sources.map((source) => `folder:${source.replace(/\/+$/, "")}`),
      })),
  },
  {
    kind: "board",
    label: "Boards",
    read: async (signal) =>
      (await get<BoardSummary[]>("/boards/", undefined, signal)).map((board) => ({
        ...resource("board", board.id, board.name, `/boards/${board.id}`, undefined, [
          String(board.id),
        ]),
        facts: facts({
          label: "Edited",
          value: board.updatedAt ? relativeTime(board.updatedAt) : "",
        }),
      })),
  },
]
