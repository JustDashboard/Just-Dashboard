import type { Verb } from "@/components/verbs"

/**
 * What the pages under one frame head have asked to be put in its menu.
 *
 * A workbench's verbs are declared where their state is — the open table's,
 * deep in the columns — and drawn in the head, which is above all of them. So
 * each part hands its verbs here under a name of its own and the head reads
 * them back in the order they were first handed over.
 *
 * A part hands them over on every render, because their closures are new on
 * every render; the head is told only when what it draws has changed (a word,
 * a verb that came or went, one that may not be pressed now). Without that a
 * head that re-rendered its page's parts would be told again by them, and
 * round it would go.
 */
export type MenuStore = {
  set: (owner: string, verbs: readonly Verb[]) => void
  drop: (owner: string) => void
  subscribe: (heard: () => void) => () => void
  /**
   * Every verb handed over, owner by owner in the order they arrived. The
   * same array until what the menu draws has changed.
   */
  verbs: () => readonly Verb[]
}

/** What the menu draws of a verb: everything but what pressing it does. */
function face(verbs: readonly Verb[]): string {
  return verbs
    .map((verb) =>
      [
        verb.key,
        verb.label,
        verb.group ?? "",
        verb.disabled ? "off" : "",
        verb.danger ? "danger" : "",
        // Two glyphs are told apart by the component that draws them.
        verb.icon.displayName ?? verb.icon.name,
      ].join("\u0001"),
    )
    .join("\u0002")
}

export function menuStore(): MenuStore {
  const held = new Map<string, { verbs: readonly Verb[]; face: string }>()
  const heard = new Set<() => void>()
  let drawn: readonly Verb[] = []
  const changed = () => {
    drawn = collect()
    for (const tell of heard) tell()
  }
  const collect = () => {
    const all: Verb[] = []
    for (const [owner, entry] of held) {
      for (const verb of entry.verbs) {
        all.push({
          ...verb,
          key: `${owner}:${verb.key}`,
          // Looked up when it is pressed: the menu may have been drawn from
          // an earlier render of the part than the one on screen.
          run: () =>
            held
              .get(owner)
              ?.verbs.find((latest) => latest.key === verb.key)
              ?.run(),
        })
      }
    }
    return all
  }
  return {
    set(owner, verbs) {
      const next = face(verbs)
      const before = held.get(owner)
      held.set(owner, { verbs, face: next })
      if (!before || before.face !== next) changed()
    },
    drop(owner) {
      if (held.delete(owner)) changed()
    },
    subscribe(tell) {
      heard.add(tell)
      return () => heard.delete(tell)
    },
    verbs: () => drawn,
  }
}
