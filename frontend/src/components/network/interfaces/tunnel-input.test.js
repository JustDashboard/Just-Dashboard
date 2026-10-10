import { expect, test } from "bun:test"
import { tunnelProblems } from "./tunnel-input"

const draft = { vni: "42", remote: "", group: "", local: "", parent: "", key: "", ttl: "" }
const problems = (kind, values) =>
  Object.fromEntries(
    Object.entries(tunnelProblems(kind, { ...draft, ...values })).filter(([, v]) => v),
  )

test("multicast VXLAN requires a group, its sending card, and one endpoint family", () => {
  expect(problems("vxlan", { group: "239.1.1.1", parent: "ens3", local: "192.0.2.1" })).toEqual({})
  expect(problems("vxlan", { group: "ff05::42", parent: "ens3", local: "2001:db8::1" })).toEqual({})
  expect(problems("vxlan", { group: "239.1.1.1" }).parent).toContain("network card")
  expect(problems("vxlan", { group: "192.0.2.7", parent: "ens3" }).group).toContain("multicast")
  expect(
    problems("vxlan", { group: "ff05::42", parent: "ens3", local: "192.0.2.1" }).local,
  ).toContain("same address family")
  expect(problems("vxlan", { group: "ff05::42%ens3", parent: "ens3" }).group).toContain("multicast")
  expect(
    problems("vxlan", { group: "239.1.1.1", remote: "192.0.2.7", parent: "ens3" }).group,
  ).toContain("not both")
})

test("unicast VXLAN rejects multicast, unspecified and malformed ends without guessing a default", () => {
  for (const remote of ["239.1.1.1", "ff05::42", "0.0.0.0", "::", "192.0.2.7/32", "192.000.2.7"])
    expect(problems("vxlan", { remote }).remote).toBeDefined()
  for (const vni of ["0", "16777216", "42.5", "4e2", "-1", "bogus"])
    expect(problems("vxlan", { remote: "192.0.2.7", vni }).vni).toBeDefined()
  expect(problems("vxlan", { remote: "2001:db8::7", vni: "16777215" })).toEqual({})
})

test("GRE variants use their requested family and optional decimal uint32 keys and hop limits", () => {
  for (const kind of ["gre", "gretap", "ip6gre", "ip6gretap"]) {
    const remote = kind.startsWith("ip6") ? "2001:db8::7" : "192.0.2.7"
    expect(problems(kind, { remote, key: "4294967295", ttl: "255" })).toEqual({})
    expect(problems(kind, { remote, key: "0", ttl: "0" })).toEqual({})
    expect(problems(kind, { remote, key: "", ttl: "" })).toEqual({})
    for (const key of ["4294967296", "-1", "0x80000", "1.5", "1e3", "NaN"])
      expect(problems(kind, { remote, key }).key).toBeDefined()
    for (const ttl of ["256", "-1", "3.5", "0x40", "Infinity"])
      expect(problems(kind, { remote, ttl }).ttl).toBeDefined()
    expect(
      problems(kind, { remote: kind.startsWith("ip6") ? "192.0.2.7" : "2001:db8::7" }).remote,
    ).toContain("endpoints")
    expect(
      problems(kind, { remote, local: kind.startsWith("ip6") ? "192.0.2.1" : "2001:db8::1" }).local,
    ).toContain("same address family")
  }
})
