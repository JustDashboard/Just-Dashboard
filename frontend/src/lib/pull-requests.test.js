import { describe, expect, test } from "bun:test"
import {
  canTest,
  cleanupFailed,
  heldReason,
  mergeDetail,
  previewHeld,
  previewOutOfDate,
  previewStatus,
  pullChecksTone,
  pullOfPreview,
  unavailableReason,
} from "./pull-requests"

// Three surfaces read these words, so the order the facts are weighed in has
// to be the same everywhere: a removal outranks a build, a moved head outranks
// "Ready", and "Ready" means reachable rather than merely built.

const sha = "a".repeat(40)
const moved = "b".repeat(40)

const preview = (over = {}) => ({
  id: 1,
  triggerId: 2,
  providerRef: "7",
  environmentId: 9,
  environmentSlug: "pr-7",
  state: "open",
  updatedAt: "2026-09-22T12:00:00Z",
  number: 7,
  revision: sha,
  headRevision: sha,
  ...over,
})

const run = (state, operation = "preview_create") => ({
  id: 5,
  runNumber: 1,
  state,
  operation,
  requestedAt: "2026-09-22T12:00:00Z",
})

describe("the dot for a pull request's checks", () => {
  test("gh's three words map onto the three tones", () => {
    expect(pullChecksTone({ checks: "success" })).toBe("running")
    expect(pullChecksTone({ checks: "failure" })).toBe("danger")
    expect(pullChecksTone({ checks: "pending" })).toBe("warning")
  })
  test("no checks is not a verdict", () => {
    expect(pullChecksTone({})).toBe("unknown")
    expect(pullChecksTone({ checks: "" })).toBe("unknown")
  })
})

describe("whether a preview is out of date", () => {
  test("a head that moved past the approved revision", () => {
    expect(previewOutOfDate(preview({ headRevision: moved }))).toBe(true)
  })
  test("not without both revisions, and never once closed", () => {
    expect(previewOutOfDate(preview({ headRevision: undefined }))).toBe(false)
    expect(previewOutOfDate(preview({ revision: undefined, headRevision: moved }))).toBe(false)
    expect(previewOutOfDate(preview({ state: "closed", headRevision: moved }))).toBe(false)
    expect(previewOutOfDate(undefined)).toBe(false)
  })
})

describe("what a preview says about itself", () => {
  test("nothing for no preview", () => {
    expect(previewStatus(undefined)).toBeUndefined()
    expect(previewStatus(null)).toBeUndefined()
  })
  test("ready only when the live release is reachable", () => {
    expect(
      previewStatus(preview({ liveReleaseId: 3, address: { kind: "tailnet", published: true } })),
    ).toEqual({ tone: "running", label: "Ready" })
    expect(
      previewStatus(preview({ liveReleaseId: 3, address: { kind: "tailnet", published: false } })),
    ).toEqual({ tone: "warning", label: "Not reachable" })
    expect(previewStatus(preview({ liveReleaseId: 3 }))).toEqual({
      tone: "warning",
      label: "Not reachable",
    })
  })
  test("a build in flight, and a build that failed", () => {
    expect(previewStatus(preview({ lastRun: run("running") }))).toEqual({
      tone: "notice",
      label: "Building",
    })
    expect(previewStatus(preview({ lastRun: run("failed") }))).toEqual({
      tone: "danger",
      label: "Failed",
    })
  })
  test("a moved head outranks a reachable release", () => {
    expect(
      previewStatus(
        preview({
          headRevision: moved,
          liveReleaseId: 3,
          address: { kind: "tailnet", published: true },
        }),
      ),
    ).toEqual({ tone: "warning", label: "Out of date" })
  })
  test("a removal is said before anything else", () => {
    expect(
      previewStatus(preview({ state: "closed", lastRun: run("running", "preview_remove") })),
    ).toEqual({ tone: "warning", label: "Closing" })
    expect(
      previewStatus(preview({ state: "closed", lastRun: run("failed", "preview_remove") })),
    ).toEqual({ tone: "danger", label: "Cleanup failed" })
    expect(
      previewStatus(preview({ state: "closed", lastRun: run("succeeded", "preview_remove") })),
    ).toEqual({ tone: "stopped", label: "Closed" })
  })
  test("a removal that ended short of success is a failed cleanup, not a closed preview", () => {
    // The route refuses to build over a cancelled or superseded removal just
    // as it refuses a failed one, so the word has to hold the verb the same
    // way: reading "Closed" here offered a test the server would refuse.
    for (const state of ["cancelled", "superseded", "rolled_back"]) {
      expect(
        previewStatus(preview({ state: "closed", lastRun: run(state, "preview_remove") })),
      ).toEqual({ tone: "danger", label: "Cleanup failed" })
    }
  })
  test("approved and not yet built", () => {
    expect(previewStatus(preview())).toEqual({ tone: "notice", label: "Pending" })
  })
})

describe("whether a pull request can be tested", () => {
  test("only an open one with an exact head", () => {
    expect(canTest({ state: "open", headSha: sha })).toBe(true)
    expect(canTest({ state: "open" })).toBe(false)
    expect(canTest({ state: "merged", headSha: sha })).toBe(false)
  })
})

describe("whether a preview holds the test verb back", () => {
  const ready = preview({ liveReleaseId: 3, address: { kind: "tailnet", published: true } })
  const building = preview({ lastRun: run("running") })
  const closing = preview({ state: "closed", lastRun: run("running", "preview_remove") })
  const failedCleanup = preview({ state: "closed", lastRun: run("cancelled", "preview_remove") })

  test("ready at this commit, building, closing and a failed cleanup all hold it", () => {
    expect(previewHeld(ready)).toBe(true)
    expect(previewHeld(building)).toBe(true)
    expect(previewHeld(closing)).toBe(true)
    expect(previewHeld(failedCleanup)).toBe(true)
  })
  test("every other reading is one press from a fresh build", () => {
    expect(previewHeld(undefined)).toBe(false)
    expect(previewHeld(null)).toBe(false)
    expect(previewHeld(preview())).toBe(false)
    expect(previewHeld(preview({ lastRun: run("failed") }))).toBe(false)
    expect(previewHeld(preview({ liveReleaseId: 3 }))).toBe(false)
    expect(previewHeld(preview({ headRevision: moved, liveReleaseId: 3 }))).toBe(false)
    expect(
      previewHeld(preview({ state: "closed", lastRun: run("succeeded", "preview_remove") })),
    ).toBe(false)
  })
  test("each held reading has the sentence the dialog says instead", () => {
    expect(heldReason(ready)).toBe("This pull request already has a preview at this commit.")
    expect(heldReason(building)).toMatch(/already building/)
    expect(heldReason(closing)).toMatch(/still closing/)
    expect(heldReason(failedCleanup)).toMatch(/Retry the cleanup/)
    expect(heldReason(preview())).toBeUndefined()
  })
  test("only the failed cleanup is the thing to retry", () => {
    expect(cleanupFailed(failedCleanup)).toBe(true)
    expect(cleanupFailed(closing)).toBe(false)
    expect(cleanupFailed(preview({ lastRun: run("failed") }))).toBe(false)
    expect(cleanupFailed(undefined)).toBe(false)
  })
})

describe("why a listing could not be read", () => {
  test("the App is the one fix when the CLI is missing too", () => {
    // The server says app_not_installed only when there is no CLI at all,
    // so sending the reader to sign in to it is advice nobody can follow.
    const sentence = unavailableReason({ reason: "app_not_installed", repository: "acme/app" })
    expect(sentence).toBe(
      "Install the GitHub App on acme/app to read its pull requests; the GitHub CLI is not installed on this host.",
    )
    expect(sentence).not.toMatch(/sign in/i)
  })
  test("the other reasons name their own fix", () => {
    expect(unavailableReason({ reason: "sign_in_required", repository: "acme/app" })).toMatch(
      /Sign in to GitHub on the Git page/,
    )
    expect(unavailableReason({ reason: "not_installed", repository: "acme/app" })).toMatch(
      /GitHub CLI is not installed/,
    )
    expect(unavailableReason({ reason: "not_github", repository: "" })).toMatch(
      /not a GitHub repository/,
    )
    expect(unavailableReason({ reason: "", repository: "acme/app" })).toMatch(/could not be read/)
  })
})

describe("what the Merge verb promises", () => {
  test("a preview closes only when there is one, and production only redeploys when it would", () => {
    expect(mergeDetail({ draft: false, previewOpen: true, automatic: true })).toBe(
      "Merge it into its base branch on GitHub. Its preview closes. Production redeploys automatically.",
    )
    expect(mergeDetail({ draft: false, previewOpen: false, automatic: true })).toBe(
      "Merge it into its base branch on GitHub. Production redeploys automatically.",
    )
    expect(mergeDetail({ draft: false, previewOpen: true, automatic: false })).toBe(
      "Merge it into its base branch on GitHub. Its preview closes. Auto-deploy is off — deploy production afterwards.",
    )
    expect(mergeDetail({ draft: false, previewOpen: false, automatic: false })).toBe(
      "Merge it into its base branch on GitHub. Auto-deploy is off — deploy production afterwards.",
    )
  })
  test("a draft cannot be merged at all", () => {
    expect(mergeDetail({ draft: true, previewOpen: true, automatic: true })).toBe(
      "A draft cannot be merged until it is marked ready on GitHub.",
    )
  })
})

describe("a preview's pull request as the row draws one", () => {
  const recorded = preview({
    title: "Add caching",
    headRef: "feature",
    headRepository: "Mallory/App",
    author: "mallory",
    revision: sha,
  })
  test("named by what the dashboard recorded, with its page and fork reading from the listing", () => {
    const pull = pullOfPreview(recorded, {
      available: true,
      host: "github.com",
      repository: "acme/app",
    })
    expect(pull).toMatchObject({
      number: 7,
      title: "Add caching",
      url: "https://github.com/acme/app/pull/7",
      state: "closed",
      head: "feature",
      author: "mallory",
      headSha: sha,
      fork: true,
    })
    expect(
      pullOfPreview(recorded, { available: false, host: "", repository: "acme/app" }),
    ).toMatchObject({ url: "https://github.com/acme/app/pull/7", state: "unknown" })
    expect(
      pullOfPreview(recorded, { available: true, host: "", repository: "mallory/app" }).fork,
    ).toBe(false)
  })
  test("a listing that names no repository has no page to open and calls nothing a fork", () => {
    const pull = pullOfPreview(recorded, { available: false, host: "", repository: "" })
    expect(pull.url).toBe("")
    expect(pull.fork).toBe(false)
  })
  test("a preview recorded without a title is named by its number", () => {
    expect(
      pullOfPreview(preview(), { available: true, host: "github.com", repository: "acme/app" })
        .title,
    ).toBe("Pull request #7")
  })
})
