import { expect, test } from "bun:test"
import { appliesLine, namesLine, pendingLine } from "./save-bar-words"

test("one kind of save is said whole", () => {
  expect(appliesLine(["immediately"])).toBe("Applies immediately")
  expect(appliesLine(["next-deployment", "next-deployment"])).toBe(
    "Applies on your next deployment",
  )
  expect(appliesLine(["next-reload"])).toBe("Applies when nginx next reloads")
  expect(appliesLine(["restart"])).toBe("Applies when the server restarts")
})

test("several kinds are split at the soonest, whichever they are", () => {
  expect(appliesLine(["next-deployment", "immediately"])).toBe(
    "Some apply now, the rest on your next deployment",
  )
  // The line used to say "the rest on your next deployment" for any mix.
  expect(appliesLine(["immediately", "restart"])).toBe(
    "Some apply now, the rest when the server restarts",
  )
  expect(appliesLine(["next-reload", "immediately"])).toBe(
    "Some apply now, the rest when nginx next reloads",
  )
  expect(appliesLine(["next-deployment", "restart"])).toBe(
    "Some apply when the server restarts, the rest on your next deployment",
  )
  expect(appliesLine(["next-deployment", "restart", "immediately"])).toBe(
    "Some apply now, the rest when the server restarts or on your next deployment",
  )
})

test("the parts are named once each", () => {
  expect(namesLine([])).toBe("")
  expect(namesLine(["General"])).toBe("General")
  expect(namesLine(["General", "Build", "General", "Runtime"])).toBe("General, Build and Runtime")
})

test("a value that cannot be saved is said before a form's own note", () => {
  const entries = [{ name: "Listener", applies: "immediately", note: "Restarts the dashboard" }]
  expect(pendingLine(entries, false)).toBe("Listener · Restarts the dashboard")
  expect(pendingLine(entries, true)).toBe("A value is out of range — fix it to save")
  expect(pendingLine([{ name: "Resolver", applies: "immediately" }], false)).toBe(
    "Resolver · Applies immediately",
  )
})
