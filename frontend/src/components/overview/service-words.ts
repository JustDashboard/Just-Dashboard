import { plural } from "@/lib/format"

/*
 * What each module's row on the Overview says: one sentence a person would
 * say ("9 containers running, 2 stopped") and the state beside it. They were
 * a figure, a unit and a caption on a tile, which the reader put together.
 * Kept free of React so `bun test` reads them.
 */

export type ServiceTone = "running" | "stopped" | "warning" | "danger" | "notice"

export type ServiceWords = {
  sentence: string
  state: { tone: ServiceTone; label: string }
}

export function dockerWords(total: number, running: number): ServiceWords {
  const stopped = total - running
  if (total === 0)
    return { sentence: "No containers yet", state: { tone: "stopped", label: "Idle" } }
  if (running === 0)
    return {
      sentence: `${plural(total, "container")}, none running`,
      state: { tone: "stopped", label: "Stopped" },
    }
  return {
    sentence: `${plural(running, "container")} running${stopped > 0 ? `, ${stopped} stopped` : ""}`,
    state: { tone: "running", label: "Running" },
  }
}

/** `soonest` is the certificate with the fewest days left. */
export function certificateWords(
  total: number,
  soonest: { daysLeft: number; expired: boolean; expiring?: boolean } | undefined,
): ServiceWords {
  if (total === 0 || !soonest)
    return { sentence: "No certificates issued", state: { tone: "stopped", label: "None" } }
  const count = plural(total, "certificate")
  if (soonest.expired)
    return {
      sentence: `${count}, ${total === 1 ? "expired" : "one expired"}`,
      state: { tone: "danger", label: "Expired" },
    }
  const days = plural(Math.max(soonest.daysLeft, 0), "day")
  return {
    sentence:
      total === 1 ? `1 certificate, renews in ${days}` : `${count}, the next renews in ${days}`,
    // One that should have renewed by now and has not is the proxy's to chase.
    state: soonest.expiring
      ? { tone: "warning", label: "Expiring" }
      : { tone: "running", label: "Valid" },
  }
}

export function packageWords(report: {
  packages: unknown[]
  securityCount: number
  securityFiltering: boolean
  rebootRequired: boolean
}): ServiceWords {
  const pending = report.packages.length
  const waiting =
    pending === 0
      ? "Nothing to update"
      : `${plural(pending, "update")} waiting${
          report.securityCount > 0
            ? `, ${report.securityCount} for security`
            : report.securityFiltering
              ? ", none for security"
              : ""
        }`
  if (report.rebootRequired)
    return {
      sentence: `${waiting}, restart needed`,
      state: { tone: "warning", label: "Restart needed" },
    }
  if (report.securityCount > 0)
    return { sentence: waiting, state: { tone: "warning", label: "Security updates" } }
  if (pending > 0) return { sentence: waiting, state: { tone: "notice", label: "Updates" } }
  return { sentence: waiting, state: { tone: "running", label: "Up to date" } }
}

export function unitWords(counts: { running: number; failed: number }): ServiceWords {
  const running = `${plural(counts.running, "service")} running`
  if (counts.failed > 0)
    return {
      sentence: `${counts.failed} failed, ${running}`,
      state: { tone: "danger", label: "Failed" },
    }
  return { sentence: `${running}, none failed`, state: { tone: "running", label: "Running" } }
}

export function gitWords(
  repos: readonly { dirty: boolean; behind: number; conflicts: number }[],
): ServiceWords {
  if (repos.length === 0)
    return { sentence: "No repositories yet", state: { tone: "stopped", label: "None" } }
  const dirty = repos.filter((repo) => repo.dirty).length
  const behind = repos.filter((repo) => repo.behind > 0).length
  const conflicted = repos.filter((repo) => repo.conflicts > 0).length
  const count = plural(repos.length, "repository", "repositories")
  const sentence = [
    count,
    conflicted > 0 && `${conflicted} in conflict`,
    dirty > 0 ? `${dirty} uncommitted` : "all committed",
    behind > 0 && `${behind} behind`,
  ]
    .filter(Boolean)
    .join(", ")
  if (conflicted > 0) return { sentence, state: { tone: "warning", label: "In conflict" } }
  if (dirty > 0) return { sentence, state: { tone: "notice", label: "Uncommitted" } }
  if (behind > 0) return { sentence, state: { tone: "notice", label: "Behind" } }
  return { sentence, state: { tone: "running", label: "Clean" } }
}

export function remoteWords(computers: number, open: number, engineRunning: boolean): ServiceWords {
  if (computers === 0)
    return { sentence: "No computers yet", state: { tone: "stopped", label: "None" } }
  const count = plural(computers, "computer")
  if (open > 0)
    return { sentence: `${count}, ${open} open now`, state: { tone: "running", label: "Open" } }
  if (engineRunning)
    return { sentence: `${count}, ready to connect`, state: { tone: "running", label: "Ready" } }
  return {
    sentence: `${count}, the engine is not running`,
    state: { tone: "stopped", label: "Stopped" },
  }
}

export function versionWords(version: string, behind: number, breaking: boolean): ServiceWords {
  if (behind === 0)
    return {
      sentence: `Version ${version}, the newest`,
      state: { tone: "running", label: "Up to date" },
    }
  return {
    sentence: `Version ${version}, ${plural(behind, "release")} behind${breaking ? " with a breaking change" : ""}`,
    state: { tone: breaking ? "warning" : "notice", label: "Update" },
  }
}
