import { expect, test } from "bun:test"
import {
  DAY,
  HOUR,
  MINUTE,
  activeNow,
  byNetwork,
  inKeyScope,
  inUserScope,
  keyLine,
  lastSignIns,
  lifetimeAxis,
  peopleVerdict,
  placeOn,
  sessionGuard,
  sessionLine,
  usableKeys,
} from "./access"

const NOW = Date.parse("2026-10-14T12:00:00Z")
const ago = (ms) => new Date(NOW - ms).toISOString()
const ahead = (ms) => new Date(NOW + ms).toISOString()

const session = (over = {}) => ({
  id: "s",
  userId: 1,
  twoFactorPassed: true,
  authMethod: "totp",
  ip: "100.101.7.12",
  userAgent: "curl/8.5.0",
  createdAt: ago(3 * DAY),
  lastSeenAt: ago(10_000),
  expiresAt: ahead(27 * DAY),
  current: false,
  ...over,
})

const key = (over = {}) => ({
  id: 1,
  userId: 1,
  name: "github-actions",
  prefix: "vpsd_ab12",
  role: "limited",
  createdAt: ago(40 * DAY),
  revoked: false,
  ...over,
})

const user = (over = {}) => ({
  id: 1,
  username: "ion",
  displayName: "Ion",
  avatarVersion: 0,
  role: "admin",
  totpEnabled: true,
  disabled: false,
  mustChangePassword: false,
  lastLoginAt: ago(2 * HOUR),
  createdAt: ago(200 * DAY),
  ...over,
})

test("a session is open while it was seen inside two minutes", () => {
  expect(activeNow(session({ lastSeenAt: ago(90_000) }), NOW)).toBe(true)
  expect(activeNow(session({ lastSeenAt: ago(3 * MINUTE) }), NOW)).toBe(false)
})

test("a session's wire says what let it in", () => {
  expect(sessionGuard(session({ twoFactorPassed: false, authMethod: "password" }))).toBe("pending")
  expect(sessionGuard(session({ authMethod: "recovery" }))).toBe("recovery")
  expect(sessionGuard(session({ authMethod: undefined }))).toBe("unknown")

  expect(sessionLine(session(), NOW)).toEqual({ still: false, dashed: false, tone: "default" })
  expect(sessionLine(session({ authMethod: "password", lastSeenAt: ago(DAY) }), NOW)).toEqual({
    still: true,
    dashed: false,
    tone: "warning",
  })
  // Owing its code: dashed and amber, whatever else it is.
  expect(sessionLine(session({ twoFactorPassed: false }), NOW)).toMatchObject({
    dashed: true,
    tone: "warning",
  })
})

test("sessions group by the network they came in on, the tailnet first", () => {
  const groups = byNetwork([
    session({ id: "a", ip: "203.0.113.7" }),
    session({ id: "b", ip: "192.168.1.20" }),
    session({ id: "c", ip: "100.88.14.3" }),
    session({ id: "d", ip: "100.101.7.12" }),
  ])
  expect(groups.map((g) => g.kind)).toEqual(["tailscale", "local", "internet"])
  expect(groups[0].sessions.map((s) => s.id)).toEqual(["c", "d"])
  expect(groups[0].product).toBe("tailscale")
})

test("every session's life shares one axis, from the oldest sign-in to the last expiry", () => {
  const axis = lifetimeAxis(
    [
      session({ createdAt: ago(9 * DAY), expiresAt: ahead(21 * DAY) }),
      session({ createdAt: ago(3 * DAY), expiresAt: ahead(27 * DAY) }),
    ],
    NOW,
  )
  expect(axis).toEqual({ from: NOW - 9 * DAY, to: NOW + 27 * DAY })
  expect(placeOn(NOW, axis)).toBeCloseTo(25)
  expect(placeOn(NOW - 30 * DAY, axis)).toBe(0)
  // No sessions still gives "now" room either side.
  expect(lifetimeAxis([], NOW)).toEqual({ from: NOW - HOUR, to: NOW + HOUR })
})

test("a key's wire pulses when called today, is dashed when never called, amber near its end", () => {
  expect(keyLine(key({ lastUsedAt: ago(2 * HOUR) }), NOW)).toEqual({
    still: false,
    dashed: false,
    tone: "default",
  })
  expect(keyLine(key(), NOW)).toEqual({ still: true, dashed: true, tone: "default" })
  expect(keyLine(key({ lastUsedAt: ago(3 * DAY), expiresAt: ahead(5 * DAY) }), NOW)).toEqual({
    still: true,
    dashed: false,
    tone: "warning",
  })
})

test("only keys that still open a door are usable, and the chips narrow them", () => {
  const keys = [
    key({ id: 1, lastUsedAt: ago(2 * HOUR), expiresAt: ahead(5 * DAY) }),
    key({ id: 2 }),
    key({ id: 3, revoked: true }),
    key({ id: 4, expiresAt: ago(DAY) }),
    key({ id: 5, lastUsedAt: ago(10 * DAY), expiresAt: ahead(200 * DAY) }),
  ]
  const usable = usableKeys(keys, NOW)
  expect(usable.map((k) => k.id)).toEqual([1, 2, 5])
  const ids = (scope) => usable.filter((k) => inKeyScope(k, scope, NOW)).map((k) => k.id)
  expect(ids("all")).toEqual([1, 2, 5])
  expect(ids("week")).toEqual([1])
  expect(ids("unused")).toEqual([2])
  expect(ids("expiring")).toEqual([1])
})

test("the users verdict is amber only while an administrator is on a password alone", () => {
  expect(peopleVerdict([user()])).toEqual({ tone: "running", bare: 0, admins: 0 })
  expect(peopleVerdict([user(), user({ role: "limited", totpEnabled: false })])).toEqual({
    tone: "notice",
    bare: 1,
    admins: 0,
  })
  expect(
    peopleVerdict([user({ totpEnabled: false }), user({ disabled: true, totpEnabled: false })]),
  ).toEqual({ tone: "warning", bare: 1, admins: 1 })
})

test("the user chips narrow by role and by what guards an account", () => {
  const users = [
    user({ id: 1 }),
    user({ id: 2, role: "limited", totpEnabled: false, lastLoginAt: "0001-01-01T00:00:00Z" }),
    user({ id: 3, role: "readonly", totpEnabled: false, disabled: true }),
  ]
  const ids = (scope) => users.filter((u) => inUserScope(u, scope)).map((u) => u.id)
  expect(ids("all")).toEqual([1, 2, 3])
  expect(ids("admin")).toEqual([1])
  expect(ids("readonly")).toEqual([3])
  // A disabled account cannot be opened by its password at all.
  expect(ids("bare")).toEqual([2])
  expect(ids("never")).toEqual([2])
  expect(ids("disabled")).toEqual([3])
})

test("last sign-ins split by the window and count who never signed in", () => {
  const { within, older, never } = lastSignIns(
    [
      user({ id: 1, lastLoginAt: ago(2 * HOUR) }),
      user({ id: 2, lastLoginAt: ago(10 * MINUTE) }),
      user({ id: 3, lastLoginAt: ago(40 * DAY) }),
      user({ id: 4, lastLoginAt: "0001-01-01T00:00:00Z" }),
    ],
    NOW,
    7 * DAY,
  )
  expect(within.map((e) => e.user.id)).toEqual([2, 1])
  expect(older).toBe(1)
  expect(never).toBe(1)
})

test("required two-factor warns for every enabled unenrolled account and excludes disabled accounts", () => {
  const people = [
    user(),
    user({ role: "limited", totpEnabled: false }),
    user({ role: "admin", totpEnabled: false, disabled: true }),
  ]
  expect(peopleVerdict(people, true)).toEqual({ tone: "warning", bare: 1, admins: 0 })
  expect(peopleVerdict(people, false)).toEqual({ tone: "notice", bare: 1, admins: 0 })
  expect(peopleVerdict([user()], true)).toEqual({ tone: "running", bare: 0, admins: 0 })
})
