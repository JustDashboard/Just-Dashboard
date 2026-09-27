import type { LogSourceIndex } from "@/lib/types"
import type { ServiceLogSource } from "@/components/logs/service-logs"

/**
 * The files the Log view offers, in the order a reader wants them: apt's
 * transactions with the command that ran them first, then dpkg's per-package
 * record, then what ran unattended overnight, then what apt printed — and the
 * RPM world's two for a dnf host. Anything else the server reads as a package
 * log (the unattended run's own dpkg output) follows under its own name.
 */
const PACKAGE_LOGS = [
  "/var/log/apt/history.log",
  "/var/log/dpkg.log",
  "/var/log/unattended-upgrades/unattended-upgrades.log",
  "/var/log/apt/term.log",
  "/var/log/dnf.log",
  "/var/log/dnf.rpm.log",
]

/** The host's package logs as the view's sources, each drawn as the distribution's mark. */
export function packageLogSources(index: LogSourceIndex, product?: string): ServiceLogSource[] {
  const known = PACKAGE_LOGS.flatMap((path) => index.sources.filter((s) => s.path === path))
  const rest = index.sources.filter(
    (s) => s.lens === "packages" && s.path && !PACKAGE_LOGS.includes(s.path),
  )
  return [...known, ...rest].map((source) => ({ ...source, lens: "packages", product }))
}
