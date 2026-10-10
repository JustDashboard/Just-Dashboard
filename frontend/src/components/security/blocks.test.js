import { expect, test } from "bun:test"
import { asPrefix, heldBy, heldSentence, sourceLabel } from "./blocks"

const view = {
  distinct: 3,
  duplicated: 1,
  covered: 1,
  communityOnly: 0,
  truncated: false,
  engines: [],
  entries: [
    {
      value: "203.0.113.9",
      range: false,
      engines: ["crowdsec", "fail2ban"],
      sources: [
        { engine: "fail2ban", ref: "sshd" },
        { engine: "crowdsec", ref: "11", detail: "crowdsecurity/ssh-bf", origin: "crowdsec" },
      ],
      coveredBy: ["203.0.113.0/24"],
    },
    {
      value: "203.0.113.0/24",
      range: true,
      engines: ["firewall"],
      sources: [{ engine: "firewall", ref: "1", detail: "deny" }],
    },
    {
      value: "2001:db8:bad::/48",
      range: true,
      engines: ["crowdsec"],
      sources: [{ engine: "crowdsec", ref: "18", origin: "CAPI", community: true }],
    },
  ],
}

test("an address is matched exactly and by the ranges that contain it", () => {
  const held = heldBy("203.0.113.9", view)
  expect(held.same?.value).toBe("203.0.113.9")
  expect(held.inside.map((e) => e.value)).toEqual(["203.0.113.0/24"])
  expect(heldBy("203.0.113.200", view).same).toBeUndefined()
  expect(heldBy("203.0.113.200", view).inside).toHaveLength(1)
  expect(heldBy("2001:db8:bad::1", view).inside[0].value).toBe("2001:db8:bad::/48")
  expect(heldBy("198.51.100.4", view)).toEqual({ inside: [] })
})

test("half-typed input and a missing view hold nothing", () => {
  expect(heldBy("203.0.", view)).toEqual({ inside: [] })
  expect(heldBy("203.0.113.9", undefined)).toEqual({ inside: [] })
  expect(heldSentence("", view)).toBeUndefined()
})

test("the dialog sentence names every engine already refusing the address", () => {
  expect(heldSentence("203.0.113.9", view)).toBe(
    "203.0.113.9 is already held by fail2ban sshd, CrowdSec #11, and inside 203.0.113.0/24 (the firewall).",
  )
  expect(heldSentence("198.51.100.4", view)).toBeUndefined()
  expect(sourceLabel(view.entries[2].sources[0])).toBe("CrowdSec #18 (community)")
  expect(asPrefix("2001:db8::1")).toBe("2001:db8::1/128")
})
