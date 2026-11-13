/**
 * What a page says where the thing it reads is not there: which thing, why in
 * plain words, and the next step. `MissingThing` (`missing-thing.tsx`) draws
 * one of these; this file is the words, so they are the same on every page
 * and can be checked without a browser.
 *
 * The daemon's own sentence is never the headline. It is kept in `raw`, for
 * the fold the screen calls "What the server said".
 */

/** The next step: a page that helps, or a line to copy into a shell or a file. */
export type MissingStep =
  { kind: "link"; label: string; href: string } | { kind: "copy"; label: string; text: string }

export type Missing = {
  /** The thing drawn as itself: a `ProductLogo` id, or nothing for a module with no mark. */
  thing?: string
  /** Which absence this is, for a page that says one differently and for tests. */
  reason: MissingReason
  title: string
  /** Why, in one or two plain sentences: what was found, then where it is fixed. */
  sentence: string
  steps: MissingStep[]
  /** The subsystem's own words. */
  raw?: string
}

export type MissingReason =
  | "docker-absent"
  | "docker-stopped"
  | "docker-refused"
  | "docker-elsewhere"
  | "docker-silent"
  | "docker-other"
  | "no-systemd"
  | "module-off"

/** Where the host's systemd is looked for (`procs.SystemdRunDir`'s parent, the folder Compose mounts). */
export const SYSTEMD_RUN_DIR = "/run/systemd"

/** The line of the shipped Compose file that gives the backend the host's systemd. */
export const SYSTEMD_MOUNT = `${SYSTEMD_RUN_DIR}:${SYSTEMD_RUN_DIR}`

/** What the page would have listed, as the sentence names it. */
const SYSTEMD_READS = {
  services: "Services are systemd units",
  service: "A service is a systemd unit",
  timers: "Timers are systemd units",
  journal: "The journal is systemd's",
} as const

export type SystemdReader = keyof typeof SYSTEMD_READS

/**
 * A server systemd does not run: a container, WSL without it, or a host
 * started by another init. The same is read where the dashboard's own
 * container was started without the host's `/run/systemd`, which is the one
 * case with a fix, so the mount is the line to copy.
 */
export function noSystemd(reader: SystemdReader, raw?: string): Missing {
  return {
    thing: "systemd",
    reason: "no-systemd",
    title: "No systemd on this server",
    sentence: `${SYSTEMD_READS[reader]}, and nothing answers at ${SYSTEMD_RUN_DIR}: this is a container, or a host started by another init. Where the dashboard runs in a container of its own, that folder has to be mounted into it.`,
    steps: [
      { kind: "link", label: "Open Processes", href: "/processes" },
      { kind: "copy", label: "Copy the mount", text: SYSTEMD_MOUNT },
    ],
    raw: raw?.trim() || undefined,
  }
}

/**
 * A part of the dashboard switched off in Configuration. `canConfigure` is
 * whether this account may change it: anybody else is told who can, and is
 * not sent to a page that would refuse them.
 */
export function moduleOff(
  module: { name: string; thing?: string; setting?: string },
  canConfigure: boolean,
): Missing {
  const where = module.setting ? `${module.setting} in Configuration` : "Configuration"
  return {
    thing: module.thing,
    reason: "module-off",
    title: `${module.name} is turned off`,
    sentence: canConfigure
      ? `It is switched off under ${where}, so nothing here is read or run.`
      : `It is switched off under ${where}. An administrator can turn it on.`,
    steps: canConfigure
      ? [{ kind: "link", label: "Open Configuration", href: "/dashboard/configuration" }]
      : [],
  }
}

/** The parts of an API refusal this reads: `ApiError` has them all. */
export type Refusal = {
  code?: string
  resource?: string
  message?: string
  reason?: string
  raw?: string
}

/**
 * The absence behind a failed read, where the answer names one: a page that
 * asked Docker or systemd for something and was told the thing is not there
 * draws that, with the way to the page that fixes it, instead of a box
 * reading "Not available on this host". Anything else is not an absence and
 * stays the failure it is (`undefined`).
 */
export function missingFromError(
  error: unknown,
  context: { systemd?: SystemdReader; canConfigure?: boolean } = {},
): Missing | undefined {
  if (!error || typeof error !== "object") return undefined
  const refusal = error as Refusal
  const raw = refusal.raw?.trim() || undefined
  switch (refusal.code) {
    case "docker_unavailable":
      return {
        thing: "docker",
        reason: "docker-silent",
        title: "Docker is not answering",
        sentence:
          "This is read from Docker, and its daemon did not answer the dashboard. The Docker page says which fault it is and starts the daemon where that is the fix.",
        steps: [{ kind: "link", label: "Open Docker", href: "/docker" }],
        raw,
      }
    case "systemd_not_running":
      return noSystemd(context.systemd ?? "services", raw)
    case "not_installed":
      // `httpx.Absent` names what is missing; a `systemctl` that is not on
      // the path at all says so in its message.
      return refusal.resource === "systemd" || /\bsystemctl\b/.test(refusal.message ?? "")
        ? noSystemd(context.systemd ?? "services", raw)
        : undefined
    case "terminal_disabled":
      return moduleOff(
        { name: "The web terminal", setting: "Features" },
        context.canConfigure ?? false,
      )
    default:
      return undefined
  }
}

/** The Docker kinds a reading draws, in the order a sentence lists them. */
const DOCKER_KINDS = ["deployments", "containers", "stacks", "volumes", "images", "networks"]

/**
 * One line for several reads that failed for one reason: "Docker is not
 * answering: containers, stacks and volumes not drawn". `kinds` are the
 * plural names of what went unread, `verb` what the page would have done
 * with them.
 */
export function unreadLine(subject: string, kinds: string[], verb = "drawn"): string {
  const unique = [...new Set(kinds.map((kind) => kind.toLowerCase()))].sort((a, b) => {
    const [x, y] = [DOCKER_KINDS.indexOf(a), DOCKER_KINDS.indexOf(b)]
    return (x < 0 ? DOCKER_KINDS.length : x) - (y < 0 ? DOCKER_KINDS.length : y)
  })
  if (unique.length === 0) return subject
  const list =
    unique.length === 1
      ? unique[0]
      : `${unique.slice(0, -1).join(", ")} and ${unique[unique.length - 1]}`
  return `${subject}: ${list} not ${verb}`
}

/**
 * Whether a read's own words are a daemon that could not be reached, for the
 * pages that are handed a sentence rather than an error code (the log
 * sources' `detail`, the map's `unread`).
 */
export function saysDockerUnreachable(text: string | undefined): boolean {
  const lower = (text ?? "").toLowerCase()
  return (
    lower.includes("cannot connect to the docker daemon") ||
    lower.includes("docker daemon is not reachable") ||
    lower.includes("docker is not reachable") ||
    lower.includes("docker is not available") ||
    lower.includes("docker.sock")
  )
}

/** One line for a place that is already inside something (`MissingLine`). */
export type MissingLineWords = {
  thing?: string
  sentence: string
  href?: string
  /** The link's words. */
  label?: string
}

/**
 * Why a kind of log source is absent, as one sentence and the page that says
 * more. The source index hands the reason over as whatever the subsystem
 * said, which for Docker is the client's own question ("Is the docker daemon
 * running?"); the rail printed it whole.
 */
export function logKindMissing(kind: string, said: string | undefined): MissingLineWords {
  const lower = (said ?? "").toLowerCase()
  switch (kind) {
    case "docker":
      return {
        thing: "docker",
        sentence: saysDockerUnreachable(said) ? "Docker is not running" : "Docker did not answer",
        href: "/docker",
        label: "Open Docker",
      }
    case "pm2":
      return {
        thing: "pm2",
        sentence: lower.includes("not installed")
          ? "PM2 is not installed"
          : lower.includes("no managed processes")
            ? "PM2 runs no applications"
            : "PM2 did not answer",
        href: "/processes/pm2",
        label: "Open PM2",
      }
    case "journal":
      return {
        thing: "systemd",
        sentence: "No systemd on this server",
        href: "/processes/services",
        label: "Open Services",
      }
    default: {
      const text = (said ?? "").trim()
      return {
        sentence: text ? text.charAt(0).toUpperCase() + text.slice(1) : "Not on this server",
      }
    }
  }
}
