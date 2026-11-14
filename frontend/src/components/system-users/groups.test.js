import { describe, expect, test } from "bun:test"
import {
  groupFigures,
  groupHref,
  groupPeople,
  groupReach,
  groupReachesRoot,
  listGroups,
  validGid,
} from "./groups"

const group = (fields) => ({
  name: "developers",
  gid: 1100,
  members: [],
  primary: [],
  system: false,
  rootEquivalent: false,
  sudoRules: [],
  protected: false,
  ...fields,
})

const rule = (fields) => ({
  id: "r",
  file: "/etc/sudoers",
  line: 50,
  text: "%sudo ALL=(ALL:ALL) ALL",
  who: ["%sudo"],
  runAs: "ALL : ALL",
  commands: ["ALL"],
  noPassword: false,
  full: true,
  asRoot: true,
  managed: false,
  ...fields,
})

const host = [
  group({ name: "adm", gid: 4, system: true, members: ["ion"] }),
  group({ name: "developers", members: ["ion", "mira"] }),
  group({
    name: "docker",
    gid: 988,
    system: true,
    rootEquivalent: true,
    members: ["deploy", "ion"],
  }),
  group({ name: "ion", gid: 1000, primary: ["ion"] }),
  group({ name: "sudo", gid: 27, system: true, sudoRules: [rule()], members: ["ion", "zoro"] }),
  group({ name: "www-data", gid: 33, system: true, members: ["alex"], primary: ["www-data"] }),
]

describe("groupReach", () => {
  test("sudo's own rule is read before the group's name", () => {
    expect(groupReach(group({ name: "sudo", sudoRules: [rule()] }))).toMatchObject({
      tone: "warning",
      label: "Root, asks for a password",
    })
    expect(
      groupReach(group({ name: "wheel", sudoRules: [rule({ noPassword: true })] })),
    ).toMatchObject({ tone: "danger", text: "Root with no password" })
  })

  test("a rule for named commands says which", () => {
    const reach = groupReach(
      group({ sudoRules: [rule({ full: false, commands: ["/usr/bin/systemctl restart api"] })] }),
    )
    expect(reach.tone).toBe("default")
    expect(reach.text).toBe("Runs /usr/bin/systemctl restart api as root")
    expect(reach.detail).toBe("/usr/bin/systemctl restart api")
  })

  test("root by another name keeps what it can do apart from the verdict", () => {
    const reach = groupReach(group({ name: "docker", rootEquivalent: true }))
    expect(reach.label).toBe("Root by another name")
    expect(reach.detail).toBe("can mount the host's files into a container")
    expect(groupReach(group({ name: "kvm", rootEquivalent: true }))).toMatchObject({
      text: "Root by another name",
      detail: undefined,
    })
  })

  test("a group that hands nothing over says nothing", () => {
    expect(groupReach(group({}))).toEqual({ tone: "muted", label: "", text: "" })
    expect(groupReach(group({ name: "adm" })).label).toBe("Reads the system logs")
  })
})

describe("groupReachesRoot", () => {
  test("a rule for some commands is not the whole of root", () => {
    expect(groupReachesRoot(group({ sudoRules: [rule({ full: false })] }))).toBe(false)
    expect(groupReachesRoot(group({ sudoRules: [rule()] }))).toBe(true)
    expect(groupReachesRoot(group({ rootEquivalent: true }))).toBe(true)
  })
})

describe("groupPeople", () => {
  test("members first, then those it is the main group of, each once", () => {
    expect(groupPeople({ members: ["alex", "www-data"], primary: ["www-data", "caddy"] })).toEqual([
      "alex",
      "www-data",
      "caddy",
    ])
  })
})

describe("listGroups", () => {
  const names = (options) => listGroups(host, options).map((g) => g.name)

  test("what reaches root leads, and a package's or a private group is behind the switch", () => {
    expect(names({ showSystem: false })).toEqual(["docker", "sudo", "developers"])
    expect(names({ showSystem: true })).toEqual([
      "docker",
      "sudo",
      "developers",
      "ion",
      "adm",
      "www-data",
    ])
  })

  test("the search reads names and members, whatever their case", () => {
    expect(names({ showSystem: false, query: " MIRA " })).toEqual(["developers"])
    expect(names({ showSystem: true, query: "www" })).toEqual(["www-data"])
    expect(names({ showSystem: true, query: "caddy" })).toEqual([])
  })

  test("the root scope keeps only what is the whole of root", () => {
    expect(names({ showSystem: true, scope: "root" })).toEqual(["docker", "sudo"])
  })
})

describe("groupFigures", () => {
  test("counts an account in two root groups once", () => {
    expect(groupFigures(host)).toEqual({
      total: 6,
      reachRoot: 2,
      rootMembers: 3,
      system: 4,
      own: 1,
    })
  })
})

describe("groupHref", () => {
  test("the first view is the bare address", () => {
    expect(groupHref("www-data")).toBe("/system-users/groups/www-data")
    expect(groupHref("developers", "overview")).toBe("/system-users/groups/developers")
    expect(groupHref("developers", "members")).toBe("/system-users/groups/developers?tab=members")
  })
})

describe("validGid", () => {
  test("an empty box is not group 0", () => {
    expect(validGid("")).toBe(false)
    expect(validGid(" ")).toBe(false)
    expect(validGid("0")).toBe(true)
    expect(validGid("1100")).toBe(true)
    expect(validGid("-1")).toBe(false)
    expect(validGid("1e3")).toBe(false)
  })
})
