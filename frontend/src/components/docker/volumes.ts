import { imageProduct } from "@/components/product-logo"
import { hueFor, LANES } from "@/lib/hue"
import type { BackupResource, VolumeDetail } from "@/lib/types"

/**
 * Where a volume stands with Docker, which is what decides every action on it.
 *
 * The daemon keeps a reference for every container that mounts a volume,
 * running or stopped, and refuses to remove one that has any; its prune takes
 * only the local volumes nothing references. So a stopped stack's data is
 * safe from a prune. What a prune does take is a volume whose containers were
 * *removed*: a stack taken down with `docker compose down`, the anonymous
 * volume of a container deleted without `-v`, a volume made by hand and never
 * mounted. The page says something different about each, so they are told
 * apart here.
 */
export type Standing = "running" | "stopped" | "down" | "loose" | "anonymous"

export const STANDING_ORDER: Standing[] = ["running", "stopped", "down", "loose", "anonymous"]

export const STANDING_LABEL: Record<Standing, string> = {
  running: "In use",
  stopped: "Held by stopped containers",
  down: "Left by a stack",
  loose: "Not mounted",
  anonymous: "Anonymous, not mounted",
}

/** The short word a row carries, where the band's line has the room for a phrase. */
export const STANDING_WORD: Record<Standing, string> = {
  running: "in use",
  stopped: "stopped",
  down: "stack is down",
  loose: "not mounted",
  anonymous: "anonymous",
}

/**
 * Green and amber are readings of state: in use, and a stack's data with
 * nothing left to run it. The other three are kinds of hold — cyan for a
 * container that will start again, violet for a name somebody chose, muted
 * for a hash nobody did — so no kind reads as a second warning.
 */
export const STANDING_COLOR: Record<Standing, string> = {
  running: "var(--success)",
  stopped: "var(--tag-cyan)",
  down: "var(--warning)",
  loose: "var(--tag-violet)",
  anonymous: "color-mix(in oklab, var(--muted-foreground) 45%, transparent)",
}

/** The Volumes span's hue on the Docker overview's disk bar, so a size reads as its share of it. */
export const VOLUME_HUE = "var(--chart-5)"

const UP = new Set(["running", "restarting", "paused"])

export function composeProject(volume: Pick<VolumeDetail, "labels">): string | undefined {
  return volume.labels["com.docker.compose.project"] || undefined
}

/**
 * Docker labels the volumes it makes for a container's unnamed `VOLUME` lines
 * since 23.0; before that the 64-character hash is the only sign.
 */
export function isAnonymous(volume: Pick<VolumeDetail, "name" | "labels">): boolean {
  return "com.docker.volume.anonymous" in volume.labels || /^[0-9a-f]{64}$/.test(volume.name)
}

export function standing(
  volume: Pick<VolumeDetail, "usedBy" | "refCount" | "labels" | "name">,
): Standing {
  if (volume.usedBy.some((u) => UP.has(u.state))) return "running"
  // A reference Docker counted that the container listing could not name is
  // still a hold; calling it unmounted would invite the delete it refuses.
  if (volume.usedBy.length > 0 || volume.refCount > 0) return "stopped"
  if (composeProject(volume)) return "down"
  if (isAnonymous(volume)) return "anonymous"
  return "loose"
}

/**
 * Whether `docker volume prune --all` takes it: the daemon's own filter is a
 * local volume with no driver options that nothing references. A local volume
 * with options is a remote filesystem or a bind, and pruning one would free
 * nothing, so the daemon skips it — as it skips every other driver.
 */
export function prunable(
  volume: Pick<VolumeDetail, "usedBy" | "refCount" | "driver" | "options">,
): boolean {
  return (
    volume.usedBy.length === 0 &&
    volume.refCount <= 0 &&
    volume.driver === "local" &&
    Object.keys(volume.options ?? {}).length === 0
  )
}

/**
 * A volume's size, with "not measured" told apart from "empty". Docker fills
 * the figure in from its disk-usage walk, which only local volumes get; a
 * volume that walk has not reached carries no reference count either.
 */
export function sizeReading(volume: Pick<VolumeDetail, "size" | "driver" | "refCount">): {
  bytes?: number
  word?: "empty" | "not measured" | "not measurable"
} {
  if (volume.size > 0) return { bytes: volume.size }
  if (volume.driver !== "local") return { word: "not measurable" }
  if (volume.refCount < 0) return { word: "not measured" }
  return { word: "empty" }
}

/**
 * The name as Compose wrote it, the project a step back from the volume:
 * down a column of `shop_pgdata`, `shop_redis`, `shop_uploads` it is the part
 * after the underscore that differs.
 */
export function splitName(volume: Pick<VolumeDetail, "name" | "labels">): {
  prefix: string
  rest: string
} {
  const project = composeProject(volume)
  const own = volume.labels["com.docker.compose.volume"]
  if (project && own && volume.name === `${project}_${own}`) {
    return { prefix: `${project}_`, rest: own }
  }
  return { prefix: "", rest: volume.name }
}

/**
 * The product a volume keeps the data of: that of the first container that
 * mounts it with a logo, or — for one nothing mounts — what its stack or its
 * name says, so a stack's leftover `nextcloud_db` is still drawn as
 * Nextcloud. Words shorter than four letters are passed over in a name,
 * where `x` and `r` are a volume's suffix far more often than a product.
 */
export function volumeProduct(
  volume: Pick<VolumeDetail, "name" | "labels" | "usedBy">,
  productOf: Map<string, string>,
): string | undefined {
  for (const user of volume.usedBy) {
    const product = productOf.get(user.id)
    if (product && product !== "docker") return product
  }
  const words = [
    composeProject(volume),
    ...volume.name.split(/[_.-]/).filter((word) => word.length >= 4),
  ]
  for (const word of words) {
    const product = word ? imageProduct(word) : "docker"
    if (product !== "docker") return product
  }
  return undefined
}

export type Holder = {
  key: string
  name: string
  kind: "stack" | "container" | "none"
  volumes: VolumeDetail[]
  size: number
  /** A stack with none of its containers left. */
  down: boolean
}

/**
 * Who keeps their data in a volume: the stack of the first container that
 * mounts it, or the container itself when it runs on its own, or — for a
 * volume nothing mounts — the stack its Compose labels name. The last is the
 * case worth the join: a stack taken down still owns what it left behind.
 */
export function holderOf(volume: Pick<VolumeDetail, "usedBy" | "labels">): {
  key: string
  name: string
  kind: Holder["kind"]
} {
  const stack = volume.usedBy.find((u) => u.stack)?.stack ?? composeProject(volume)
  if (stack) return { key: `stack:${stack}`, name: stack, kind: "stack" }
  const first = volume.usedBy[0]
  if (first) return { key: `container:${first.name}`, name: first.name, kind: "container" }
  return { key: "none", name: "Nothing", kind: "none" }
}

export function holders(volumes: VolumeDetail[]): Holder[] {
  const map = new Map<string, Holder>()
  for (const volume of volumes) {
    const { key, name, kind } = holderOf(volume)
    const holder = map.get(key) ?? { key, name, kind, volumes: [], size: 0, down: true }
    holder.volumes.push(volume)
    holder.size += Math.max(volume.size, 0)
    holder.down &&= kind === "stack" && volume.usedBy.length === 0
    map.set(key, holder)
  }
  return [...map.values()].sort((a, b) => b.size - a.size || a.name.localeCompare(b.name))
}

/** A stack keeps the lane hue the Stacks and Containers pages give it; nothing is muted. */
export function holderHue(holder: Pick<Holder, "kind" | "name">): string {
  if (holder.kind === "none") return "color-mix(in oklab, var(--muted-foreground) 45%, transparent)"
  return hueFor(holder.name, LANES)
}

export type Backup = {
  state: "protected" | "paused" | "none"
  jobs: { id: number; name: string }[]
  lastAt?: string
}

/**
 * What the Backups page says about a volume: covered by a job that runs,
 * covered only by paused ones, or by nothing.
 */
export function backupOf(resource: BackupResource | undefined): Backup | undefined {
  if (!resource) return undefined
  const jobs = resource.coveredBy.map((c) => ({ id: c.jobId, name: c.jobName }))
  if (resource.protected) return { state: "protected", jobs, lastAt: resource.lastBackupAt }
  if (resource.coveredBy.length > 0) return { state: "paused", jobs, lastAt: resource.lastBackupAt }
  return { state: "none", jobs }
}

/**
 * The volume list with each user's state as the containers socket last said,
 * so a dot changes the moment Docker's does rather than at the next poll of
 * the list.
 */
export function withLiveStates(volumes: VolumeDetail[], states: Map<string, string>) {
  if (states.size === 0) return volumes
  return volumes.map((volume) => {
    if (!volume.usedBy.some((u) => states.has(u.id) && states.get(u.id) !== u.state)) return volume
    return {
      ...volume,
      usedBy: volume.usedBy.map((u) => {
        const state = states.get(u.id)
        return state && state !== u.state ? { ...u, state } : u
      }),
    }
  })
}

export type Show = "all" | "running" | "stopped" | "unmounted" | "unprotected"

export type SortKey = "name" | "mounted" | "standing" | "size" | "created"
export type VolumeSort = { key: SortKey; desc: boolean }

/** The way each column reads first: figures largest first, words from the top of the alphabet. */
export const FIRST_DIRECTION: Record<SortKey, boolean> = {
  name: false,
  mounted: true,
  standing: false,
  size: true,
  created: true,
}

export function matches(volume: VolumeDetail, needle: string): boolean {
  if (!needle) return true
  const haystack = [
    volume.name,
    holderOf(volume).name,
    ...volume.usedBy.flatMap((u) => [u.name, u.destination]),
  ]
  return haystack.some((text) => text.toLowerCase().includes(needle))
}

export function inShow(volume: VolumeDetail, show: Show, backup: Backup | undefined): boolean {
  const s = standing(volume)
  switch (show) {
    case "all":
      return true
    case "running":
      return s === "running"
    case "stopped":
      return s === "stopped"
    case "unmounted":
      return s === "down" || s === "loose" || s === "anonymous"
    case "unprotected":
      return backup !== undefined && backup.state !== "protected"
  }
}

export function visibleVolumes(
  volumes: VolumeDetail[],
  options: {
    query: string
    show: Show
    standing: Standing | ""
    holder: string
    sort: VolumeSort
    backups: Map<string, Backup>
  },
): VolumeDetail[] {
  const needle = options.query.trim().toLowerCase()
  const kept = volumes.filter(
    (volume) =>
      matches(volume, needle) &&
      inShow(volume, options.show, options.backups.get(volume.name)) &&
      (!options.standing || standing(volume) === options.standing) &&
      (!options.holder || holderOf(volume).key === options.holder),
  )
  const sign = options.sort.desc ? -1 : 1
  const by = (a: VolumeDetail, b: VolumeDetail): number => {
    switch (options.sort.key) {
      case "name":
        return a.name.localeCompare(b.name)
      case "mounted":
        return a.usedBy.length - b.usedBy.length
      case "standing":
        return STANDING_ORDER.indexOf(standing(a)) - STANDING_ORDER.indexOf(standing(b))
      case "size":
        return a.size - b.size
      case "created":
        return Date.parse(a.createdAt || "0") - Date.parse(b.createdAt || "0")
    }
  }
  return kept.sort((a, b) => sign * by(a, b) || a.name.localeCompare(b.name))
}

/** What a prune would take right now: the volumes and the bytes Docker measured in them. */
export function pruneShare(volumes: VolumeDetail[]) {
  const taken = volumes.filter(prunable)
  return { volumes: taken, size: taken.reduce((sum, v) => sum + Math.max(v.size, 0), 0) }
}
