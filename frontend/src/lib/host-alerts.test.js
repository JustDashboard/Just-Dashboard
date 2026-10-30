import { describe, expect, test } from "bun:test"
import {
  HOST_ALERT_DEFAULTS,
  HOST_ALERT_KINDS,
  cadenceWords,
  describeHostAlert,
  hostAlertCounts,
  hostAlertDraft,
  hostAlertFields,
  minutesWords,
  readsMetrics,
} from "./host-alerts"

describe("host alerts", () => {
  test("each kind says what it watches for, from its defaults when nothing is given", () => {
    expect(describeHostAlert("cpu_high", {})).toBe("averages 90% or more over 10 min")
    expect(describeHostAlert("memory_high", { threshold: 85 })).toBe(
      "averages 85% or more over 5 min",
    )
    expect(describeHostAlert("disk_filling", { threshold: 80 })).toBe(
      "a filesystem reaches 80% of its space or inodes",
    )
    expect(describeHostAlert("container_down", { minutes: 120 })).toBe(
      "exits, keeps restarting or turns unhealthy for 2 h",
    )
    expect(describeHostAlert("backup_failed", {})).toBe("the latest run of a backup job fails")
    expect(describeHostAlert("image_vulnerable", {})).toBe(
      "a live release's image has a fixable vulnerability and a newer base to rebuild on",
    )
    expect(hostAlertFields("image_vulnerable")).toEqual({ threshold: false, minutes: false })
    expect(describeHostAlert("tunnel_down", {})).toBe(
      "holds no connection to Cloudflare's edge for 2 min",
    )
    expect(describeHostAlert("account_changed", {})).toBe(
      "a new account, SSH key or root-reaching sudo rule appears, or root signs in",
    )
    expect(hostAlertFields("account_changed")).toEqual({ threshold: false, minutes: false })
    for (const kind of HOST_ALERT_KINDS) expect(HOST_ALERT_DEFAULTS[kind]).toBeDefined()
  })

  test("a form sends only the figures its kind reads, inside the server's ranges", () => {
    expect(hostAlertDraft("cpu_high", { threshold: "92.5", minutes: "15" })).toEqual({
      params: { threshold: 92.5, minutes: 15 },
    })
    expect(hostAlertDraft("disk_filling", { threshold: "95", minutes: "30" })).toEqual({
      params: { threshold: 95 },
    })
    expect(hostAlertDraft("container_down", { threshold: "", minutes: "3" })).toEqual({
      params: { minutes: 3 },
    })
    expect(hostAlertDraft("backup_failed", { threshold: "x", minutes: "y" })).toEqual({
      params: {},
    })
    expect(hostAlertDraft("disk_filling", { threshold: "100", minutes: "" }).problem).toBe(
      "The line is a percentage between 10 and 99.",
    )
    expect(hostAlertDraft("cpu_high", { threshold: "", minutes: "10" }).problem).toBeDefined()
    expect(hostAlertDraft("memory_high", { threshold: "90", minutes: "1.5" }).problem).toBe(
      "The minutes are a whole number between 1 and 1440.",
    )
    expect(hostAlertFields("disk_filling")).toEqual({ threshold: true, minutes: false })
    expect(hostAlertFields("backup_failed")).toEqual({ threshold: false, minutes: false })
    expect(readsMetrics("container_down")).toBe(false)
    expect(readsMetrics("disk_filling")).toBe(true)
  })

  test("durations and cadence read as words", () => {
    expect(minutesWords(1)).toBe("1 min")
    expect(minutesWords(90)).toBe("90 min")
    expect(minutesWords(180)).toBe("3 h")
    expect(minutesWords(1440)).toBe("1 day")
    expect(minutesWords(2880)).toBe("2 days")
    expect(cadenceWords(60)).toBe("every minute")
    expect(cadenceWords(300)).toBe("every 5 minutes")
  })

  test("a muted subject is neither firing nor holding", () => {
    expect(
      hostAlertCounts([
        { state: "firing", muted: false },
        { state: "firing", muted: true },
        { state: "pending", muted: false },
        { state: "quiet", muted: true },
      ]),
    ).toEqual({ firing: 1, holding: 1 })
  })
})
