import { expect, test } from "bun:test"
import { sshReading } from "./ssh-reading"

const config = (settings, over = {}) => ({
  available: true,
  source: "sshd -T",
  settings: Object.entries(settings).map(([key, value]) => ({
    key,
    label: key,
    value,
    detail: "",
    kind: "choice",
  })),
  ports: ["22"],
  keyedAccounts: [],
  hasMatchBlocks: false,
  ...over,
})

test("the tile says the riskiest fact about how a login gets in", () => {
  expect(
    sshReading(config({ passwordauthentication: "no", permitrootlogin: "prohibit-password" })),
  ).toEqual({ value: "Keys only", tone: "default", hint: "port 22" })
  expect(sshReading(config({ passwordauthentication: "yes", permitrootlogin: "no" }))).toEqual({
    value: "Passwords allowed",
    tone: "warning",
    hint: "port 22",
  })
  expect(sshReading(config({ passwordauthentication: "yes", permitrootlogin: "yes" }))).toEqual({
    value: "Root can sign in",
    tone: "danger",
    hint: "port 22",
  })
})

test("root with a key only is not the red reading, and an absent directive is sshd's default", () => {
  // PermitRootLogin yes with passwords off lets root in by key alone.
  expect(sshReading(config({ passwordauthentication: "no", permitrootlogin: "yes" }))?.value).toBe(
    "Keys only",
  )
  // sshd's defaults: passwords on, root by key only.
  expect(sshReading(config({}))).toMatchObject({ value: "Passwords allowed", tone: "warning" })
})

test("the hint is where it listens, and a host without sshd says so", () => {
  expect(
    sshReading(config({ passwordauthentication: "no" }, { ports: ["22", "2222"] }))?.hint,
  ).toBe("ports 22, 2222")
  expect(sshReading(config({ passwordauthentication: "no" }, { ports: [] }))?.hint).toBe("port 22")
  expect(sshReading({ ...config({}), available: false })).toEqual({
    value: "No SSH server",
    tone: "default",
    hint: "neither sshd nor its configuration was found",
  })
  expect(sshReading(undefined)).toBeUndefined()
})
