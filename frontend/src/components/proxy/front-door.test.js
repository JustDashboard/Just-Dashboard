import { describe, expect, test } from "bun:test"
import { frontDoorLine, frontDoors } from "./front-door"

const listener = (port, address, process) => ({ protocol: "tcp", port, address, process })

describe("frontDoorLine", () => {
  test("a port the engine itself holds is open, and a closed one says so", () => {
    const doors = frontDoors([listener(80, "0.0.0.0", "nginx"), listener(80, "::", "nginx")])
    expect(frontDoorLine(doors, "nginx")).toBe(":80 open · :443 closed")
  })

  test("a port something else holds names it, with or without the engine beside it", () => {
    const doors = frontDoors([
      listener(80, "0.0.0.0", "nginx"),
      listener(443, "0.0.0.0", "nginx"),
      listener(443, "::", "docker-proxy"),
    ])
    expect(frontDoorLine(doors, "nginx")).toBe(":80 open · :443 nginx + docker-proxy")
  })

  test("without an engine named every holder is named", () => {
    const doors = frontDoors([listener(80, "0.0.0.0", "nginx"), listener(443, "127.0.0.1", "x")])
    expect(frontDoorLine(doors)).toBe(":80 nginx · :443 closed")
  })
})
