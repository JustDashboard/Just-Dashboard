import { describe, expect, test } from "bun:test"
import {
  accountFindings,
  isPrivateGroup,
  readRestrictions,
  restrictionWords,
  rootSummary,
  signInSummary,
  splitKeyOptions,
  writeRestrictions,
} from "./access"

const closed = (why) => ({ allowed: false, why })
const open = { allowed: true }
const user = (fields) => ({
  username: "ion",
  uid: 1000,
  system: false,
  comment: "",
  groups: [],
  canLogin: true,
  disabled: false,
  noPassword: false,
  signIn: { password: closed("no_password"), key: closed("no_keys") },
  rootRoutes: [],
  sshKeyCount: 0,
  ...fields,
})

describe("signInSummary", () => {
  test("no password and no keys is no way in, and says why", () => {
    const summary = signInSummary(user({}))
    expect(summary.label).toBe("No way in")
    expect(summary.tone).toBe("warning")
    expect(summary.detail).toContain("no password")
    expect(summary.detail).toContain("no SSH keys")
  })

  test("a daemon's closed shell is not a warning", () => {
    const summary = signInSummary(user({ system: true, canLogin: false }))
    expect(summary.label).toBe("No login")
    expect(summary.tone).toBe("unknown")
  })

  test("disabled wins over routes that would otherwise be open", () => {
    expect(signInSummary(user({ disabled: true })).label).toBe("Disabled")
  })

  test("an empty password that SSH would take is the alarm", () => {
    const summary = signInSummary(
      user({ noPassword: true, signIn: { password: open, key: closed("no_keys") } }),
    )
    expect(summary.tone).toBe("danger")
  })
})

describe("rootSummary", () => {
  const sudo = (fields) => ({ kind: "sudo", usable: true, ...fields })

  test("the strongest sudo rule is the one named", () => {
    const summary = rootSummary({
      rootRoutes: [sudo({ usable: false }), sudo({ noPassword: true })],
    })
    expect(summary.label).toBe("No password asked")
    expect(summary.tone).toBe("danger")
  })

  test("a rule asking for a password it lacks reads as going nowhere", () => {
    const summary = rootSummary({ rootRoutes: [sudo({ usable: false })] })
    expect(summary.tone).toBe("muted")
    expect(summary.label).toBe("Needs a password it lacks")
  })

  test("docker and lxd come through as groups beside a sudo rule", () => {
    const summary = rootSummary({
      rootRoutes: [sudo({}), { kind: "group", via: "docker", usable: true }],
    })
    expect(summary.label).toBe("Asks for password")
    expect(summary.groups).toEqual(["docker"])
  })

  test("uid 0 is root whatever else is said", () => {
    expect(rootSummary({ rootRoutes: [{ kind: "uid0", usable: true }] }).label).toBe("Is root")
  })
})

describe("accountFindings", () => {
  const root = (signIn) =>
    user({ username: "root", uid: 0, system: true, signIn, rootRoutes: [{ kind: "uid0" }] })

  test("a clean host says nothing at all", () => {
    expect(
      accountFindings([
        root({ password: closed("root_keys_only"), key: open }),
        user({ signIn: { password: closed("no_password"), key: open } }),
      ]),
    ).toEqual([])
  })

  test("the host this page was rebuilt against", () => {
    const findings = accountFindings([
      root({ password: open, key: closed("no_keys") }),
      user({
        username: "ubuntu",
        rootRoutes: [
          {
            kind: "sudo",
            noPassword: true,
            usable: true,
            file: "/etc/sudoers.d/90-cloud-init-users",
          },
        ],
      }),
      user({ username: "zoro", rootRoutes: [{ kind: "sudo", via: "sudo", usable: false }] }),
    ])
    expect(findings.map((f) => f.id)).toEqual(["root-password", "sudo-nopassword", "no-way-in"])
    expect(findings[1].title).toBe("ubuntu can become root with no password")
    expect(findings[1].detail).toBe("From /etc/sudoers.d/90-cloud-init-users")
    expect(findings[2].title).toBe("ubuntu and zoro have no way in")
  })

  test("a disabled account is not stranded: that was the point", () => {
    expect(accountFindings([user({ disabled: true })])).toEqual([])
  })
})

describe("key restrictions", () => {
  const line = 'restrict,from="10.0.0.0/8,192.168.0.1",command="/bin/backup",expiry-time="20270101"'

  test("a comma inside quotes does not split an option", () => {
    expect(splitKeyOptions(line)).toEqual([
      "restrict",
      'from="10.0.0.0/8,192.168.0.1"',
      'command="/bin/backup"',
      'expiry-time="20270101"',
    ])
  })

  test("reading and writing round-trip, and an option it has no field for survives", () => {
    const read = readRestrictions(`${line},no-pty`)
    expect(read.expires).toBe("2027-01-01")
    expect(read.other).toEqual(["no-pty"])
    expect(writeRestrictions(read).sort()).toEqual(splitKeyOptions(`${line},no-pty`).sort())
  })

  test("the row's words", () => {
    expect(restrictionWords(line)).toEqual([
      "No forwarding",
      "Only from 10.0.0.0/8,192.168.0.1",
      "One command",
      "Expires 2027-01-01",
    ])
    expect(restrictionWords(undefined)).toEqual([])
  })
})

test("a group useradd made for one account is private; one with members is not", () => {
  expect(isPrivateGroup({ name: "ion", primary: ["ion"], members: [] })).toBe(true)
  expect(isPrivateGroup({ name: "ion", primary: ["ion"], members: ["mira"] })).toBe(false)
  expect(isPrivateGroup({ name: "staff", primary: ["ion"], members: [] })).toBe(false)
})
