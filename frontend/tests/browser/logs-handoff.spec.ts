import { expect, test } from "@playwright/test"
import { json, mockProject } from "./deploy-fixture"

/**
 * The Logs page honours the exact window a deployment run hands it and never
 * substitutes another source for one that is gone. This lived in the old
 * deployment spec although it exercises `/logs`; it keeps its own file now so
 * the deployment specs can be rewritten around the redesigned screens.
 */

test.describe("runtime log handoffs", () => {
  test.use({ timezoneId: "America/New_York" })

  test("log links preserve exact activation windows and never substitute a missing source", async ({
    page,
  }) => {
    await mockProject(page)
    const searches: URL[] = []
    await page.route("**/api/v1/logs/**", async (route) => {
      const url = new URL(route.request().url())
      if (url.pathname.endsWith("/sources"))
        return json(route, {
          sources: [
            { id: "file:/var/log/syslog", label: "syslog", kind: "system", rotated: false },
            { id: "docker:abc123", label: "web-live", kind: "docker", rotated: false },
          ],
          units: [],
          roots: ["/var/log"],
          missing: {},
        })
      if (url.pathname.endsWith("/search")) {
        searches.push(url)
        return json(route, {
          lines: [{ text: "activation evidence", source: "docker:abc123" }],
          scanned: 1,
          matched: 1,
          truncated: false,
          complete: true,
          files: [],
          histogram: [],
          tookMillis: 1,
        })
      }
      return json(route, {})
    })
    const since = "2026-11-01T06:25:30.123Z"
    const until = "2026-11-01T06:35:30.456Z"
    await page.goto(`/logs?${new URLSearchParams({ source: "docker:abc123", since, until })}`)
    await expect.poll(() => searches.length).toBe(1)
    expect(searches[0].searchParams.get("source")).toBe("docker:abc123")
    expect(searches[0].searchParams.get("since")).toBe(since)
    expect(searches[0].searchParams.get("until")).toBe(until)
    await expect(page.getByText("activation evidence", { exact: true })).toBeVisible()
    await page.reload()
    await expect.poll(() => searches.length).toBe(2)
    expect(searches[1].searchParams.get("since")).toBe(since)
    expect(searches[1].searchParams.get("until")).toBe(until)

    await page.goto("/logs?source=docker:removed&mode=search")
    await expect(page.getByText("Requested log source unavailable", { exact: true })).toBeVisible()
    expect(new URL(page.url()).searchParams.get("source")).toBe("docker:removed")
    expect(searches).toHaveLength(2)

    for (const bounds of [
      { since: "invalid", until },
      { since: until, until: since },
    ]) {
      await page.goto(`/logs?${new URLSearchParams({ source: "docker:abc123", ...bounds })}`)
      await expect(page.getByText("Invalid log window", { exact: true })).toBeVisible()
      expect(searches).toHaveLength(2)
    }
    await page.getByRole("button", { name: "Use last 24 hours" }).click()
    await expect.poll(() => searches.length).toBe(3)
    expect(searches[2].searchParams.get("source")).toBe("docker:abc123")
    expect(searches[2].searchParams.has("until")).toBe(false)
  })
})
