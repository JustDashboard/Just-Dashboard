import { describe, expect, test } from "bun:test"
import {
  logKindMissing,
  missingFromError,
  moduleOff,
  noSystemd,
  saysDockerUnreachable,
  unreadLine,
} from "./missing-thing-model"

const daemon =
  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"

describe("a server with no systemd", () => {
  test("says why in plain words and names the mount that fixes the one fixable case", () => {
    const missing = noSystemd("services")
    expect(missing.thing).toBe("systemd")
    expect(missing.reason).toBe("no-systemd")
    expect(missing.title).toBe("No systemd on this server")
    expect(missing.sentence).toContain("Services are systemd units")
    expect(missing.sentence).toContain("/run/systemd")
    expect(missing.steps).toEqual([
      { kind: "link", label: "Open Processes", href: "/processes" },
      { kind: "copy", label: "Copy the mount", text: "/run/systemd:/run/systemd" },
    ])
  })

  test("names what the page would have read", () => {
    expect(noSystemd("timers").sentence).toStartWith("Timers are systemd units")
    expect(noSystemd("service").sentence).toStartWith("A service is a systemd unit")
    expect(noSystemd("journal").sentence).toStartWith("The journal is systemd's")
  })

  test("keeps systemctl's own words for the fold, never in the sentence", () => {
    const raw = "System has not been booted with systemd as init system (PID 1). Can't operate."
    const missing = noSystemd("service", `  ${raw}\n`)
    expect(missing.raw).toBe(raw)
    expect(missing.title).not.toContain("booted")
    expect(missing.sentence).not.toContain("booted")
    expect(noSystemd("service", "  ").raw).toBeUndefined()
  })
})

describe("a module turned off in Configuration", () => {
  test("sends an account that may change it to Configuration", () => {
    const missing = moduleOff({ name: "The web terminal", setting: "Features" }, true)
    expect(missing.reason).toBe("module-off")
    expect(missing.title).toBe("The web terminal is turned off")
    expect(missing.sentence).toContain("Features in Configuration")
    expect(missing.steps).toEqual([
      { kind: "link", label: "Open Configuration", href: "/dashboard/configuration" },
    ])
  })

  test("tells anybody else who can, with nothing to press", () => {
    const missing = moduleOff({ name: "Remote Desktop", thing: "chrome" }, false)
    expect(missing.thing).toBe("chrome")
    expect(missing.sentence).toContain("An administrator can turn it on")
    expect(missing.steps).toEqual([])
  })
})

describe("the absence behind a failed read", () => {
  test("a daemon that did not answer points at the Docker page", () => {
    const missing = missingFromError({
      code: "docker_unavailable",
      message: "The Docker daemon is not reachable from this dashboard.",
      raw: daemon,
    })
    expect(missing).toMatchObject({
      thing: "docker",
      reason: "docker-silent",
      title: "Docker is not answering",
      raw: daemon,
    })
    expect(missing.title).not.toContain("Cannot connect")
    expect(missing.sentence).not.toContain("Cannot connect")
    expect(missing.steps).toEqual([{ kind: "link", label: "Open Docker", href: "/docker" }])
  })

  test("systemd that is not running, however the route said it", () => {
    for (const refusal of [
      { code: "systemd_not_running", message: "systemd is not running on this host" },
      { code: "not_installed", resource: "systemd", message: "systemd is not running." },
      { code: "not_installed", message: "systemctl is not installed" },
    ]) {
      const missing = missingFromError(refusal, { systemd: "service" })
      expect(missing?.reason).toBe("no-systemd")
      expect(missing?.sentence).toStartWith("A service is a systemd unit")
    }
  })

  test("a terminal that is off is a module, and the step is an administrator's", () => {
    expect(missingFromError({ code: "terminal_disabled" })?.steps).toEqual([])
    expect(missingFromError({ code: "terminal_disabled" }, { canConfigure: true })?.steps).toEqual([
      { kind: "link", label: "Open Configuration", href: "/dashboard/configuration" },
    ])
  })

  test("another program that is not installed, and any other failure, stay failures", () => {
    expect(missingFromError({ code: "not_installed", message: "git is not installed" })).toBe(
      undefined,
    )
    expect(missingFromError({ code: "command_failed", message: "exit status 1" })).toBeUndefined()
    expect(missingFromError(new Error("network"))).toBeUndefined()
    expect(missingFromError(undefined)).toBeUndefined()
    expect(missingFromError("docker_unavailable")).toBeUndefined()
  })
})

describe("one line for several reads that failed for one reason", () => {
  test("lists the kinds once, in the order the map draws them", () => {
    expect(
      unreadLine("Docker is not answering", ["Volumes", "Containers", "Stacks", "Containers"]),
    ).toBe("Docker is not answering: containers, stacks and volumes not drawn")
    expect(unreadLine("Docker is not answering", ["Containers"])).toBe(
      "Docker is not answering: containers not drawn",
    )
    expect(unreadLine("Docker is not running", ["containers", "stacks"], "listed")).toBe(
      "Docker is not running: containers and stacks not listed",
    )
    expect(unreadLine("Docker is not answering", [])).toBe("Docker is not answering")
  })
})

describe("a sentence that is a daemon nobody reached", () => {
  test("the client's question and the backend's reading of it", () => {
    expect(saysDockerUnreachable(daemon)).toBe(true)
    expect(saysDockerUnreachable("The Docker daemon is not reachable from this dashboard.")).toBe(
      true,
    )
    expect(saysDockerUnreachable("docker is not available on this host")).toBe(true)
    expect(saysDockerUnreachable("permission denied")).toBe(false)
    expect(saysDockerUnreachable(undefined)).toBe(false)
  })
})

describe("a kind of log source that is absent", () => {
  test("Docker's question becomes a sentence and the way to the Docker page", () => {
    const line = logKindMissing("docker", daemon)
    expect(line).toEqual({
      thing: "docker",
      sentence: "Docker is not running",
      href: "/docker",
      label: "Open Docker",
    })
    // A socket that refused the dashboard is a daemon that runs.
    expect(logKindMissing("docker", "permission denied").sentence).toBe("Docker did not answer")
  })

  test("PM2 says which of its three reasons it is", () => {
    expect(logKindMissing("pm2", "PM2 is not installed on this host").sentence).toBe(
      "PM2 is not installed",
    )
    expect(logKindMissing("pm2", "PM2 has no managed processes").sentence).toBe(
      "PM2 runs no applications",
    )
    expect(logKindMissing("pm2", "exit status 1")).toMatchObject({
      sentence: "PM2 did not answer",
      href: "/processes/pm2",
    })
  })

  test("the journal without systemd points at Services, which says why", () => {
    expect(logKindMissing("journal", "systemd is not running on this host")).toEqual({
      thing: "systemd",
      sentence: "No systemd on this server",
      href: "/processes/services",
      label: "Open Services",
    })
  })

  test("a kind this does not know keeps its reason, as a sentence", () => {
    expect(logKindMissing("nginx", "no access log is written")).toEqual({
      sentence: "No access log is written",
    })
    expect(logKindMissing("app", undefined).sentence).toBe("Not on this server")
  })
})
