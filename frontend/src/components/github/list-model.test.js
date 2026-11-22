import { describe, expect, test } from "bun:test"
import { ApiError } from "@/lib/api"
import {
  accountFacts,
  cloneFolderProblem,
  githubRepoName,
  listFailure,
  loginProblem,
  permissionWords,
  pickConnection,
  rowTags,
} from "./list-model"

describe("githubRepoName", () => {
  test("keeps GitHub's characters and turns every other run into one dash", () => {
    expect(githubRepoName("  my new  repo!! ")).toEqual({ name: "my-new-repo-" })
    expect(githubRepoName("api.v2_final-1")).toEqual({ name: "api.v2_final-1" })
  })
  test("refuses what GitHub would refuse", () => {
    expect(githubRepoName("").problem).toBe("Name the repository.")
    expect(githubRepoName("x".repeat(101)).problem).toMatch(/100/)
    expect(githubRepoName("..").problem).toMatch(/does not allow/)
    expect(githubRepoName("!!!").problem).toMatch(/letter or digit/)
    expect(githubRepoName("x".repeat(100)).problem).toBeUndefined()
  })
})

describe("cloneFolderProblem", () => {
  test("is the server's one-segment rule", () => {
    expect(cloneFolderProblem("api")).toBeUndefined()
    expect(cloneFolderProblem("api@2")).toBeUndefined()
    expect(cloneFolderProblem("")).toMatch(/Name/)
    expect(cloneFolderProblem("a/b")).toMatch(/slash/)
    expect(cloneFolderProblem(".hidden")).toMatch(/hidden/)
    expect(cloneFolderProblem("-rf")).toMatch(/dash/)
    expect(cloneFolderProblem("my repo")).toMatch(/only/)
  })
})

describe("accountFacts", () => {
  test("says the counts in words, leaving out what is none", () => {
    expect(accountFacts("mira", { all: 9, public: 4, private: 5, archived: 0 })).toEqual([
      "@mira",
      "9 repositories",
      "5 private",
    ])
    expect(accountFacts("mira", { all: 1, public: 1, private: 0, archived: 1 })).toEqual([
      "@mira",
      "1 repository",
      "1 archived",
    ])
    expect(accountFacts("mira", undefined)).toEqual(["@mira"])
  })
})

describe("pickConnection", () => {
  const list = [
    { id: 2, status: "expired" },
    { id: 1, status: "active" },
  ]
  test("takes the address's account when it is one of these", () => {
    expect(pickConnection(list, "2")?.id).toBe(2)
  })
  test("falls back to the first active, then the first", () => {
    expect(pickConnection(list, null)?.id).toBe(1)
    expect(pickConnection(list, "99")?.id).toBe(1)
    expect(pickConnection([{ id: 3, status: "revoked" }], null)?.id).toBe(3)
    expect(pickConnection([], null)).toBeUndefined()
  })
})

describe("listFailure", () => {
  test("tells a spent rate limit, a reconnect and a failure apart", () => {
    const rate = new ApiError(429, "github_rate_limited", "spent until 14:05 UTC", undefined, {
      retryAfterMs: 60_000,
    })
    expect(listFailure(rate, 1_000)).toEqual({
      kind: "rate",
      message: "spent until 14:05 UTC",
      until: new Date(61_000),
    })
    expect(listFailure(new ApiError(409, "github_reconnect", "again"))).toEqual({
      kind: "reconnect",
      message: "again",
    })
    expect(listFailure(new Error("boom"))).toEqual({ kind: "failed" })
  })
})

describe("rowTags", () => {
  const base = { archived: false, fork: false, isTemplate: false, relation: "owned", role: "admin" }
  test("says the kind and a role below maintain", () => {
    expect(rowTags(base)).toEqual([])
    expect(rowTags({ ...base, archived: true, fork: true, role: "maintain" })).toEqual([
      "Archived",
      "Fork",
    ])
    expect(rowTags({ ...base, isTemplate: true, relation: "collaborator", role: "read" })).toEqual(
      ["Template", "Shared with you", "Read only"],
    )
  })
})

test("permissionWords puts GitHub's grant in sentences, unknown ones plainly", () => {
  expect(permissionWords({ contents: "write", metadata: "read", secret_scanning: "read" })).toEqual(
    [
      "See the repositories it is installed on",
      "Read and push code, create and delete branches",
      "secret scanning: read",
    ],
  )
})

test("loginProblem holds GitHub's login rule", () => {
  expect(loginProblem("northwind")).toBeUndefined()
  expect(loginProblem("acme-labs")).toBeUndefined()
  expect(loginProblem("-acme")).toBeDefined()
  expect(loginProblem("acme--labs")).toBeDefined()
  expect(loginProblem("")).toBeDefined()
})
