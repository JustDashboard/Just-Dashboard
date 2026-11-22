import { describe, expect, test } from "bun:test"
import {
  GITHUB_LIST_DEFAULTS,
  isNarrowed,
  languagesOf,
  narrowRepositories,
  ownersOf,
  repositoryCounts,
} from "./github-list"

const repo = (fullName, over = {}) => {
  const [login, name] = fullName.split("/")
  return {
    id: fullName.length,
    name,
    fullName,
    owner: { login, type: login === "acme" ? "Organization" : "User" },
    visibility: "public",
    private: false,
    stars: 0,
    forks: 0,
    openIssues: 0,
    archived: false,
    fork: false,
    isTemplate: false,
    defaultBranch: "main",
    htmlUrl: `https://github.com/${fullName}`,
    cloneUrl: `https://github.com/${fullName}.git`,
    sshUrl: `git@github.com:${fullName}.git`,
    installationId: 1,
    role: "admin",
    relation: "owned",
    can: {},
    ...over,
  }
}

const repos = [
  repo("octo/site", { language: "TypeScript", stars: 3, updatedAt: "2026-11-20T10:00:00Z" }),
  repo("acme/api", {
    visibility: "private",
    private: true,
    language: "Go",
    stars: 9,
    description: "Billing API",
    pushedAt: "2026-11-21T10:00:00Z",
    createdAt: "2025-01-01T00:00:00Z",
  }),
  repo("acme/old", { archived: true, language: "Go", createdAt: "2026-01-01T00:00:00Z" }),
  repo("Octo/Notes", { topics: ["writing"], visibility: "internal", private: true }),
]

const narrow = (over) =>
  narrowRepositories(repos, { ...GITHUB_LIST_DEFAULTS, ...over }).map((r) => r.fullName)

describe("narrowing the repository list", () => {
  test("the states count over everything and private includes internal", () => {
    expect(repositoryCounts(repos)).toEqual({ all: 4, public: 2, private: 2, archived: 1 })
    expect(narrow({ state: "private", sort: "name" })).toEqual(["acme/api", "Octo/Notes"])
    expect(narrow({ state: "archived" })).toEqual(["acme/old"])
  })

  test("the search reads names, descriptions, languages and topics", () => {
    expect(narrow({ query: "billing" })).toEqual(["acme/api"])
    expect(narrow({ query: "WRITING" })).toEqual(["Octo/Notes"])
    expect(narrow({ query: "go", sort: "name" })).toEqual(["acme/api", "acme/old"])
  })

  test("owner and language filters, case-insensitive owner", () => {
    expect(narrow({ owner: "OCTO", sort: "name" })).toEqual(["Octo/Notes", "octo/site"])
    expect(narrow({ language: "Go", sort: "name" })).toEqual(["acme/api", "acme/old"])
  })

  test("every order is total and stable by name", () => {
    expect(narrow({ sort: "updated" }).slice(0, 2)).toEqual(["acme/api", "octo/site"])
    expect(narrow({ sort: "stars" })[0]).toBe("acme/api")
    expect(narrow({ sort: "created" })[0]).toBe("acme/old")
    expect(narrow({ sort: "visibility" })).toEqual([
      "acme/old",
      "octo/site",
      "acme/api",
      "Octo/Notes",
    ])
    expect(narrowRepositories(repos, GITHUB_LIST_DEFAULTS)).not.toBe(repos)
  })

  test("owners put the connected account first, languages by use", () => {
    expect(ownersOf(repos, "octo").map((o) => [o.login, o.count])).toEqual([
      ["octo", 2],
      ["acme", 2],
    ])
    expect(languagesOf(repos)).toEqual([
      { name: "Go", count: 2 },
      { name: "TypeScript", count: 1 },
    ])
    expect(isNarrowed(GITHUB_LIST_DEFAULTS)).toBe(false)
    expect(isNarrowed({ ...GITHUB_LIST_DEFAULTS, sort: "name" })).toBe(false)
    expect(isNarrowed({ ...GITHUB_LIST_DEFAULTS, owner: "acme" })).toBe(true)
  })
})
