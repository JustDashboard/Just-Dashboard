import type { DockerImage, ImageUpdateStatus } from "@/lib/types"

/** The first tag that names something; an untagged layer has none. */
export function primaryTag(image: Pick<DockerImage, "repoTags">): string | undefined {
  return image.repoTags.find((t) => t && t !== "<none>:<none>")
}

/** The handle `docker` prints, without the digest's algorithm in front of it. */
export function shortId(id: string) {
  return id.replace(/^sha256:/, "").slice(0, 12)
}

/** The image's own tag or digest, as a confirmation names it. */
export function imagePhrase(image: Pick<DockerImage, "id" | "repoTags">): string {
  return primaryTag(image) ?? shortId(image.id)
}

/**
 * A reference in its three parts, as a registry reads it: `ghcr.io/acme/api:main`
 * is ghcr.io's `acme/api` at `main`. A first segment is a registry only when it
 * could be a host — it has a dot or a port, or is `localhost` — which is
 * Docker's own rule; `grafana/grafana` is Docker Hub's.
 */
export function splitReference(reference: string): {
  registry?: string
  repository: string
  tag?: string
  digest?: string
} {
  const [named, digest] = reference.split("@")
  const slash = named.indexOf("/")
  const first = slash === -1 ? "" : named.slice(0, slash)
  const hosted =
    first !== "" && (first.includes(".") || first.includes(":") || first === "localhost")
  const path = hosted ? named.slice(slash + 1) : named
  const colon = path.lastIndexOf(":")
  return {
    registry: hosted ? first : undefined,
    repository: colon === -1 ? path : path.slice(0, colon),
    tag: colon === -1 ? undefined : path.slice(colon + 1),
    digest,
  }
}

/** Where a reference is pulled from, in the words its operator would use. */
export function registryName(reference: string) {
  const { registry } = splitReference(reference)
  return registry ?? "Docker Hub"
}

/**
 * The version the publisher stamped on the image, when they did. The tag is
 * often `latest` or a major line, and the OCI label is the one place the
 * release it actually is gets written down.
 */
export function imageVersion(image: Pick<DockerImage, "labels">): string | undefined {
  return image.labels?.["org.opencontainers.image.version"] || undefined
}

/**
 * What the registry, or the lack of one, says about an image — one word per
 * image, so the band can count them and the table can narrow to them.
 *
 * Only images a container runs are checked, so an image nothing uses is
 * `unused` rather than `unknown`: nobody asked, and a stale layer nothing
 * references is not news.
 */
export type Freshness =
  "outdated" | "current" | "pinned" | "local" | "unknown" | "unused" | "untagged"

export function freshness(image: DockerImage, update: ImageUpdateStatus | undefined): Freshness {
  if (image.dangling || !primaryTag(image)) return "untagged"
  if (update) {
    if (update.state === "outdated") return "outdated"
    if (update.state === "current") return "current"
    if (update.state === "pinned") return "pinned"
    if (update.state === "local") return "local"
    return "unknown"
  }
  if (image.containers === 0) return "unused"
  return image.repoDigests.length === 0 ? "local" : "unknown"
}

/** The order the band lists them in: what wants doing first. */
export const FRESHNESS_ORDER: Freshness[] = [
  "outdated",
  "current",
  "pinned",
  "local",
  "unknown",
  "unused",
  "untagged",
]

export const FRESHNESS_LABEL: Record<Freshness, string> = {
  outdated: "Update available",
  current: "Current",
  pinned: "Pinned by digest",
  local: "Built here",
  unknown: "Not checked",
  unused: "Not in use",
  untagged: "Untagged",
}

/**
 * The colour of each answer. Green and amber are readings of state; the
 * other three are kinds of reference — pinned and built here are choices
 * somebody made — so they take `--tag-*` hues that cannot be read as one,
 * and no answer is the muted ink.
 */
export const FRESHNESS_COLOR: Record<Freshness, string> = {
  outdated: "var(--warning)",
  current: "var(--success)",
  pinned: "var(--tag-cyan)",
  local: "var(--tag-violet)",
  unknown: "color-mix(in oklab, var(--muted-foreground) 55%, transparent)",
  unused: "color-mix(in oklab, var(--muted-foreground) 30%, transparent)",
  untagged: "color-mix(in oklab, var(--muted-foreground) 18%, transparent)",
}

export type Use = "all" | "running" | "unused" | "untagged"

export type ImageSort = "size" | "created" | "name"

/** The table's rows, narrowed by the toolbar and the band and in the chosen order. */
export function visibleImages(
  images: DockerImage[],
  {
    query,
    use,
    state,
    sort,
    states,
  }: {
    query: string
    use: Use
    state: Freshness | ""
    sort: ImageSort
    states: Map<string, Freshness>
  },
) {
  const needle = query.trim().toLowerCase()
  const rows = images.filter((image) => {
    if (use === "running" && image.containers === 0) return false
    if (use === "unused" && (image.containers > 0 || image.dangling)) return false
    if (use === "untagged" && !image.dangling) return false
    if (state && states.get(image.id) !== state) return false
    if (!needle) return true
    return (
      image.repoTags.some((t) => t.toLowerCase().includes(needle)) ||
      shortId(image.id).includes(needle) ||
      (imageVersion(image) ?? "").toLowerCase().includes(needle)
    )
  })
  return rows.sort((a, b) => {
    if (sort === "created") return Date.parse(b.created) - Date.parse(a.created)
    if (sort === "name") {
      const an = primaryTag(a)
      const bn = primaryTag(b)
      // Untagged layers have no name to sort by and go last.
      if (!an || !bn) return an ? -1 : bn ? 1 : b.size - a.size
      return an.localeCompare(bn)
    }
    return b.size - a.size
  })
}

/* ------------------------------------------------------------------ pull -- */

export type PullFrame = { id?: string; status: string; progress?: string }

export type PullLayer = {
  id: string
  /** Docker's own word for where the layer is. */
  status: string
  phase: "waiting" | "downloading" | "extracting" | "done" | "cached"
  /** Bytes moved and expected, while Docker says. */
  current?: number
  total?: number
  /**
   * The same two figures as Docker printed them — in its decimal units, so
   * the row reads what the raw output beside it says.
   */
  amounts?: string
}

const UNITS: Record<string, number> = { B: 1, kB: 1e3, KB: 1e3, MB: 1e6, GB: 1e9, TB: 1e12 }

/** `41.9MB` as bytes, in Docker's decimal units. */
export function parseSize(text: string): number | undefined {
  const match = /^([\d.]+)\s*([kKMGT]?B)$/.exec(text.trim())
  if (!match) return undefined
  return Number(match[1]) * (UNITS[match[2]] ?? 1)
}

function phaseOf(status: string): PullLayer["phase"] {
  const s = status.toLowerCase()
  if (s.startsWith("already exists")) return "cached"
  if (s.startsWith("pull complete")) return "done"
  if (s.startsWith("extracting")) return "extracting"
  if (s.startsWith("downloading") || s.startsWith("verifying") || s.startsWith("download complete"))
    return "downloading"
  return "waiting"
}

/**
 * The layers a pull is moving, from the frames Docker streams for it.
 *
 * The stream is one line per event and the same layer appears in dozens of
 * them, so the transcript of a large pull is two hundred lines that say where
 * four layers are. Folded by layer id it is four rows, each with the bytes the
 * progress bar Docker draws in text already carries. A frame with no layer id
 * — "Pulling from library/nginx", "Digest: …", "Status: …" — is about the
 * pull as a whole and is the summary line instead.
 */
export function foldPull(frames: PullFrame[]): { layers: PullLayer[]; summary?: string } {
  const layers = new Map<string, PullLayer>()
  let summary: string | undefined
  for (const frame of frames) {
    // A frame whose id is the tag ("Pulling from …" carries it) is not a layer.
    if (!frame.id || !/^[0-9a-f]{12}$/.test(frame.id)) {
      summary = frame.status
      continue
    }
    const previous = layers.get(frame.id)
    const phase = phaseOf(frame.status)
    const amounts = /([\d.]+\s*[kKMGT]?B)\s*\/\s*([\d.]+\s*[kKMGT]?B)/.exec(frame.progress ?? "")
    const next: PullLayer = {
      id: frame.id,
      status: frame.status,
      phase,
      current: amounts ? parseSize(amounts[1]) : undefined,
      total: amounts ? parseSize(amounts[2]) : previous?.total,
      amounts: amounts ? `${amounts[1]} / ${amounts[2]}` : undefined,
    }
    if (phase === "done" || phase === "cached") next.current = next.total
    layers.set(frame.id, next)
  }
  return { layers: [...layers.values()], summary }
}

/** How far through the download a pull is, over the layers whose size is known. */
export function pullShare(layers: PullLayer[]) {
  let moved = 0
  let total = 0
  for (const layer of layers) {
    if (layer.phase === "cached" || !layer.total) continue
    total += layer.total
    // Extracting comes after the bytes are here.
    moved +=
      layer.phase === "downloading"
        ? (layer.current ?? 0)
        : layer.phase === "waiting"
          ? 0
          : layer.total
  }
  return { moved, total }
}

/* ---------------------------------------------------------------- layers -- */

/**
 * A history line as the Dockerfile instruction that wrote it. The classic
 * builder records `/bin/sh -c #(nop) ENV …` for a metadata step and
 * `/bin/sh -c …` for a RUN; BuildKit records the instruction and appends
 * `# buildkit`. Read either way, a layer is a verb and its argument.
 */
export function layerInstruction(createdBy: string): { verb: string; rest: string } {
  let text = createdBy.trim().replace(/\s*# buildkit$/, "")
  if (/^\/bin\/sh -c #\(nop\)\s*/.test(text)) text = text.replace(/^\/bin\/sh -c #\(nop\)\s*/, "")
  else if (/^\/bin\/sh -c /.test(text)) text = `RUN ${text.slice("/bin/sh -c ".length)}`
  text = text.replace(/^RUN \/bin\/sh -c /, "RUN ")
  const match = /^([A-Z]+)\s+([\s\S]*)$/.exec(text)
  return match ? { verb: match[1], rest: match[2] } : { verb: "", rest: text }
}
