import { describe, expect, test } from "bun:test"
import { serverName, serverNameProblem } from "./server-name"

describe("serverNameProblem", () => {
  test("takes what a kernel takes", () => {
    for (const name of ["atlas", "Atlas-01", " atlas ", "db.example.com", "7", "a".repeat(63)])
      expect(serverNameProblem(name)).toBeUndefined()
  })

  // An empty field is not yet a wrong name; the button is what waits for one.
  test("says nothing of a field still empty", () => {
    expect(serverNameProblem("")).toBeUndefined()
    expect(serverNameProblem("   ")).toBeUndefined()
  })

  test("refuses what the server would, in its words", () => {
    expect(serverNameProblem("my server")).toBe("A name holds only letters, digits and hyphens.")
    expect(serverNameProblem("under_score")).toBe("A name holds only letters, digits and hyphens.")
    expect(serverNameProblem("naïve")).toBe("A name holds only letters, digits and hyphens.")
    expect(serverNameProblem("-lead")).toBe("A name cannot start or end with a hyphen.")
    expect(serverNameProblem("trail-")).toBe("A name cannot start or end with a hyphen.")
    expect(serverNameProblem("a..b")).toBe(
      "A name cannot start or end with a dot, or have two in a row.",
    )
    expect(serverNameProblem(".a")).toBe(
      "A name cannot start or end with a dot, or have two in a row.",
    )
    expect(serverNameProblem("localhost")).toContain("localhost already means this machine")
    expect(serverNameProblem("LOCALHOST.localdomain")).toContain("localhost already means")
    expect(serverNameProblem("a".repeat(65))).toBe("A server's name is at most 64 characters.")
    expect(serverNameProblem(`${"a".repeat(64)}`)).toBe(
      "Each part of a name between dots is at most 63 characters.",
    )
  })
})

test("serverName is the name as the server keeps it", () => {
  expect(serverName("  Atlas-01 ")).toBe("atlas-01")
})
