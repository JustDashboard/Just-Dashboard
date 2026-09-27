import type { LogSourceIndex } from "@/lib/types"
import type { ServiceLogSource } from "@/components/logs/service-logs"
import { journalIdSource, journalSource, kernelSource } from "@/lib/log-sources"

/**
 * Which log each Security page reads, out of what this host has.
 *
 * The same lines live in different places on different hosts: sshd writes
 * to auth.log on Debian, to secure on the RPM family and only to the
 * journal on an image with no syslog daemon; ufw's drops go to ufw.log,
 * then kern.log, then only the kernel ring. Each page asks for the file an
 * operator would open first, as `/logs/sources` lists it — so a file outside
 * `JD_LOG_ROOTS`, or auth data a non-admin may not read, is never offered —
 * and falls back to the journal's reading of the same program.
 *
 * `undefined` is "not known yet"; `null` is "this host has none of them",
 * which a page says in words rather than drawing an empty pane.
 */

/** The programs whose lines are the SSH page's: the daemon, the privilege changes, the seats. */
export const SSH_IDENTS = ["sshd", "sshd-session", "sshd-auth", "sudo", "su", "systemd-logind"]

function listedFile(index: LogSourceIndex, paths: string[]): ServiceLogSource | undefined {
  for (const path of paths) {
    const found = index.sources.find((source) => source.path === path)
    if (found) return found
  }
  return undefined
}

/** The journal is listed whenever systemd runs; without it there is no fallback. */
function hasJournal(index: LogSourceIndex) {
  return index.sources.some((source) => source.kind === "journal")
}

export function authLogSource(
  index: LogSourceIndex | undefined,
): ServiceLogSource | null | undefined {
  if (!index) return undefined
  const file = listedFile(index, ["/var/log/auth.log", "/var/log/secure"])
  if (file) return { ...file, lens: "auth" }
  if (!hasJournal(index)) return null
  return {
    id: journalIdSource(SSH_IDENTS),
    label: "sshd, sudo and logind",
    kind: "journal-id",
    lens: "auth",
    detail: "What they wrote to the journal — this host keeps no auth.log",
  }
}

/**
 * The firewall's own file, else the kernel's, else its ring. Always read as
 * the firewall: kern.log is mostly other things, and the page's questions —
 * what was blocked, from where, on which port — are the firewall lens's.
 */
export function firewallLogSource(
  index: LogSourceIndex | undefined,
): ServiceLogSource | null | undefined {
  if (!index) return undefined
  const file = listedFile(index, ["/var/log/ufw.log", "/var/log/kern.log"])
  if (file) return { ...file, lens: "firewall" }
  if (!hasJournal(index)) return null
  return {
    id: kernelSource(),
    label: "Kernel ring",
    kind: "kernel",
    lens: "firewall",
    detail: "The firewall's drops as the journal keeps them",
  }
}

/**
 * fail2ban's log, else its unit's journal — which is where it writes when its
 * `logtarget` is the journal, and where the old page said there was nothing
 * to read.
 */
export function fail2banLogSource(
  index: LogSourceIndex | undefined,
): ServiceLogSource | null | undefined {
  if (!index) return undefined
  const file = listedFile(index, ["/var/log/fail2ban.log"])
  if (file) return { ...file, lens: "fail2ban" }
  if (!hasJournal(index)) return null
  return {
    id: journalSource("fail2ban.service"),
    label: "fail2ban.service",
    kind: "journal",
    lens: "fail2ban",
    detail: "What fail2ban wrote to the journal",
  }
}
