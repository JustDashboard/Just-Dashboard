import { describe, expect, test } from "bun:test"
import {
  balancingText,
  failuresText,
  memberCheck,
  memberRole,
  poolVerdict,
  poolsOfSite,
} from "./upstream-pools"

const shop = {
  name: "jd_shop_pool",
  kind: "http",
  sites: ["shop.example.com"],
  files: ["/etc/nginx/sites-available/shop"],
  method: "least_conn",
  keepalive: 16,
  balancing: "native",
  verdict: "degraded",
  noLive: 0,
  members: [
    { address: "10.0.0.1:8080", weight: 3, maxFails: 2, failTimeout: "15s", state: "up", ms: 2 },
    { address: "10.0.0.2:8080", state: "refused", failures: { refused: 3, disabled: 1 } },
    { address: "10.0.0.3:8080", backup: true, state: "up", ms: 1 },
    { address: "10.0.0.4:8080", down: true },
  ],
}

describe("upstream pools", () => {
  test("a site's pools are those its file forwards to", () => {
    const report = { checkedAt: "", targets: [], pools: [shop] }
    expect(poolsOfSite(report, "/etc/nginx/sites-available/shop")).toEqual([shop])
    expect(poolsOfSite(report, "/etc/nginx/sites-available/other")).toEqual([])
    expect(poolsOfSite({ checkedAt: "", targets: [] }, "/x")).toEqual([])
  })

  test("native balancing names the method, the primaries and the backups", () => {
    expect(balancingText(shop)).toBe(
      "nginx sends requests to the server with the fewest active connections across 2 servers, keeping up to 16 idle connections open, and sets one aside after it fails. Its backup takes over only when every primary has failed.",
    )
  })

  test("one name's addresses and one address are told apart", () => {
    const dns = {
      ...shop,
      name: undefined,
      balancing: "native-dns",
      members: [{ address: "api.internal:9000", resolved: ["10.0.2.1", "10.0.2.2"], state: "up" }],
    }
    expect(balancingText(dns)).toStartWith(
      "nginx sends requests in turn across the 2 addresses api.internal:9000 resolves to.",
    )
    const single = {
      ...shop,
      name: undefined,
      balancing: "single",
      provider: "AWS Elastic Load Balancing",
      members: [{ address: "lb.elb.amazonaws.com:443", state: "up" }],
    }
    expect(balancingText(single)).toContain("managed outside nginx")
    expect(balancingText(single)).toEndWith(
      "The name suggests AWS Elastic Load Balancing, which balances behind it.",
    )
  })

  test("each server reads its role, its check and what nginx logged", () => {
    expect(memberRole(shop.members[0])).toBe(
      "primary, weight 3, set aside after 2 failures for 15s",
    )
    expect(memberRole(shop.members[2])).toBe("backup")
    expect(memberRole(shop.members[3])).toBe("marked down")
    expect(memberCheck(shop.members[0])).toEqual({ tone: "running", label: "up 2 ms" })
    expect(memberCheck(shop.members[3])).toEqual({ tone: "unknown", label: "not checked" })
    expect(failuresText(shop.members[1])).toBe("refused 3×, set aside 1×")
    expect(failuresText(shop.members[0])).toBe("")
    expect(poolVerdict(shop)).toEqual({ tone: "warning", label: "Degraded" })
  })
})
