import { expect, test } from "bun:test"
import {
  ATTACKING_NOW_MS,
  accountsOf,
  attackingNow,
  attemptsWords,
  drawnFirst,
  heldFrom,
  idleWords,
  originsOf,
  refusedBy,
  signedInWords,
  windowWords,
} from "./logins-reading"

const session = (user, tty, from, loginTime) => ({ user, tty, from, loginTime, isSsh: !!from })
const login = (user, loginTime, over = {}) => ({
  kind: "login",
  user,
  tty: "pts/0",
  from: "198.51.100.7",
  loginTime,
  active: false,
  ...over,
})

test("sessions fold by where they are held from, the console among them", () => {
  expect(
    originsOf([
      session("ubuntu", "pts/0", "100.110.34.9", "2026-11-13T10:00:00Z"),
      session("deploy", "pts/1", "100.110.34.9", "2026-11-13T09:00:00Z"),
      session("ubuntu", "pts/2", "100.110.34.9", "2026-11-13T11:00:00Z"),
      session("root", "tty1", "", "2026-11-13T08:00:00Z"),
    ]),
  ).toEqual([
    {
      address: "100.110.34.9",
      users: ["ubuntu", "deploy"],
      sessions: 3,
      since: "2026-11-13T09:00:00Z",
    },
    { address: "", users: ["root"], sessions: 1, since: "2026-11-13T08:00:00Z" },
  ])
  expect(originsOf([])).toEqual([])
})

test("accounts are those in now, then those the record names, most recent first", () => {
  const accounts = accountsOf(
    [session("ubuntu", "pts/0", "100.110.34.9"), session("ubuntu", "pts/1", "")],
    [
      login("ubuntu", "2026-11-13T10:00:00Z", { active: true }),
      login("mira", "2026-11-10T10:00:00Z"),
      login("deploy", "2026-11-12T10:00:00Z"),
      login("deploy", "2026-11-11T10:00:00Z"),
      { kind: "boot", user: "reboot", tty: "", from: "6.14.0", loginTime: "2026-11-12T12:00:00Z" },
      login("", "2026-11-12T13:00:00Z"),
    ],
  )
  expect(accounts.map((account) => account.user)).toEqual(["ubuntu", "deploy", "mira"])
  expect(accounts[0]).toEqual({
    user: "ubuntu",
    open: 2,
    origins: ["100.110.34.9", ""],
    terminals: ["pts/0", "pts/1"],
    last: "2026-11-13T10:00:00Z",
  })
  expect(accounts[1]).toMatchObject({ open: 0, last: "2026-11-12T10:00:00Z" })
})

test("an origin and an account light each other only while a session joins them", () => {
  const [ubuntu, mira] = accountsOf(
    [session("ubuntu", "pts/0", "100.110.34.9")],
    [login("mira", "2026-11-10T10:00:00Z", { from: "100.110.34.9" })],
  )
  expect(heldFrom(ubuntu, "100.110.34.9")).toBe(true)
  expect(heldFrom(ubuntu, "203.0.113.9")).toBe(false)
  expect(heldFrom(mira, "100.110.34.9")).toBe(false)
})

test("the line's words", () => {
  expect(signedInWords(0)).toBe("Nobody signed in")
  expect(signedInWords(1)).toBe("1 signed in")
  expect(signedInWords(1200)).toBe("1,200 signed in")
  expect(windowWords(168)).toBe("7 days")
  expect(windowWords(24)).toBe("24 hours")
  expect(windowWords(1)).toBe("hour")
  expect(attemptsWords({ attempts: 2312, capped: false })).toBe("2,312")
  expect(attemptsWords({ attempts: 5000, capped: true })).toBe("at least 5,000")
  expect(idleWords("active")).toBe("active now")
  expect(idleWords("00:12")).toBe("idle 00:12")
  expect(idleWords(undefined)).toBe("idle time unknown")
})

test("an attempt inside the last quarter of an hour is one in flight", () => {
  const now = Date.parse("2026-11-13T12:00:00Z")
  expect(attackingNow(new Date(now - 60_000).toISOString(), now)).toBe(true)
  expect(attackingNow(new Date(now - ATTACKING_NOW_MS - 1).toISOString(), now)).toBe(false)
  expect(attackingNow(undefined, now)).toBe(false)
  expect(attackingNow("not a date", now)).toBe(false)
})

test("an address is refused by a deny for it or for a network it sits in", () => {
  expect(refusedBy("203.0.113.9", ["203.0.113.9"])).toBe(true)
  expect(refusedBy("203.0.113.9", ["203.0.113.0/24"])).toBe(true)
  expect(refusedBy("203.0.113.9", ["198.51.100.0/24", "203.0.113.10"])).toBe(false)
  expect(refusedBy("2001:db8::9", ["2001:db8::/32"])).toBe(true)
  expect(refusedBy("2001:db8::9", ["203.0.113.0/24"])).toBe(false)
  // A rule written against a name is not an address and holds nothing.
  expect(refusedBy("203.0.113.9", ["office-vpn"])).toBe(false)
  expect(refusedBy("203.0.113.9", [])).toBe(false)
})

test("the picture's rows are shared between who is in and who is trying", () => {
  // Everything fits: everything is drawn.
  expect(drawnFirst(1, 3, 4)).toEqual({ origins: 1, attackers: 3 })
  expect(drawnFirst(0, 0, 4)).toEqual({ origins: 0, attackers: 0 })
  // One row too many: three are drawn and the fourth row counts the rest.
  expect(drawnFirst(3, 3, 4)).toEqual({ origins: 2, attackers: 1 })
  expect(drawnFirst(1, 9, 4)).toEqual({ origins: 1, attackers: 2 })
  expect(drawnFirst(0, 9, 4)).toEqual({ origins: 0, attackers: 3 })
  expect(drawnFirst(6, 0, 4)).toEqual({ origins: 3, attackers: 0 })
  expect(drawnFirst(6, 1, 4)).toEqual({ origins: 2, attackers: 1 })
})
