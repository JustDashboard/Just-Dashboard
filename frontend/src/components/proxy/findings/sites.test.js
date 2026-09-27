import { describe, expect, test } from "bun:test"
import { siteFindings } from "./sites"

const site = (over) => ({
  name: "app",
  kind: "nginx",
  path: "/etc/nginx/sites-available/app",
  enabledPath: "/etc/nginx/sites-enabled/app",
  enabled: true,
  layout: "sites-available",
  formEditable: true,
  serverNames: ["app.example.com"],
  listen: ["443 ssl"],
  upstreams: ["http://127.0.0.1:3000"],
  tls: true,
  modified: "",
  size: 0,
  ...over,
})

describe("site findings", () => {
  test("a link to nothing is critical, says why, and is not also 'not serving'", () => {
    const findings = siteFindings({
      vhosts: [
        site({
          name: "ghost",
          layout: "sites-enabled",
          path: "",
          enabled: false,
          broken: "dangling",
          linkTarget: "/etc/nginx/sites-available/gone",
          serverNames: [],
          upstreams: [],
        }),
      ],
    })
    expect(findings).toHaveLength(1)
    expect(findings[0].level).toBe("critical")
    expect(findings[0].title).toBe("sites-enabled/ghost points at a file that is gone")
    expect(findings[0].detail).toContain("/etc/nginx/sites-available/gone")
    expect(findings[0].detail).toContain("refuses every reload")
    expect(findings[0].advice).toBe(
      "Remove the link from Sites, or put the file back where it points.",
    )
  })

  test("a site whose own link dangles is repaired by enabling it", () => {
    const [finding] = siteFindings({
      vhosts: [site({ enabled: false, broken: "dangling", linkTarget: "/gone" })],
    })
    expect(finding.advice).toContain("Enable app from Sites")
  })

  test("a stale link names the file nginx serves instead", () => {
    const [finding] = siteFindings({
      vhosts: [
        site({ enabled: false, broken: "stale", linkTarget: "/etc/nginx/sites-available/old" }),
      ],
    })
    expect(finding.level).toBe("warning")
    expect(finding.detail).toBe(
      "sites-enabled/app points at /etc/nginx/sites-available/old, so nginx serves that file and not /etc/nginx/sites-available/app.",
    )
  })

  test("a disabled site and a plain one keep their findings", () => {
    const findings = siteFindings({
      vhosts: [site({ name: "off", enabled: false }), site({ name: "plain", tls: false })],
    })
    expect(findings.map((f) => [f.id, f.level])).toEqual([
      ["site.disabled.off", "notice"],
      ["site.plain.plain", "warning"],
    ])
  })
})
