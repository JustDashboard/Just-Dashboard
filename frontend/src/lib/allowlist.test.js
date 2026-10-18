import { expect, test } from "bun:test"
import { coversLoopback } from "./allowlist"

test("only IPv4 prefixes containing 127.0.0.1 protect its final allowlist entry", () => {
  for (const entry of ["127.0.0.1", "127.0.0.1/32", "127.0.0.0/8", "126.0.0.0/7", "0.0.0.0/0"])
    expect(coversLoopback(entry)).toBe(true)
  for (const entry of [
    "127.2.0.0/16",
    "127.0.0.2/32",
    "::/0",
    "::1",
    "128.0.0.0/1",
    "127.0.0.1/33",
    "127.300.0.1/8",
    "127.0.0.1/",
    "127.0.0.1/8/0",
  ])
    expect(coversLoopback(entry)).toBe(false)
})
