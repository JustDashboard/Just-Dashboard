import type { ServiceLogSource } from "@/components/logs/service-logs"
import type { HostLogProbe } from "@/components/security/host-logs"

/**
 * The files the Log view offers, in the order a reader wants them: apt's
 * transactions with the command that ran them first, then dpkg's per-package
 * record, then what ran unattended overnight and the dpkg output of that
 * run, then what apt printed — and the RPM world's two for a dnf host. Each
 * is one the server reads as a package log without being told.
 */
export const PACKAGE_LOGS = [
  "/var/log/apt/history.log",
  "/var/log/dpkg.log",
  "/var/log/unattended-upgrades/unattended-upgrades.log",
  "/var/log/unattended-upgrades/unattended-upgrades-dpkg.log",
  "/var/log/apt/term.log",
  "/var/log/dnf.log",
  "/var/log/dnf.rpm.log",
]

/**
 * The package logs this host keeps, in the list's order, each drawn as the
 * distribution's mark. The lens is left for the server to detect, which it
 * does for every one of them: a lens the page names is the pane's "Read as"
 * set for the reader.
 */
export function packageLogSources(probes: HostLogProbe[], product?: string): ServiceLogSource[] {
  return probes.flatMap((probe) =>
    "source" in probe ? [{ ...probe.source, lens: undefined, product }] : [],
  )
}

/** Whether any of them is a path the server will not read: outside `JD_LOG_ROOTS`. */
export function packageLogsOutsideRoots(probes: HostLogProbe[]) {
  return probes.some((probe) => "refused" in probe && probe.refused === "outside")
}
