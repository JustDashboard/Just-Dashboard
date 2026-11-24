"use client"

import { Fragment } from "react"
import { cn } from "@/lib/utils"
import { useApple } from "@/lib/platform"
import { useSingleKeyShortcuts } from "@/hooks/use-shortcuts"
import { Switch } from "@/components/ui/switch"

/**
 * One key as a cap: the shape every shortcut hint in the product draws. It is
 * set a step *into* whatever holds it (half the window's ground, lined at the
 * white line), so in a raised field like the rail's search it reads as a key
 * pressed into the face, as Just Hub draws it. Letters are in the sans: a cap
 * reads as a key by its shape, not by a typewriter face.
 */
export const KEYCAP =
  "inline-flex h-5 min-w-5 items-center justify-center rounded-sm border border-line bg-ground/50 px-1 font-sans text-micro font-medium leading-none text-muted-foreground"

/**
 * Keys drawn as a glyph rather than a letter: the arrows, Enter, and the
 * modifiers' signs. A glyph is drawn thinner than a letter, so beside a
 * letter's cap it read a step fainter; a weight up draws it at the letters'
 * ink.
 */
const GLYPH_KEYS = /^[←↑→↓↵⏎⇧⌘⌥⌃]$/
const GLYPH_CAP = "font-sans font-semibold"

/** Names that are keys on a keyboard; anything longer than one character and not here is words. */
const KEY_NAMES = new Set([
  "Ctrl",
  "Alt",
  "Shift",
  "Meta",
  "Option",
  "Enter",
  "Return",
  "Esc",
  "Escape",
  "Tab",
  "Space",
  "Backspace",
  "Delete",
  "Del",
  "Insert",
  "Home",
  "End",
  "PageUp",
  "PageDown",
  "Fn",
])

/** A piece of a written shortcut: a key drawn as a cap, or words around them. */
export type KeyPiece = { cap: boolean; text: string }

/**
 * A written shortcut as what to draw, for one keyboard.
 *
 * Shortcuts are written once for both keyboards — `Ctrl/⌘+F`,
 * `F5 · Ctrl/⌘+R` — and drawn for the one in front of the reader: a cap
 * saying "Ctrl/⌘" is a key no keyboard has. ` · ` separates ways to do the
 * same thing ("or"), ` / ` keys that do the two halves of it (`↑ / ↓`), and
 * `+` keys held together. A piece that is not a key (`click`, "Type its first
 * letters") stays words.
 */
export function keyWays(keys: string, apple: boolean): KeyPiece[][][] {
  return keys
    .replaceAll("Ctrl/⌘", apple ? "⌘" : "Ctrl")
    .split(" · ")
    .map((way) =>
      way.split(" / ").map((part) =>
        part.split(/\+(?!$)/).map((text) => ({
          cap: [...text].length === 1 || KEY_NAMES.has(text) || /^F\d{1,2}$/.test(text),
          text,
        })),
      ),
    )
}

/**
 * A shortcut as the keys a reader presses, one cap per key, Ctrl or ⌘ by the
 * reader's platform.
 *
 * A hint for a keyboard means nothing under a finger, so by default it is not
 * drawn on a touch screen (`pointer: coarse`). A list somebody opened to read
 * the keys (`ShortcutList`) passes `touch="show"`: a tablet with a keyboard
 * case is still a coarse pointer.
 */
export function Keycaps({
  keys,
  touch = "hide",
  className,
}: {
  keys: string
  touch?: "hide" | "show"
  className?: string
}) {
  const ways = keyWays(keys, useApple())
  return (
    <span
      data-slot="keycaps"
      className={cn(
        "inline-flex flex-wrap items-center gap-1 text-muted-foreground",
        touch === "hide" && "pointer-coarse:hidden",
        className,
      )}
    >
      {ways.map((way, w) => (
        <Fragment key={w}>
          {w > 0 && <span className="px-0.5 text-micro">or</span>}
          {way.map((part, p) => (
            <Fragment key={p}>
              {p > 0 && (
                <span aria-hidden className="text-micro">
                  /
                </span>
              )}
              <span className="inline-flex items-center gap-1">
                {part.map((piece, k) =>
                  piece.cap ? (
                    <kbd key={k} className={cn(KEYCAP, GLYPH_KEYS.test(piece.text) && GLYPH_CAP)}>
                      {piece.text}
                    </kbd>
                  ) : (
                    <span key={k} className="text-hint">
                      {piece.text}
                    </span>
                  ),
                )}
              </span>
            </Fragment>
          ))}
        </Fragment>
      ))}
    </span>
  )
}

/** The palette's own shortcut, for the controls that open it. */
export function useLauncherKeys() {
  return useApple() ? "⌘+K" : "Ctrl+K"
}

export type ShortcutGroup = {
  title?: string
  items: { keys: string; does: string }[]
}

/**
 * The shortcuts of a page, as words and the keys that do them: what a help
 * sheet lists. Groups take a title when there is more than one.
 */
export function ShortcutList({
  groups,
  className,
}: {
  groups: ShortcutGroup[]
  className?: string
}) {
  return (
    <div data-slot="shortcut-list" className={cn("space-y-5", className)}>
      {groups.map((group, index) => (
        <section key={group.title ?? index}>
          {group.title && <h3 className="mb-1 text-body font-medium">{group.title}</h3>}
          <dl className="divide-y divide-hairline text-body">
            {group.items.map((item) => (
              <div
                key={item.does}
                className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-1.5"
              >
                <dt>{item.does}</dt>
                <dd>
                  <Keycaps keys={item.keys} touch="show" />
                </dd>
              </div>
            ))}
          </dl>
        </section>
      ))}
    </div>
  )
}

/**
 * The switch for one-character shortcuts (`n`, `t`, `?`, a name's first
 * letters from anywhere on the page). Off, they answer only inside the list
 * that has focus, and chords with Ctrl, ⌘ or Alt keep working: speech input
 * types letters, and a page that acts on them acts on words (WCAG 2.1.4).
 * Kept per browser (`useSingleKeyShortcuts`).
 */
export function SingleKeySwitch({ className }: { className?: string }) {
  const [on, setOn] = useSingleKeyShortcuts()
  return (
    <label
      data-slot="single-key-switch"
      className={cn("flex min-w-0 flex-1 cursor-pointer items-center gap-3 text-body", className)}
    >
      <span className="min-w-0 flex-1">Single-key shortcuts</span>
      <Switch checked={on} onCheckedChange={setOn} />
    </label>
  )
}
