import { describe, expect, test } from "bun:test"
import {
  githubCallbackRegistered,
  githubCallbackUrl,
  githubRemoteKey,
  githubRepoApi,
  githubRepoHref,
  localCheckoutsOf,
  localGitHref,
} from "./github"

describe("a remote as a GitHub repository", () => {
  test("every way of writing one github.com remote is one key", () => {
    for (const remote of [
      "https://github.com/Acme/API.git",
      "https://***@github.com/acme/api",
      "git@github.com:acme/api.git",
      "ssh://git@github.com/acme/api.git",
      "ssh://git@ssh.github.com:443/acme/api.git",
      "https://www.github.com/acme/api/",
    ]) {
      expect(githubRemoteKey(remote)).toBe("acme/api")
    }
  })

  test("another forge, a path or a remote without an owner is not GitHub", () => {
    for (const remote of [
      undefined,
      "",
      "https://gitlab.com/acme/api.git",
      "/srv/mirrors/api.git",
      "https://github.com/api.git",
      "https://github.com.evil.example/acme/api.git",
      "https://gitlab.com/group/sub/api.git",
    ]) {
      expect(githubRemoteKey(remote)).toBeUndefined()
    }
  })

  test("checkouts are matched by remote, not by folder name, mains first", () => {
    const repo = (path, remote, worktree) => ({
      path,
      name: path.split("/").pop(),
      remote,
      worktree,
    })
    const repos = [
      repo("/srv/api", "https://github.com/other/api.git"),
      repo("/srv/work/.worktrees/x", "git@github.com:acme/api.git", true),
      repo("/srv/acme-api", "git@github.com:Acme/Api.git"),
      repo("/srv/web", "https://github.com/acme/web.git"),
    ]
    expect(localCheckoutsOf(repos, "acme/api").map((r) => r.path)).toEqual([
      "/srv/acme-api",
      "/srv/work/.worktrees/x",
    ])
    expect(localCheckoutsOf(undefined, "acme/api")).toEqual([])
  })
})

describe("addresses", () => {
  test("the API path escapes owner and name once", () => {
    expect(githubRepoApi(3, "acme/my.repo", "/branches")).toBe(
      "/github/connections/3/repos/acme/my.repo/branches",
    )
    expect(githubRepoApi(3, "acme/a b")).toBe("/github/connections/3/repos/acme/a%20b")
    expect(() => githubRepoApi(3, "acme")).toThrow()
  })

  test("the page keeps the first view and no account as the bare address", () => {
    expect(githubRepoHref("acme/api")).toBe("/github/acme/api")
    expect(githubRepoHref("acme/api", { account: 2, tab: "overview" })).toBe(
      "/github/acme/api?account=2",
    )
    expect(githubRepoHref("acme/api", { account: 2, tab: "pulls" })).toBe(
      "/github/acme/api?account=2&tab=pulls",
    )
    expect(localGitHref("/srv/a b")).toBe("/git?repo=%2Fsrv%2Fa%20b")
  })

  test("a callback is registered only for the exact address", () => {
    const setup = {
      configured: true,
      app: { callbackUrls: ["https://dash.example.com/github/callback"] },
    }
    expect(githubCallbackUrl("https://dash.example.com/")).toBe(
      "https://dash.example.com/github/callback",
    )
    expect(githubCallbackRegistered(setup, "https://dash.example.com")).toBe(true)
    expect(githubCallbackRegistered(setup, "http://10.0.0.5:8443")).toBe(false)
    expect(githubCallbackRegistered(undefined, "https://dash.example.com")).toBe(false)
  })
})
