import { describe, expect, test } from "bun:test"
import { commitHeadline, commitLogin, noreplyLogin } from "./commit-faces"

describe("the account behind a commit", () => {
  test("GitHub's private addresses carry the login", () => {
    expect(noreplyLogin("12345+Wayy01@users.noreply.github.com")).toBe("Wayy01")
    expect(noreplyLogin("Wayy01@users.noreply.github.com")).toBe("Wayy01")
    expect(noreplyLogin("me@example.com")).toBeUndefined()
    expect(noreplyLogin(undefined)).toBeUndefined()
  })

  test("GitHub's answer is read whatever the case of the commit's address", () => {
    expect(commitLogin({ "me@example.com": "Wayy01" }, "Me@Example.com")).toBe("Wayy01")
    expect(commitLogin({}, "me@example.com")).toBeUndefined()
    expect(commitLogin(undefined, "1+ada@users.noreply.github.com")).toBe("ada")
  })
})

describe("a commit's headline", () => {
  test("a merged pull request says which and from where", () => {
    expect(commitHeadline("Merge pull request #409 from JustDashboard/patch/0.8.4")).toBe(
      "Merged #409 from patch/0.8.4",
    )
  })

  test("anything else is the author's own words", () => {
    expect(commitHeadline("Release 0.8.4")).toBe("Release 0.8.4")
    expect(commitHeadline("Merge branch 'main'")).toBe("Merge branch 'main'")
  })
})
