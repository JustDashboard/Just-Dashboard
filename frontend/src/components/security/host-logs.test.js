import { expect, test } from "bun:test"
import { SSH_IDENTS, authLogSource, fail2banLogSource, firewallLogSource } from "./host-logs"

const file = (path, lens) => ({
  id: `file:${path}`,
  label: path.slice(path.lastIndexOf("/") + 1),
  kind: "system",
  path,
  lens,
  rotated: false,
})

const journal = { id: "journal:", label: "systemd journal", kind: "journal", rotated: false }

const index = (...sources) => ({ sources, units: [], roots: ["/var/log"], missing: {} })

test("nothing is chosen before the host's logs are listed", () => {
  expect(authLogSource(undefined)).toBeUndefined()
  expect(firewallLogSource(undefined)).toBeUndefined()
  expect(fail2banLogSource(undefined)).toBeUndefined()
})

test("the SSH page reads auth.log, then secure, then sshd and sudo through the journal", () => {
  const both = index(file("/var/log/secure", "auth"), file("/var/log/auth.log", "auth"), journal)
  expect(authLogSource(both)?.id).toBe("file:/var/log/auth.log")
  expect(authLogSource(index(file("/var/log/secure", "auth"), journal))?.id).toBe(
    "file:/var/log/secure",
  )
  const fallback = authLogSource(index(journal))
  expect(fallback?.id).toBe(`journal-id:${SSH_IDENTS.join(",")}`)
  expect(fallback?.kind).toBe("journal-id")
  expect(fallback?.lens).toBe("auth")
  // Every ident is one the server gates as auth data, so a reader who may
  // not have it is refused it rather than handed half of it.
  expect(SSH_IDENTS).toEqual(["sshd", "sshd-session", "sshd-auth", "sudo", "su", "systemd-logind"])
})

test("a host with neither the files nor a journal has no log to offer", () => {
  expect(authLogSource(index(file("/var/log/syslog", "syslog")))).toBeNull()
  expect(firewallLogSource(index())).toBeNull()
  expect(fail2banLogSource(index())).toBeNull()
})

test("the firewall's lines are read as the firewall's wherever they are written", () => {
  const ufw = firewallLogSource(
    index(file("/var/log/kern.log", "kernel"), file("/var/log/ufw.log", "firewall"), journal),
  )
  expect(ufw?.id).toBe("file:/var/log/ufw.log")
  // kern.log is the kernel's, and the ring is too; the page asks the
  // firewall's questions of both.
  const kern = firewallLogSource(index(file("/var/log/kern.log", "kernel"), journal))
  expect(kern?.id).toBe("file:/var/log/kern.log")
  expect(kern?.lens).toBe("firewall")
  const ring = firewallLogSource(index(journal))
  expect(ring?.id).toBe("kernel:")
  expect(ring?.lens).toBe("firewall")
})

test("fail2ban's log, else its unit's journal", () => {
  expect(fail2banLogSource(index(file("/var/log/fail2ban.log", "fail2ban"), journal))?.id).toBe(
    "file:/var/log/fail2ban.log",
  )
  const unit = fail2banLogSource(index(journal))
  expect(unit?.id).toBe("journal:fail2ban.service")
  expect(unit?.lens).toBe("fail2ban")
})

test("a file of the same name somewhere else is not the one the page means", () => {
  const elsewhere = index(file("/srv/app/logs/auth.log", "app"), journal)
  expect(authLogSource(elsewhere)?.kind).toBe("journal-id")
})
