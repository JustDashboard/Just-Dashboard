import { describe, expect, test } from "bun:test"
import { unreadNotices } from "./unread"

const daemon =
  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"

describe("what the map could not read", () => {
  test("every kind Docker would have answered for is one line with one way on", () => {
    const notices = unreadNotices([
      { key: "containers", label: "Containers", reason: daemon },
      { key: "stacks", label: "Compose stacks", reason: daemon },
      { key: "volumes", label: "Volumes", reason: `Last read 2m ago: ${daemon}` },
    ])
    expect(notices).toHaveLength(1)
    expect(notices[0]).toMatchObject({
      key: "docker",
      thing: "docker",
      sentence: "Docker is not answering: containers, stacks and volumes not drawn",
      href: "/docker",
      label: "Open Docker",
    })
    // The client's question is kept for the fold, once per wording.
    expect(notices[0].sentence).not.toContain("Cannot connect")
    expect(notices[0].raw).toBe(`${daemon}\nLast read 2m ago: ${daemon}`)
  })

  test("another failure keeps its own line, and one reason is said once", () => {
    const notices = unreadNotices([
      { key: "containers", label: "Containers", reason: daemon },
      { key: "domains", label: "Domains", reason: "nginx: configuration test failed" },
      { key: "certificates", label: "Certificates", reason: "nginx: configuration test failed" },
      { key: "backups", label: "Backups" },
    ])
    expect(notices.map((notice) => notice.sentence)).toEqual([
      "Docker is not answering: containers not drawn",
      "Domains and certificates not read",
      "Backups not read",
    ])
    expect(notices[1].raw).toBe("nginx: configuration test failed")
    expect(notices[1].href).toBeUndefined()
    expect(notices[2].raw).toBeUndefined()
  })

  test("a kind still being read for the first time is not a failure", () => {
    expect(
      unreadNotices([
        { key: "repositories", label: "Checkouts", reason: "Still being read" },
        { key: "backups", label: "Backups", reason: "Still being read" },
      ]),
    ).toEqual([{ key: "filling", sentence: "Still reading checkouts and backups" }])
  })

  test("nothing unread says nothing", () => {
    expect(unreadNotices([])).toEqual([])
  })
})
