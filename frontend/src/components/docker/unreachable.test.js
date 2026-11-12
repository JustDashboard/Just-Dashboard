import { describe, expect, test } from "bun:test"
import { dockerUnreachable } from "./unreachable"

const host = "unix:///var/run/docker.sock"
const refusedConnection =
  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"
const deniedSocket =
  'permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: Get "http://%2Fvar%2Frun%2Fdocker.sock/_ping": dial unix /var/run/docker.sock: connect: permission denied'

const unit = (activeState, loadState = "loaded") => ({ activeState, loadState })

describe("why Docker did not answer", () => {
  test("a socket that refused the dashboard is not a stopped daemon", () => {
    const state = dockerUnreachable({ available: false, host, error: deniedSocket }, unit("active"))
    expect(state.fault).toBe("refused")
    expect(state.title).toBe("The dashboard may not open Docker's socket")
    expect(state.description).toContain("/var/run/docker.sock is there and refused")
    expect(state.description).toContain("docker")
    expect(state.startable).toBe(false)
    expect(state.raw).toBe(deniedSocket)
  })

  test("a unit systemd says is not running is stopped, and can be started", () => {
    for (const activeState of ["inactive", "failed", "deactivating"]) {
      const state = dockerUnreachable(
        { available: false, host, error: refusedConnection },
        unit(activeState),
      )
      expect(state.fault).toBe("stopped")
      expect(state.title).toBe("Docker is stopped")
      expect(state.description).toContain(`docker.service is ${activeState}`)
      expect(state.startable).toBe(true)
    }
    // One that is already starting is not offered a second start.
    expect(
      dockerUnreachable({ available: false, host, error: refusedConnection }, unit("activating"))
        .startable,
    ).toBe(false)
  })

  test("a daemon that runs and does not answer is somewhere the dashboard does not ask", () => {
    const state = dockerUnreachable(
      { available: false, host: "tcp://10.0.0.5:2375", error: "context deadline exceeded" },
      unit("active"),
    )
    expect(state.fault).toBe("elsewhere")
    expect(state.description).toContain("nothing answers at tcp://10.0.0.5:2375")
    expect(state.description).toContain("JD_DOCKER_HOST")
    expect(state.startable).toBe(false)
  })

  test("without a word from systemd it claims neither", () => {
    for (const reading of [undefined, unit("inactive", "not-found")]) {
      const state = dockerUnreachable({ available: false, error: refusedConnection }, reading)
      expect(state.fault).toBe("silent")
      expect(state.title).toBe("Docker is not answering")
      expect(state.description).toContain("/var/run/docker.sock")
      expect(state.startable).toBe(false)
    }
    // No error at all, as when the ping itself could not be read.
    expect(dockerUnreachable({ available: false }, undefined)).toMatchObject({
      fault: "silent",
      raw: undefined,
    })
  })

  test("an answer that is not a connection failure is shown as what it is", () => {
    const state = dockerUnreachable(
      {
        available: false,
        host,
        error:
          "Error response from daemon: client version 1.52 is too new. Maximum supported API version is 1.43",
      },
      unit("active"),
    )
    expect(state.fault).toBe("other")
    expect(state.raw).toContain("too new")
    expect(state.startable).toBe(false)
  })

  test("a client that could not be made is read as a wrong address, not a stopped daemon", () => {
    const state = dockerUnreachable(
      {
        available: false,
        host: "bogus",
        error: "docker is not available on this host: unable to parse docker host `bogus`",
      },
      unit("active"),
    )
    expect(state.fault).toBe("elsewhere")
  })
})
