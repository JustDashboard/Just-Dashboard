import { beforeEach, describe, expect, test } from "bun:test"
import { failureReason, notify } from "./toast"

// Sonner keeps its toasts in a store of its own, so what was asked of it can be
// read back without a toaster on a page. The toaster (`components/ui/sonner`)
// draws the close button; tests/browser holds what a person sees.

// Sonner hands a dismissal to the next frame; there is no frame here, so it runs at once.
globalThis.requestAnimationFrame ??= (run) => {
  run()
  return 0
}
globalThis.cancelAnimationFrame ??= () => {}

/** The toasts on screen: sonner's list without the ones already dismissed. */
function shown() {
  return notify.raw.getToasts()
}

function byId(id) {
  return shown().find((toast) => toast.id === id)
}

beforeEach(() => {
  notify.dismiss()
})

describe("notify.error", () => {
  test("stays until dismissed when it carries a reason", () => {
    const id = notify.error("Could not save", new Error("disk is full"))
    expect(byId(id).duration).toBe(Infinity)
    expect(byId(id).description).toBe("Disk is full.")
  })

  test("stays until dismissed with no reason too", () => {
    const id = notify.error("Not connected")
    expect(byId(id).duration).toBe(Infinity)
    expect(byId(id).description).toBeUndefined()
  })

  test("keeps a duration its caller chose", () => {
    const id = notify.error("Not connected", undefined, { duration: 8_000 })
    expect(byId(id).duration).toBe(8_000)
  })

  test("raised again with the same words, replaces the copy on screen", () => {
    const first = notify.error("Not connected")
    const second = notify.error("Not connected")
    expect(second).not.toBe(first)
    expect(shown().map((toast) => toast.id)).toEqual([second])
  })

  test("a different reason is a different failure, and both stand", () => {
    const one = notify.error("Could not save", new Error("disk is full"))
    const two = notify.error("Could not save", new Error("not found"))
    expect(
      shown()
        .map((toast) => toast.id)
        .sort(),
    ).toEqual([one, two].sort())
  })
})

describe("the passing toasts", () => {
  // A toast stands over the corner's controls while it is up, so a success
  // keeps sonner's own short life; only a failure waits to be read.
  test("leave by themselves, on the toaster's own timer", () => {
    for (const kind of ["success", "info", "warning"]) {
      const id = notify[kind]("Saved")
      expect(byId(id).duration).toBeUndefined()
    }
  })

  test("keep a duration their caller chose", () => {
    const id = notify.warning("Kept the last good config", { duration: 12_000 })
    expect(byId(id).duration).toBe(12_000)
  })
})

test("failureReason says the error's message as a sentence", () => {
  expect(failureReason(new Error("too many requests, slow down"))).toBe(
    "Too many requests, slow down.",
  )
})
