import { describe, expect, test } from "bun:test"
import { ApiError } from "@/lib/api"
import {
  abilityList,
  branchProblem,
  changeCount,
  draftOf,
  grantWords,
  nameProblem,
  parseTopics,
  reasonFor,
  repoFailure,
  runState,
  runVerbs,
  settingsChanges,
  topicProblem,
} from "./model"

const detail = (over = {}) => ({
  id: 1,
  name: "api",
  fullName: "northwind/api",
  owner: { login: "northwind", type: "Organization" },
  description: "Orders",
  visibility: "private",
  private: true,
  stars: 0,
  forks: 0,
  openIssues: 0,
  archived: false,
  fork: false,
  isTemplate: false,
  defaultBranch: "main",
  htmlUrl: "https://github.com/northwind/api",
  cloneUrl: "https://github.com/northwind/api.git",
  sshUrl: "git@github.com:northwind/api.git",
  topics: ["billing"],
  installationId: 12,
  role: "admin",
  relation: "organization",
  can: {},
  settings: { hasIssues: true, hasWiki: false, hasProjects: false, hasDiscussions: false },
  installation: {},
  ...over,
})

describe("settingsChanges", () => {
  test("an untouched draft sends nothing", () => {
    const repo = detail()
    expect(settingsChanges(repo, draftOf(repo))).toEqual({})
  })

  test("only the changed fields are sent", () => {
    const repo = detail()
    const draft = { ...draftOf(repo), description: "Orders and billing", hasWiki: true }
    expect(settingsChanges(repo, draft)).toEqual({
      description: "Orders and billing",
      hasWiki: true,
    })
    expect(changeCount(settingsChanges(repo, draft))).toBe(2)
  })

  test("a setting GitHub did not state is never sent", () => {
    const repo = detail()
    const draft = { ...draftOf(repo), allowSquashMerge: false }
    expect("allowSquashMerge" in draftOf(repo)).toBe(false)
    expect(settingsChanges(repo, draft)).toEqual({})
  })

  test("a rename, a topic list and a default branch", () => {
    const repo = detail()
    const draft = {
      ...draftOf(repo),
      name: " api-v2 ",
      topics: ["billing", "grpc"],
      defaultBranch: "trunk",
    }
    expect(settingsChanges(repo, draft)).toEqual({
      name: "api-v2",
      topics: ["billing", "grpc"],
      defaultBranch: "trunk",
    })
  })

  test("whitespace typed around a value is not a change", () => {
    const repo = detail()
    expect(settingsChanges(repo, { ...draftOf(repo), description: "Orders  " })).toEqual({})
  })
})

describe("validation", () => {
  test("repository names", () => {
    expect(nameProblem("api")).toBeUndefined()
    expect(nameProblem("")).toBeDefined()
    expect(nameProblem("has space")).toBeDefined()
    expect(nameProblem("..")).toBeDefined()
  })

  test("topics", () => {
    expect(parseTopics("Billing, grpc  grpc")).toEqual(["billing", "grpc"])
    expect(topicProblem("grpc")).toBeUndefined()
    expect(topicProblem("-x")).toBeDefined()
  })

  test("branch names", () => {
    expect(branchProblem("feature/login")).toBeUndefined()
    expect(branchProblem("a..b")).toBeDefined()
    expect(branchProblem("with space")).toBeDefined()
    expect(branchProblem("x.lock")).toBeDefined()
    expect(branchProblem("")).toBeDefined()
  })
})

describe("runs", () => {
  test("a run's word comes from its status while it runs, its conclusion after", () => {
    expect(runState({ status: "in_progress" })).toEqual({
      word: "running",
      tone: "notice",
      live: true,
    })
    expect(runState({ status: "queued" }).word).toBe("queued")
    expect(runState({ status: "completed", conclusion: "success" }).word).toBe("succeeded")
    expect(runState({ status: "completed", conclusion: "failure" }).tone).toBe("danger")
    expect(runState({ status: "completed", conclusion: "cancelled" }).word).toBe("cancelled")
  })

  test("re-run once finished, cancel while waiting or running", () => {
    expect(runVerbs({ status: "completed", conclusion: "success" })).toEqual({
      rerun: true,
      rerunFailed: false,
      cancel: false,
    })
    expect(runVerbs({ status: "completed", conclusion: "failure" }).rerunFailed).toBe(true)
    expect(runVerbs({ status: "in_progress" })).toEqual({
      rerun: false,
      rerunFailed: false,
      cancel: true,
    })
  })
})

describe("abilities", () => {
  const can = {
    administer: false,
    delete: false,
    push: true,
    merge: true,
    pulls: true,
    issues: true,
    createIssues: true,
    actions: false,
    reasons: { administer: "Your role is write; this needs admin." },
  }

  test("a false ability has its reason, a true one none", () => {
    expect(reasonFor(can, "administer")).toBe("Your role is write; this needs admin.")
    expect(reasonFor(can, "push")).toBeUndefined()
    expect(reasonFor(can, "actions")).toBeDefined()
  })

  test("the list puts what you can do first", () => {
    const list = abilityList(can)
    expect(list[0].allowed).toBe(true)
    expect(list.at(-1).allowed).toBe(false)
  })

  test("the grant in words, metadata left out", () => {
    expect(
      grantWords({ administration: "read", contents: "write", issues: "write", metadata: "read" }),
    ).toBe("change code and issues · read settings")
  })
})

describe("repoFailure", () => {
  test("tells the page's three answers apart", () => {
    expect(repoFailure(new ApiError(404, "github_not_found", "gone")).kind).toBe("missing")
    expect(repoFailure(new ApiError(409, "github_reconnect", "again")).kind).toBe("reconnect")
    const rate = repoFailure(
      new ApiError(429, "github_rate_limited", "spent", undefined, { retryAfterMs: 60_000 }),
    )
    expect(rate.kind).toBe("rate")
    expect(rate.until).toBeGreaterThan(Date.now())
    expect(repoFailure(new Error("x")).kind).toBe("other")
  })
})
