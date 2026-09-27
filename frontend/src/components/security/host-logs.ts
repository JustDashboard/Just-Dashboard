import type { ServiceLogSource } from "@/components/logs/service-logs"
import { fileSource, journalIdSource, journalSource, kernelSource } from "@/lib/log-sources"

/**
 * Which log each Security page reads, out of what this host has.
 *
 * The same lines live in different places on different hosts: sshd writes
 * to auth.log on Debian, to secure on the RPM family and only to the
 * journal on an image with no syslog daemon; ufw's drops go to ufw.log,
 * then kern.log, then only the kernel ring. Each page asks after the files
 * an operator would open first, one by one (`GET /logs/source`, a stat on
 * the server) — not out of `/logs/sources`, which walks the log roots and
 * asks Docker, PM2 and systemd about everything else on the host, so a page
 * that wanted two files waited on all of it — and falls back to the
 * journal's reading of the same program, saying why in the pane's facts.
 */

/**
 * One file a page would open first. `lens` is named only where the page
 * reads the file otherwise than the server would — kern.log as the
 * firewall's; everywhere else the server's own description stands.
 */
export type HostLogFile = { path: string; lens?: string }

export type HostLogPlan = {
  files: HostLogFile[]
  /** The journal's reading of the same program, and what it holds, in the pane's words. */
  journal: Omit<ServiceLogSource, "detail"> & { holds: string }
  /** Why the journal is read, on a host that keeps none of the files. */
  none: string
}

/**
 * What the server said of one file: described, not there, or outside
 * `JD_LOG_ROOTS` — which it answers for a path whether or not the file
 * exists, so "outside" is said of the path and never "it is there".
 */
export type HostLogProbe =
  { path: string; source: ServiceLogSource } | { path: string; refused: "missing" | "outside" }

/** The programs whose lines are the SSH page's: the daemon, the privilege changes, the seats. */
export const SSH_IDENTS = ["sshd", "sshd-session", "sshd-auth", "sudo", "su", "systemd-logind"]

export const AUTH_LOG: HostLogPlan = {
  files: [{ path: "/var/log/auth.log" }, { path: "/var/log/secure" }],
  journal: {
    id: journalIdSource(SSH_IDENTS),
    label: "sshd, sudo and logind",
    kind: "journal-id",
    holds: "sshd's and sudo's lines as the journal has them",
  },
  none: "This host keeps no auth.log",
}

/**
 * The firewall's own file, else the kernel's, else its ring. Always read as
 * the firewall: kern.log is mostly other things, and the page's questions —
 * what was blocked, from where, on which port — are the firewall lens's.
 */
export const FIREWALL_LOG: HostLogPlan = {
  files: [{ path: "/var/log/ufw.log" }, { path: "/var/log/kern.log", lens: "firewall" }],
  journal: {
    id: kernelSource(),
    label: "Kernel ring",
    kind: "kernel",
    lens: "firewall",
    holds: "the drops as the journal has them",
  },
  none: "This host keeps no ufw.log or kern.log",
}

/**
 * fail2ban's log, else its unit's journal — which is where it writes when its
 * `logtarget` is the journal, and where the old page said there was nothing
 * to read.
 */
export const FAIL2BAN_LOG: HostLogPlan = {
  files: [{ path: "/var/log/fail2ban.log" }],
  journal: {
    id: journalSource("fail2ban.service"),
    label: "fail2ban.service",
    kind: "journal",
    holds: "what fail2ban wrote to the journal",
  },
  none: "This host keeps no fail2ban.log",
}

/**
 * The first of the plan's files the server described, else the journal and
 * the reason for it. A file read as the server reads it is its description,
 * which the pane need not ask for again; one the page reads through another
 * lens is asked about by the pane, so "Read as" still knows what the server
 * would have detected.
 */
export function hostLogSource(plan: HostLogPlan, probes: HostLogProbe[]): ServiceLogSource {
  for (const file of plan.files) {
    const probe = probes.find((p) => p.path === file.path)
    if (probe && "source" in probe) {
      const id = fileSource(file.path)
      return file.lens
        ? { ...probe.source, id, lens: file.lens }
        : { ...probe.source, id, described: true }
    }
  }
  const outside = probes.find((p) => "refused" in p && p.refused === "outside")
  const { holds, ...journal } = plan.journal
  const why = outside
    ? `${outside.path.slice(outside.path.lastIndexOf("/") + 1)} is outside JD_LOG_ROOTS`
    : plan.none
  return { ...journal, detail: `${why} — ${holds}` }
}
