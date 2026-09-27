import type { ServiceLogSource } from "@/components/logs/service-logs"
import type { HostLogProbe } from "@/components/security/host-logs"
import { fileSource } from "@/lib/log-sources"

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
 * distribution's mark. Each is the server's own description of the file,
 * lens and all — it reads every one of them as a package log — so the pane
 * does not ask for it again.
 */
export function packageLogSources(probes: HostLogProbe[], product?: string): ServiceLogSource[] {
  return probes.flatMap((probe) =>
    "source" in probe
      ? [{ ...probe.source, id: fileSource(probe.path), product, described: true }]
      : [],
  )
}

/** Whether any of them is a path the server will not read: outside `JD_LOG_ROOTS`. */
export function packageLogsOutsideRoots(probes: HostLogProbe[]) {
  return probes.some((probe) => "refused" in probe && probe.refused === "outside")
}
