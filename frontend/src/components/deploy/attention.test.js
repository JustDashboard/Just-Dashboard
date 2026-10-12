import { expect, test } from "bun:test"
import { needsProjectAttention, projectFindingNavigation } from "./attention"

test("a pending draft is not a failure, and genuine deployment failures remain", () => {
  expect(needsProjectAttention({ severity: "notice" })).toBe(false)
  expect(needsProjectAttention({ severity: "warning" })).toBe(true)
  expect(needsProjectAttention({ severity: "critical" })).toBe(true)
})

test("route faults open domain routing, and certificate faults open issuance or renewal", () => {
  expect(
    projectFindingNavigation({ code: "domain_unrouted", deepLink: "/proxy/sites" }, 12),
  ).toEqual({
    label: "Review domain routing",
    href: "/deploy/12/settings/domains",
  })
  for (const code of ["certificate_missing", "certificate_expired", "certificate_expiring"])
    expect(
      projectFindingNavigation({ code, deepLink: "/proxy/certificates?cert=%2Fapp.pem" }, 12),
    ).toEqual({
      label:
        code === "certificate_missing"
          ? "Review certificate issuance"
          : "Review certificate renewal",
      href: "/proxy/certificates?cert=%2Fapp.pem",
    })
  expect(projectFindingNavigation({ code: "certificate_expiring" }, 12).href).toBe(
    "/proxy/certificates",
  )
})

test("missing resources never open a deleted job or an unrelated container listing", () => {
  expect(
    projectFindingNavigation({ code: "backup_missing", deepLink: "/backups?job=gone" }, 12).href,
  ).toBe("/backups")
  expect(
    projectFindingNavigation({ code: "runtime_absent", deepLink: "/docker/containers" }, 12).href,
  ).toBe("/deploy/12/runtime")
  expect(
    projectFindingNavigation(
      { code: "runtime_unhealthy", deepLink: "/docker/containers?container=api" },
      12,
    ).href,
  ).toBe("/docker/containers/api")
  expect(
    projectFindingNavigation({ code: "last_deploy_failed", deepLink: "/deploy/12/runs/44" }, 12)
      .href,
  ).toBe("/deploy/12/runs/44")
  expect(
    projectFindingNavigation(
      { code: "dependency_unavailable", deepLink: "https://provider.test", external: true },
      12,
    ),
  ).toBeUndefined()
})
