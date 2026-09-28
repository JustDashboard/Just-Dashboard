import { describe, expect, test } from "bun:test"
import { recentLabels } from "./recent-jobs"

const job = (target, startedAt, title = "Running certbot.service") => ({ target, title, startedAt })
const clock = (iso, seconds = false) => {
  const at = new Date(iso)
  const parts = [at.getHours(), at.getMinutes(), ...(seconds ? [at.getSeconds()] : [])]
  return parts.map((n) => String(n).padStart(2, "0")).join(":")
}

describe("the recent runs' labels", () => {
  test("a run is named by what it acted on, or its title when that is a path", () => {
    expect(
      recentLabels([
        job("app.example.com", "2026-09-28T09:00:00Z", "Renewing app.example.com"),
        job("/etc/ssh/sshd_config.d/10-jd.conf", "2026-09-28T09:01:00Z", "Applying SSH settings"),
      ]),
    ).toEqual(["app.example.com", "Applying SSH settings"])
  })

  test("runs that share a name are told apart by when they started", () => {
    const first = "2026-09-28T09:12:00Z"
    const second = "2026-09-28T21:13:00Z"
    expect(
      recentLabels([
        job("certbot.service", second),
        job("certbot.service", first),
        job("app.example.com", first, "Renewing app.example.com"),
      ]),
    ).toEqual([
      `certbot.service ${clock(second)}`,
      `certbot.service ${clock(first)}`,
      "app.example.com",
    ])
  })

  test("two started in the same minute are told apart by the second", () => {
    const early = "2026-09-28T05:04:31Z"
    const late = "2026-09-28T05:04:52Z"
    const next = "2026-09-28T05:05:52Z"
    expect(
      recentLabels([
        job("certbot.service", next),
        job("certbot.service", late),
        job("certbot.service", early),
      ]),
    ).toEqual([
      `certbot.service ${clock(next)}`,
      `certbot.service ${clock(late, true)}`,
      `certbot.service ${clock(early, true)}`,
    ])
  })
})
