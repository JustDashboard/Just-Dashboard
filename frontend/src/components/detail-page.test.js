import { describe, expect, test } from "bun:test"
import { siblingSteps, stepCommands } from "./detail-page"

// The keys a detail page walks its rail with. The Workspace runs the command
// whose chord matches the press (`workspace/workspace.tsx`), spelled the way
// it spells a KeyboardEvent: modifiers, then the key.
const chordOf = (event) =>
  `${event.ctrlKey ? "Ctrl+" : ""}${event.metaKey ? "Meta+" : ""}${event.altKey ? "Alt+" : ""}${event.shiftKey ? "Shift+" : ""}${event.key}`

const press = (commands, key) =>
  commands
    .find((command) => command.chord === chordOf({ altKey: true, key }) && !command.disabled)
    ?.run()

describe("the sibling steps", () => {
  test("Alt+ArrowUp and Alt+ArrowDown open the one before and the one after", () => {
    const opened = []
    const steps = siblingSteps(["a", "b", "c"], "b", (key) => opened.push(key))
    const commands = stepCommands("thing", steps)
    press(commands, "ArrowUp")
    press(commands, "ArrowDown")
    expect(opened).toEqual(["a", "c"])
    expect(commands.map((command) => command.label)).toEqual(["Previous thing", "Next thing"])
  })

  test("the ends of the list are disabled, and pressing them opens nothing", () => {
    const opened = []
    const first = stepCommands("thing", siblingSteps(["a", "b"], "a", (key) => opened.push(key)))
    expect(first[0].disabled).toBe(true)
    press(first, "ArrowUp")
    const last = stepCommands("thing", siblingSteps(["a", "b"], "b", (key) => opened.push(key)))
    expect(last[1].disabled).toBe(true)
    press(last, "ArrowDown")
    expect(opened).toEqual([])
  })

  test("an item the list does not hold steps onto the list's first and last", () => {
    const opened = []
    const steps = siblingSteps(["a", "b", "c"], "z", (key) => opened.push(key))
    steps.next.run()
    steps.previous.run()
    expect(opened).toEqual(["a", "c"])
    expect(siblingSteps([], null, () => {}).next.disabled).toBe(true)
  })
})
