import type { DotTone } from "@/components/status-dot"

/**
 * What the Console asks the exec socket for. The backend's handler
 * (`handleContainerExec`) reads `cmd` — split on whitespace and run as given —
 * and `user`, which goes to Docker untouched. With no `cmd` it picks bash and
 * then sh itself, so "auto" is the absence of the parameter, not a value.
 */
export const SHELLS = [
  { id: "auto", label: "Auto", cmd: undefined },
  { id: "sh", label: "sh", cmd: "/bin/sh" },
  { id: "bash", label: "bash", cmd: "/bin/bash" },
  { id: "ash", label: "ash", cmd: "/bin/ash" },
] as const

export type ConsoleShell = (typeof SHELLS)[number]["id"]

/**
 * Only root is offered beside the image's own user. The handler does not check
 * `user` against anything and its audit line records neither parameter, so a
 * free-text user would be an unrecorded choice of identity inside the
 * container; "root" is the one name that needs no lookup to mean what it says.
 */
export const RUN_AS = [
  { id: "default", label: "Image user" },
  { id: "root", label: "root" },
] as const

export type ConsoleRunAs = (typeof RUN_AS)[number]["id"]

export type ConsoleChoice = { shell: ConsoleShell; runAs: ConsoleRunAs }

export const DEFAULT_CHOICE: ConsoleChoice = { shell: "auto", runAs: "default" }

/** A remembered value from an older build, or a hand-edited one, is the default. */
export function readShell(value: unknown): ConsoleShell {
  return SHELLS.find((shell) => shell.id === value)?.id ?? "auto"
}

export function readRunAs(value: unknown): ConsoleRunAs {
  return RUN_AS.find((one) => one.id === value)?.id ?? "default"
}

export function readChoice(value: Partial<ConsoleChoice> | undefined): ConsoleChoice {
  return { shell: readShell(value?.shell), runAs: readRunAs(value?.runAs) }
}

/**
 * The socket's query. `cmd` and `user` appear only once chosen, so the default
 * session is the same request the console has always made.
 */
export function execQuery(choice: ConsoleChoice, rows = 30, cols = 100) {
  const query: Record<string, string | number> = { rows, cols }
  const cmd = SHELLS.find((shell) => shell.id === choice.shell)?.cmd
  if (cmd) query.cmd = cmd
  if (choice.runAs === "root") query.user = "root"
  return query
}

/**
 * Whether the shell is root: asked for by name, or inherited from an image
 * that declares no user (Docker's `Config.User` is empty then) or declares
 * root by name or number. Unknown until the container's detail has arrived,
 * and unknown is not claimed as either.
 */
export function runsAsRoot(runAs: ConsoleRunAs, imageUser: string | undefined) {
  if (runAs === "root") return true
  if (imageUser === undefined) return false
  const name = imageUser.split(":")[0].trim()
  return name === "" || name === "root" || name === "0"
}

/** Docker's health word as a status reading; none where there is no health check. */
export function healthReading(health: string | undefined): { tone: DotTone; label: string } | null {
  switch (health) {
    case "healthy":
      return { tone: "running", label: "Healthy" }
    case "unhealthy":
      return { tone: "danger", label: "Unhealthy" }
    case "starting":
      return { tone: "warning", label: "Starting" }
    default:
      return null
  }
}

export function restartsLabel(count: number) {
  return `${count} ${count === 1 ? "restart" : "restarts"}`
}
