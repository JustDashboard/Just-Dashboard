import { describe, expect, test } from "bun:test"
import {
  LISTED_SERVICES,
  certificateGroups,
  groupLabel,
  groupShort,
  leadFirst,
  listedServices,
  passwordWords,
  serviceLabel,
  shared,
} from "./wiring-model"

describe("the containers", () => {
  const container = (name, state = "running") => ({ name, service: name, state })

  test("several services are named by their Compose service", () => {
    expect(serviceLabel({ name: "jd-e480-bazarr-1", service: "bazarr" }, true)).toBe("bazarr")
    // One container, a replica or one with no service keeps the container's name.
    expect(serviceLabel({ name: "jd-e12-api", service: "app" }, false)).toBe("jd-e12-api")
    expect(serviceLabel({ name: "api-2", service: "app", replica: 2 }, true)).toBe("api-2")
    expect(serviceLabel({ name: "jd-e12-api" }, true)).toBe("jd-e12-api")
  })

  test("up to four are all named", () => {
    const four = ["a", "b", "c", "d"].map((name) => container(name))
    expect(listedServices(four)).toEqual({ shown: four, hidden: 0 })
    expect(LISTED_SERVICES).toBe(4)
  })

  test("eight are three names and five counted, one that is not running named first", () => {
    const eight = ["a", "b", "c", "d", "e", "f", "g", "h"].map((name) =>
      container(name, name === "g" ? "exited" : "running"),
    )
    const { shown, hidden } = listedServices(eight)
    expect(shown.map((service) => service.name)).toEqual(["g", "a", "b"])
    expect(hidden).toBe(5)
  })

  test("one whose health check fails is named first too, though it runs", () => {
    const eight = ["a", "b", "c", "d", "e", "f", "g", "h"].map((name) => ({
      ...container(name),
      health: name === "h" ? "unhealthy" : "healthy",
    }))
    expect(listedServices(eight).shown.map((service) => service.name)).toEqual(["h", "a", "b"])
  })
})

const domain = (hostname, overrides = {}) => ({
  hostname,
  https: true,
  ownership: "managed",
  route: "served",
  certificate: "valid",
  certificateDaysLeft: 85,
  ...overrides,
})

describe("the lead name", () => {
  test("the deployment's own address leads, the rest keep their order", () => {
    const names = [domain("a.test"), domain("b.test"), domain("media.test"), domain("c.test")]
    expect(leadFirst(names, "media.test").map((one) => one.hostname)).toEqual([
      "media.test",
      "a.test",
      "b.test",
      "c.test",
    ])
  })

  test("with no address among them, the list is as given", () => {
    const names = [domain("a.test"), domain("b.test")]
    expect(leadFirst(names, "other.test")).toBe(names)
    expect(leadFirst(names, undefined)).toBe(names)
  })
})

describe("the certificates in one line", () => {
  test("seven valid certificates are one group, the fewest days left first", () => {
    const names = [
      domain("a.test", { certificateDaysLeft: 85 }),
      domain("b.test", { certificateDaysLeft: 40 }),
      ...["c", "d", "e", "f", "g"].map((label) => domain(`${label}.test`)),
    ]
    const groups = certificateGroups(names)
    expect(groups).toHaveLength(1)
    expect(groups[0].state).toBe("valid")
    expect(groups[0].domains[0].hostname).toBe("b.test")
    expect(groupLabel("valid", 7)).toBe("7 certificates valid")
  })

  test("the state that needs a look leads, whatever the order of the names", () => {
    const names = [
      domain("a.test"),
      domain("b.test", { certificate: "not requested", certificateDaysLeft: undefined }),
      domain("c.test", { certificate: "expiring", certificateDaysLeft: 9 }),
      domain("d.test"),
      domain("e.test", { certificate: "expired", certificateDaysLeft: 0 }),
    ]
    expect(certificateGroups(names).map((group) => [group.state, group.domains.length])).toEqual([
      ["expired", 1],
      ["expiring", 1],
      ["not requested", 1],
      ["valid", 2],
    ])
  })

  test("one certificate says its own word, the rest are counted short", () => {
    expect(groupLabel("expiring", 1)).toBe("Certificate expiring")
    expect(groupLabel("missing", 3)).toBe("3 with no certificate")
    expect(groupLabel("not requested", 2)).toBe("2 HTTP only")
    expect(groupShort("valid", 6)).toBe("6 valid")
    expect(groupShort("unavailable", 1)).toBe("1 not observed")
  })

  test("certificates that could not be read say what was not observed", () => {
    // Under the lead name there is no certificate column to say it for them.
    expect(groupLabel("unavailable", 1)).toBe("Certificate not observed")
    expect(groupLabel("unavailable", 7)).toBe("7 certificates not observed")
  })
})

describe("what the names share", () => {
  test("a value is said once only when every name has it", () => {
    expect(shared(["R10", "R10", "R10"])).toBe("R10")
    expect(shared(["R10", "E5"])).toBeUndefined()
    expect(shared(["R10", undefined])).toBeUndefined()
    expect(shared([])).toBeUndefined()
  })

  test("a password is a tag when every name asks for it, else counted", () => {
    expect(passwordWords([domain("a.test"), domain("b.test")])).toBeUndefined()
    expect(passwordWords([domain("a.test", { protected: true })])).toBe("Password")
    expect(
      passwordWords([domain("a.test", { protected: true }), domain("b.test"), domain("c.test")]),
    ).toBe("1 with a password")
  })
})
