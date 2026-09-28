import { describe, expect, test } from "bun:test"
import { runActorProduct } from "./run-marks"

const run = (trigger, actor = "", metadata = {}) => ({ trigger, actor, metadata })

describe("runActorProduct", () => {
  test("a push is drawn as the host it was pushed to, and as git when the host says nothing", () => {
    expect(runActorProduct(run("git_push", "git-monitor"))).toBe("git")
    expect(runActorProduct(run("git_push", "git-monitor"), "git@github.com:acme/api.git")).toBe(
      "github",
    )
    expect(
      runActorProduct(run("git_push", "git-monitor"), "https://git.example.net/acme/api"),
    ).toBe("git")
  })

  test("a forge's delivery is the forge, and so is a preview it pushed", () => {
    expect(runActorProduct(run("gitlab", "webhook"))).toBe("gitlab")
    expect(runActorProduct(run("preview", "webhook"), "https://github.com/acme/api")).toBe("github")
    // A registry is a host too, but no forge sends a pull request from one.
    expect(runActorProduct(run("preview", "webhook"), "https://quay.io/acme/api")).toBeUndefined()
    expect(runActorProduct(run("preview", "webhook"))).toBeUndefined()
  })

  test("a plain webhook is the webhook's own mark", () => {
    expect(runActorProduct(run("generic_hook", "webhook"))).toBe("webhook")
  })

  test("a person, a schedule and an API call have no product", () => {
    expect(runActorProduct(run("manual", "operator"))).toBeUndefined()
    expect(runActorProduct(run("preview", "ion"), "https://github.com/acme/api")).toBeUndefined()
    expect(
      runActorProduct(run("schedule", "scheduler:4", { scheduleName: "nightly" })),
    ).toBeUndefined()
    expect(runActorProduct(run("api", "webhook"))).toBeUndefined()
  })
})
