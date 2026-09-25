import { describe, expect, test } from "bun:test"
import { assignPulls, parseRemote, shelve } from "./git-repos"

const repo = (over) => ({
  path: "/srv/app",
  name: "app",
  branch: "main",
  dirty: false,
  changes: 0,
  staged: 0,
  untracked: 0,
  conflicts: 0,
  ahead: 0,
  behind: 0,
  detached: false,
  ...over,
})

// The one string a remote is, in every way people write it: the shelf a
// checkout lands on must not depend on whether it was cloned over https or
// ssh, or whether a token was pasted into the URL and scrubbed.
describe("reading a remote", () => {
  test("https, with and without .git and scrubbed credentials", () => {
    expect(parseRemote("https://github.com/Wayy01/api.git")).toEqual({
      host: "github.com",
      owner: "Wayy01",
      name: "api",
    })
    expect(parseRemote("https://github.com/Wayy01/api")).toEqual({
      host: "github.com",
      owner: "Wayy01",
      name: "api",
    })
    expect(parseRemote("https://***@github.com/Wayy01/api.git")?.owner).toBe("Wayy01")
  })

  test("ssh URLs, with a port, and the scp shape", () => {
    expect(parseRemote("ssh://git@gitlab.example.com:2222/group/sub/app.git")).toEqual({
      host: "gitlab.example.com",
      owner: "group/sub",
      name: "app",
    })
    expect(parseRemote("git@github.com:Wayy01/api.git")).toEqual({
      host: "github.com",
      owner: "Wayy01",
      name: "api",
    })
  })

  test("a path on this host is its directory's", () => {
    expect(parseRemote("/srv/git/mirror.git")).toEqual({
      host: "",
      owner: "/srv/git",
      name: "mirror",
    })
    expect(parseRemote("file:///srv/git/mirror.git")?.owner).toBe("/srv/git")
    expect(parseRemote("")).toBeUndefined()
    expect(parseRemote(undefined)).toBeUndefined()
  })
})

describe("shelving checkouts", () => {
  test("one shelf per account, the account with something wrong first, no remote last", () => {
    const shelves = shelve([
      repo({ path: "/srv/lib", name: "lib", remote: "git@github.com:acme/lib.git" }),
      repo({ path: "/srv/site", name: "site", remote: "https://github.com/wayy01/site.git" }),
      repo({ path: "/srv/local", name: "local" }),
      repo({ path: "/srv/app", name: "app", remote: "https://github.com/acme/app.git", dirty: true }),
    ])
    expect(shelves.map((s) => [s.label, s.repos.map((r) => r.name)])).toEqual([
      ["acme", ["lib", "app"]],
      ["wayy01", ["site"]],
      ["No remote", ["local"]],
    ])
    expect(shelves[0].host).toBe("github.com")
  })

  test("a remote naming only a repository is shelved under its host", () => {
    const [shelf] = shelve([repo({ remote: "https://git.example.com/app.git" })])
    expect(shelf.label).toBe("git.example.com")
  })
})

// gh answers per repository, and a clone with a worktree beside it is asked
// twice: the list must not say one request twice.
describe("assigning pull requests to cards", () => {
  const pull = (number, head) => ({
    number,
    title: `#${number}`,
    url: "",
    state: "open",
    draft: false,
    head,
    base: "main",
    comments: 0,
  })
  const pulls = [pull(1, "feature/one"), pull(2, "docs/two")]

  test("each request is drawn once: on the checkout on its branch, else the first by path", () => {
    const clone = repo({ path: "/srv/app", branch: "main" })
    const worktree = repo({ path: "/srv/app-docs", branch: "docs/two" })
    const drawn = assignPulls([worktree, clone], [
      { path: "/srv/app", repository: "acme/app", pulls, deployments: [] },
      { path: "/srv/app-docs", repository: "acme/app", pulls, deployments: [] },
    ])
    expect(drawn["/srv/app"].pulls.map((p) => p.number)).toEqual([1])
    expect(drawn["/srv/app-docs"].pulls.map((p) => p.number)).toEqual([2])
  })

  test("a detached checkout on the branch's commit does not claim it", () => {
    const clone = repo({ path: "/srv/app", branch: "main" })
    const pinned = repo({ path: "/srv/app-pin", branch: "feature/one", detached: true })
    const drawn = assignPulls([clone, pinned], [
      { path: "/srv/app", repository: "acme/app", pulls, deployments: [] },
      { path: "/srv/app-pin", repository: "acme/app", pulls, deployments: [] },
    ])
    expect(drawn["/srv/app"].pulls.map((p) => p.number)).toEqual([1, 2])
    expect(drawn["/srv/app-pin"].pulls).toEqual([])
  })

  test("a fork is another repository and keeps its own", () => {
    const upstream = repo({ path: "/srv/app" })
    const fork = repo({ path: "/srv/fork" })
    const drawn = assignPulls([upstream, fork], [
      { path: "/srv/app", repository: "acme/app", pulls, deployments: [] },
      { path: "/srv/fork", repository: "wayy01/app", pulls: [pull(9, "x")], deployments: [] },
    ])
    expect(drawn["/srv/app"].pulls).toHaveLength(2)
    expect(drawn["/srv/fork"].pulls.map((p) => p.number)).toEqual([9])
  })

  test("what gh said about a checkout stays with it", () => {
    const drawn = assignPulls([repo({})], [
      { path: "/srv/app", repository: "acme/app", pulls: [], deployments: [], error: "not signed in" },
    ])
    expect(drawn["/srv/app"].error).toBe("not signed in")
    expect(assignPulls([repo({})], undefined)).toEqual({})
  })
})
