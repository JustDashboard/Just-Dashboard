"use client"

import { useEffect, useRef, type RefObject } from "react"
import { useViewState } from "@/lib/view-state"
import {
  eventNames,
  isCharacterShortcut,
  isTypingTarget,
  keyOwnedElsewhere,
  shortcutSteps,
} from "@/lib/keys"

/**
 * What a shortcut does. It is handed the key press and prevents its default;
 * returning `false` leaves the key to the browser, for a `/` on a page with
 * no search to put the cursor in.
 */
export type ShortcutHandler = (event: KeyboardEvent) => void | boolean
/** Shortcuts as they are written in the help (`n`, `Shift+R`, `g s`); a falsy one is off. */
export type ShortcutMap = Record<string, ShortcutHandler | false | null | undefined>

/** How long a leading key (`g`) waits for the one that says where to. */
const SEQUENCE_MS = 1500

/**
 * Whether one-character shortcuts work outside the thing that has focus.
 *
 * Speech input types letters, so a page that acts on `n` or `t` wherever
 * focus is acts on words somebody said (WCAG 2.1.4). It is on by default and
 * kept per browser, as the theme and the terminal's font are: it is a
 * property of how this screen is driven, not of the account. Chords with
 * Ctrl, ⌘ or Alt, named keys (Enter, F5) and a list's own letters while focus
 * is in the list keep working when it is off.
 */
export function useSingleKeyShortcuts() {
  return useViewState<boolean>("keys.single", true)
}

/**
 * A page's keys, with the guards every handler needs: none fires while the
 * reader is typing (a field, an editable block, Monaco, a terminal) or while
 * a dialog or menu owns the keyboard, and a one-character key waits for the
 * single-key preference.
 *
 * `scope` keeps the map to focus inside one element, a list's `j` and `k`:
 * those answer only there, so the preference does not apply to them, and a
 * dialog elsewhere does not stop them either.
 */
export function useShortcuts(
  map: ShortcutMap,
  { scope, enabled = true }: { scope?: RefObject<HTMLElement | null>; enabled?: boolean } = {},
) {
  const [single] = useSingleKeyShortcuts()
  const current = useRef({ map, single, enabled, scope })
  useEffect(() => {
    current.current = { map, single, enabled, scope }
  })

  useEffect(() => {
    let leading: { name: string; at: number } | null = null
    const onKey = (event: KeyboardEvent) => {
      const { map, single, enabled, scope } = current.current
      if (!enabled || event.defaultPrevented || event.isComposing) return
      const target = event.target instanceof Node ? event.target : null
      if (scope) {
        if (!target || !scope.current?.contains(target)) return
      } else if (keyOwnedElsewhere()) {
        return
      }
      const typing = isTypingTarget(event.target)
      const names = eventNames(event)
      const usable = (spec: string) =>
        !(typing && isCharacterOrPlain(spec)) &&
        (single || Boolean(scope) || !isCharacterShortcut(spec))

      const specs = Object.entries(map).filter(
        (entry): entry is [string, ShortcutHandler] => Boolean(entry[1]) && usable(entry[0]),
      )
      const before = leading
      leading = null
      const run = ([, handler]: [string, ShortcutHandler]) => {
        if (handler(event) !== false) event.preventDefault()
      }

      if (before && Date.now() - before.at < SEQUENCE_MS) {
        const found = specs.find(([spec]) => {
          const steps = shortcutSteps(spec)
          return steps.length === 2 && steps[0] === before.name && names.includes(steps[1])
        })
        if (found) run(found)
        // The key after a leader is spent either way: `g` then an unknown
        // letter is not that letter's own shortcut.
        return
      }
      const found = specs.find(([spec]) => {
        const steps = shortcutSteps(spec)
        return steps.length === 1 && names.includes(steps[0])
      })
      if (found) {
        run(found)
        return
      }
      const leader = names.find((name) =>
        specs.some(([spec]) => {
          const steps = shortcutSteps(spec)
          return steps.length === 2 && steps[0] === name
        }),
      )
      if (leader) leading = { name: leader, at: Date.now() }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])
}

/** A key a field would take as text or as its own: anything without Ctrl, ⌘ or Alt. */
function isCharacterOrPlain(spec: string) {
  return shortcutSteps(spec).every((step) => !/(^|\+)(Mod|Ctrl|Alt|Meta)\+/.test(step))
}
