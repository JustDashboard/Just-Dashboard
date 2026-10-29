import type {
  DeploymentBuildMethod,
  DeploymentGPUMode,
  DeploymentGPUs,
  DeploymentSysctl,
  DeploymentTmpfs,
  DeploymentUlimit,
  DeploymentUlimitName,
} from "@/lib/types"
import { plural } from "@/lib/format"

/**
 * The process flags of a release's container (`deploy/runtime_flags.go`) as
 * the Runtime page types them: the lists one entry per line in the spelling
 * `docker run` takes — `nofile=65536:65536`, `net.core.somaxconn=1024`,
 * `/tmp 128 MiB` — and the GPU request as a choice. Parsing is the page's,
 * so a line that does not read is refused under its field before a save;
 * what a value may be is the server's (`validateRuntimeFlags`), whose
 * refusal names the field the same way.
 */

/** The limits Docker sets with --ulimit, as the server lists them. */
export const ULIMIT_NAMES: DeploymentUlimitName[] = [
  "core",
  "cpu",
  "data",
  "fsize",
  "locks",
  "memlock",
  "msgqueue",
  "nice",
  "nofile",
  "nproc",
  "rss",
  "rtprio",
  "rttime",
  "sigpending",
  "stack",
]

/** A tmpfs path's size when it names none (`DefaultTmpfsSizeMB`). */
export const DEFAULT_TMPFS_MB = 64

/** Where nginx writes when it serves a static site (`staticRootWrites`). */
export const STATIC_SITE_WRITES = ["/var/cache/nginx", "/run"]

/** The four lists and the GPU choice as the form holds them. */
export type RuntimeFlagsDraft = {
  user: string
  readOnlyRootfs: boolean
  tmpfsText: string
  ulimitsText: string
  sysctlsText: string
  gpuMode: "none" | DeploymentGPUMode
  gpuCount: string
  gpuDevices: string
  gpuPreviews: boolean
}

type FlagsPlan = {
  user?: string
  readOnlyRootfs?: boolean
  tmpfs?: DeploymentTmpfs[]
  ulimits?: DeploymentUlimit[]
  sysctls?: DeploymentSysctl[]
  gpus?: DeploymentGPUs
}

const lines = (text: string) =>
  text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)

const limitValue = (value: number) => (value === -1 ? "unlimited" : String(value))

export function ulimitLine(limit: DeploymentUlimit) {
  return limit.soft === limit.hard
    ? `${limit.name}=${limitValue(limit.soft)}`
    : `${limit.name}=${limitValue(limit.soft)}:${limitValue(limit.hard)}`
}

export function tmpfsLine(entry: DeploymentTmpfs) {
  return entry.sizeMb ? `${entry.path} ${entry.sizeMb} MiB` : entry.path
}

export function runtimeFlagsDraftOf(plan: FlagsPlan): RuntimeFlagsDraft {
  const gpus = plan.gpus
  return {
    user: plan.user ?? "",
    readOnlyRootfs: plan.readOnlyRootfs ?? false,
    tmpfsText: (plan.tmpfs ?? []).map(tmpfsLine).join("\n"),
    ulimitsText: (plan.ulimits ?? []).map(ulimitLine).join("\n"),
    sysctlsText: (plan.sysctls ?? []).map((entry) => `${entry.name}=${entry.value}`).join("\n"),
    gpuMode: gpus?.mode ?? "none",
    gpuCount: gpus?.count ? String(gpus.count) : "",
    gpuDevices: (gpus?.deviceIds ?? []).join(", "),
    gpuPreviews: gpus?.previews ?? false,
  }
}

/** Which draft field a refusal is about, and what it says. */
export type FlagsRefusal = {
  field: "user" | "tmpfsText" | "ulimitsText" | "sysctlsText" | "gpuCount" | "gpuDevices"
  message: string
}

type Parsed<T> = { value: T } | { refusal: FlagsRefusal }

const LIMIT_RE = /^(-1|unlimited|\d{1,19})$/i

function limitNumber(text: string) {
  return /^(-1|unlimited)$/i.test(text) ? -1 : Number(text)
}

export function parseUlimits(text: string): Parsed<DeploymentUlimit[] | undefined> {
  const out: DeploymentUlimit[] = []
  for (const line of lines(text)) {
    const match = /^([a-z]+)\s*=\s*([^:\s]+)(?:\s*:\s*(\S+))?$/i.exec(line)
    const name = match?.[1].toLowerCase() as DeploymentUlimitName | undefined
    if (!match || !name || !LIMIT_RE.test(match[2]) || (match[3] && !LIMIT_RE.test(match[3])))
      return {
        refusal: {
          field: "ulimitsText",
          message: `“${line}” is not a limit: write name=soft:hard, such as nofile=65536:65536.`,
        },
      }
    if (!ULIMIT_NAMES.includes(name))
      return {
        refusal: {
          field: "ulimitsText",
          message: `${name} is not a limit Docker sets; use one of ${ULIMIT_NAMES.join(", ")}.`,
        },
      }
    const soft = limitNumber(match[2])
    out.push({ name, soft, hard: match[3] ? limitNumber(match[3]) : soft })
  }
  return { value: out.length ? out : undefined }
}

const SIZE_RE = /^(\d{1,6})\s*(m|mb|mi|mib)?$/i

export function parseTmpfs(text: string): Parsed<DeploymentTmpfs[] | undefined> {
  const out: DeploymentTmpfs[] = []
  for (const line of lines(text)) {
    const [path, ...rest] = line.split(/\s+/)
    const size = rest.join(" ")
    const sized = size ? SIZE_RE.exec(size) : undefined
    if (!path.startsWith("/") || (size && !sized))
      return {
        refusal: {
          field: "tmpfsText",
          message: `“${line}” is not a writable path: write an absolute path, optionally with its size, such as /tmp 128 MiB.`,
        },
      }
    out.push(sized ? { path, sizeMb: Number(sized[1]) } : { path })
  }
  return { value: out.length ? out : undefined }
}

export function parseSysctls(text: string): Parsed<DeploymentSysctl[] | undefined> {
  const out: DeploymentSysctl[] = []
  for (const line of lines(text)) {
    const at = line.indexOf("=")
    const name = at > 0 ? line.slice(0, at).trim() : ""
    const value = at > 0 ? line.slice(at + 1).trim() : ""
    if (!name || !value)
      return {
        refusal: {
          field: "sysctlsText",
          message: `“${line}” is not a kernel parameter: write name=value, such as net.core.somaxconn=1024.`,
        },
      }
    out.push({ name, value })
  }
  return { value: out.length ? out : undefined }
}

export function parseGPUs(draft: RuntimeFlagsDraft): Parsed<DeploymentGPUs | undefined> {
  const previews = draft.gpuPreviews || undefined
  switch (draft.gpuMode) {
    case "none":
      return { value: undefined }
    case "all":
      return { value: { mode: "all", previews } }
    case "count": {
      const count = Number(draft.gpuCount.trim())
      if (!/^\d+$/.test(draft.gpuCount.trim()) || count < 1)
        return {
          refusal: { field: "gpuCount", message: "The number of GPUs is a whole number from 1." },
        }
      return { value: { mode: "count", count, previews } }
    }
    case "devices": {
      const deviceIds = draft.gpuDevices
        .split(/[\s,]+/)
        .map((id) => id.trim())
        .filter(Boolean)
      if (deviceIds.length === 0)
        return {
          refusal: {
            field: "gpuDevices",
            message: "Name the GPUs by index or UUID, such as 0, 1 or GPU-3a1b….",
          },
        }
      return { value: { mode: "devices", deviceIds, previews } }
    }
  }
}

/**
 * The draft as the plan saves it, or the first line that does not read. An
 * empty list and no GPUs are left out, as the server stores them, so turning
 * a flag on and off again is not an edit.
 */
export function runtimeFlagsOf(
  draft: RuntimeFlagsDraft,
): { plan: FlagsPlan; refusal?: undefined } | { refusal: FlagsRefusal; plan?: undefined } {
  const tmpfs = parseTmpfs(draft.tmpfsText)
  if ("refusal" in tmpfs) return { refusal: tmpfs.refusal }
  const ulimits = parseUlimits(draft.ulimitsText)
  if ("refusal" in ulimits) return { refusal: ulimits.refusal }
  const sysctls = parseSysctls(draft.sysctlsText)
  if ("refusal" in sysctls) return { refusal: sysctls.refusal }
  const gpus = parseGPUs(draft)
  if ("refusal" in gpus) return { refusal: gpus.refusal }
  return {
    plan: {
      user: draft.user.trim() || undefined,
      readOnlyRootfs: draft.readOnlyRootfs || undefined,
      tmpfs: tmpfs.value,
      ulimits: ulimits.value,
      sysctls: sysctls.value,
      gpus: gpus.value,
    },
  }
}

/** Whether the release is served by nginx, which writes under the root. */
export function servedByNginx(method: DeploymentBuildMethod, outputDirectory?: string) {
  return method === "static" || (method === "recipe" && Boolean(outputDirectory?.trim()))
}

/**
 * The writable paths to start a read-only root with: /tmp, and for a static
 * site where nginx writes its cache and PID file, so the switch turned on is
 * a plan the server saves.
 */
export function readOnlyStartingPaths(method: DeploymentBuildMethod, outputDirectory?: string) {
  return ["/tmp", ...(servedByNginx(method, outputDirectory) ? STATIC_SITE_WRITES : [])]
}

/** The section head's line: what the flags add up to, or nothing when none is set. */
export function runtimeFlagsLine(plan: FlagsPlan) {
  const parts = [
    plan.user && `runs as ${plan.user}`,
    plan.readOnlyRootfs && "read-only root",
    plan.ulimits?.length && plural(plan.ulimits.length, "ulimit"),
    plan.sysctls?.length && plural(plan.sysctls.length, "kernel parameter"),
  ].filter(Boolean)
  return parts.length ? parts.join(" · ") : undefined
}

/** The GPU request in a few words: "every GPU", "2 GPUs", "GPUs 0, 1". */
export function gpusWords(gpus: DeploymentGPUs | undefined) {
  if (!gpus) return undefined
  if (gpus.mode === "all") return "every GPU"
  if (gpus.mode === "count") return plural(gpus.count ?? 0, "GPU")
  return `${(gpus.deviceIds ?? []).length === 1 ? "GPU" : "GPUs"} ${(gpus.deviceIds ?? []).join(", ")}`
}
