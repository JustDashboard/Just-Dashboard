import { expect, test } from "bun:test"
import { AUTH_LOG, FAIL2BAN_LOG, FIREWALL_LOG, SSH_IDENTS, hostLogSource } from "./host-logs"

/** The server's description of a file it may read, under the bare path it lists files by. */
const found = (path, lens) => ({
  path,
  source: {
    id: path,
    label: path.slice(path.lastIndexOf("/") + 1),
    kind: "system",
    path,
    size: 1024,
    lens,
    rotated: false,
  },
})
const missing = (path) => ({ path, refused: "missing" })
const outside = (path) => ({ path, refused: "outside" })

test("the SSH page reads auth.log, then secure, then sshd and sudo through the journal", () => {
  const both = hostLogSource(AUTH_LOG, [
    found("/var/log/auth.log", "auth"),
    found("/var/log/secure", "auth"),
  ])
  expect(both.id).toBe("file:/var/log/auth.log")
  const secure = hostLogSource(AUTH_LOG, [
    missing("/var/log/auth.log"),
    found("/var/log/secure", "auth"),
  ])
  expect(secure.id).toBe("file:/var/log/secure")
  expect(secure.size).toBe(1024)

  const journal = hostLogSource(AUTH_LOG, [
    missing("/var/log/auth.log"),
    missing("/var/log/secure"),
  ])
  expect(journal.id).toBe(`journal-id:${SSH_IDENTS.join(",")}`)
  expect(journal.kind).toBe("journal-id")
  expect(journal.detail).toStartWith("This host keeps no auth.log")
  // Every ident is one the server gates as auth data, so a reader who may
  // not have it is refused it rather than handed half of it.
  expect(SSH_IDENTS).toEqual(["sshd", "sshd-session", "sshd-auth", "sudo", "su", "systemd-logind"])
})

test("a file outside the log roots is named as the reason, not called missing", () => {
  const source = hostLogSource(AUTH_LOG, [outside("/var/log/auth.log"), missing("/var/log/secure")])
  expect(source.kind).toBe("journal-id")
  expect(source.detail).toStartWith("auth.log is outside JD_LOG_ROOTS")
  expect(source.detail).not.toContain("keeps no")
})

test("a file read as the server reads it is its description, not asked for again", () => {
  const auth = hostLogSource(AUTH_LOG, [found("/var/log/auth.log", "auth")])
  expect(auth.lens).toBe("auth")
  expect(auth.described).toBe(true)
  // The journal was never asked after, so the pane asks.
  expect(hostLogSource(AUTH_LOG, []).described).toBeUndefined()
  expect(hostLogSource(AUTH_LOG, []).lens).toBeUndefined()
  expect(hostLogSource(FAIL2BAN_LOG, []).lens).toBeUndefined()
})

test("the firewall's lines are read as the firewall's wherever they are written", () => {
  const ufw = hostLogSource(FIREWALL_LOG, [
    found("/var/log/ufw.log", "firewall"),
    found("/var/log/kern.log", "kernel"),
  ])
  expect(ufw.id).toBe("file:/var/log/ufw.log")
  expect(ufw.lens).toBe("firewall")
  expect(ufw.described).toBe(true)
  // kern.log is the kernel's, and the ring is too; the page asks the
  // firewall's questions of both.
  const kern = hostLogSource(FIREWALL_LOG, [
    missing("/var/log/ufw.log"),
    found("/var/log/kern.log", "kernel"),
  ])
  expect(kern.id).toBe("file:/var/log/kern.log")
  expect(kern.lens).toBe("firewall")
  // Read otherwise than the server would: the pane asks what it detects.
  expect(kern.described).toBeUndefined()
  const ring = hostLogSource(FIREWALL_LOG, [
    missing("/var/log/ufw.log"),
    missing("/var/log/kern.log"),
  ])
  expect(ring.id).toBe("kernel:")
  expect(ring.lens).toBe("firewall")
  expect(ring.detail).toStartWith("This host keeps no ufw.log or kern.log")
})

test("fail2ban's log, else its unit's journal", () => {
  expect(hostLogSource(FAIL2BAN_LOG, [found("/var/log/fail2ban.log", "fail2ban")]).id).toBe(
    "file:/var/log/fail2ban.log",
  )
  const unit = hostLogSource(FAIL2BAN_LOG, [missing("/var/log/fail2ban.log")])
  expect(unit.id).toBe("journal:fail2ban.service")
  expect(unit.kind).toBe("journal")
})
