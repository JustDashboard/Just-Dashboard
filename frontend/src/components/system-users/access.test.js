import { describe, expect, test } from "bun:test"
import { isBare, keyShares, placeOnAxis, rootReach, rootRoutes, signIns, verdict } from "./access"

const user = (username, over = {}) => ({
  username,
  uid: 1000,
  gid: 1000,
  comment: "",
  home: `/home/${username}`,
  shell: "/bin/bash",
  groups: [username],
  system: false,
  locked: false,
  noPassword: false,
  sshKeyCount: 0,
  canLogin: true,
  ...over,
})

const NOW = Date.parse("2026-10-10T12:00:00Z")
const ago = (ms) => new Date(NOW - ms).toISOString()

describe("verdict", () => {
  test("counts the accounts that need no password and which of them are administrators", () => {
    const users = [
      user("a", { noPassword: true }),
      user("b", { noPassword: true, groups: ["sudo"] }),
    ]
    expect(verdict(users)).toEqual({ tone: "warning", count: 2, admins: 1, scope: "bare" })
  })

  test("an account that cannot sign in is not a warning, and a locked one is not bare", () => {
    const users = [
      user("daemon", { noPassword: true, canLogin: false }),
      user("held", { noPassword: true, locked: true }),
    ]
    expect(isBare(users[1])).toBe(false)
    expect(verdict(users)).toEqual({ tone: "running", count: 0, admins: 0 })
  })
})

describe("root reach", () => {
  test("docker is root by another name", () => {
    expect(rootRoutes({ uid: 1000, groups: ["docker", "adm"] })).toEqual([
      { label: "docker", kind: "docker" },
    ])
    expect(rootRoutes({ uid: 0, groups: ["root", "wheel"] }).map((r) => r.label)).toEqual([
      "uid 0",
      "wheel",
    ])
  })

  test("accounts that need no password lead and locked ones come last", () => {
    const users = [
      user("c", { groups: ["sudo"], locked: true }),
      user("b", { groups: ["sudo"] }),
      user("a", { groups: ["docker"], noPassword: true }),
      user("plain"),
    ]
    expect(rootReach(users).map((r) => r.user.username)).toEqual(["a", "b", "c"])
  })
})

describe("sign-ins", () => {
  const users = [
    user("recent", { lastLogin: ago(60_000) }),
    user("week", { lastLogin: ago(3 * 24 * 3600_000) }),
    user("old", { lastLogin: ago(90 * 24 * 3600_000) }),
    user("never"),
    user("daemon", { canLogin: false, system: true }),
  ]

  test("splits the window from what is earlier and counts only those who could have", () => {
    const day = signIns(users, NOW, 24 * 3600_000)
    expect(day.within.map((s) => s.user.username)).toEqual(["recent"])
    expect(day.older).toBe(2)
    expect(day.never).toBe(1)
    expect(day.latest.user.username).toBe("recent")
  })

  test("an unreadable timestamp is left out rather than placed at the epoch", () => {
    expect(signIns([user("x", { lastLogin: "yesterday" })], NOW, Infinity).latest).toBeUndefined()
  })

  test("the axis ends at now and clamps what falls outside it", () => {
    const day = 24 * 3600_000
    expect(placeOnAxis(NOW, NOW, day)).toBe(100)
    expect(placeOnAxis(NOW - day / 2, NOW, day)).toBe(50)
    expect(placeOnAxis(NOW - 2 * day, NOW, day)).toBe(0)
  })
})

test("keys are shared out most first, with the total they are shares of", () => {
  const { holders, total } = keyShares([
    user("a", { sshKeyCount: 1 }),
    user("b", { sshKeyCount: 3 }),
    user("c"),
  ])
  expect(holders.map((u) => u.username)).toEqual(["b", "a"])
  expect(total).toBe(4)
})
