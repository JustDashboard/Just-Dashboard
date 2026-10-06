"use client"

import { Fragment } from "react"
import { cn } from "@/lib/utils"
import { useApple } from "@/lib/platform"

/**
 * A shortcut as the keys a reader presses, one cap per key.
 *
 * Shortcuts are written once for both keyboards — `Ctrl/⌘+F`, `F5 · Ctrl/⌘+R`
 * — and drawn for the one in front of the reader: a cap saying "Ctrl/⌘" is a
 * key no keyboard has. `·` separates alternatives, `+` keys held together.
 */
export function Keycaps({ keys, className }: { keys: string; className?: string }) {
  const apple = useApple()
  const ways = keys
    .replaceAll("Ctrl/⌘", apple ? "⌘" : "Ctrl")
    .split(" · ")
    .map((way) => way.split(/\+(?!$)/))
  return (
    <span className={cn("inline-flex items-center gap-1 text-muted-foreground", className)}>
      {ways.map((way, i) => (
        <Fragment key={i}>
          {i > 0 && <span className="px-0.5 text-micro">or</span>}
          {way.map((key) => (
            <kbd key={key} className={KEYCAP}>
              {key}
            </kbd>
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

export const KEYCAP =
  "inline-flex h-5 min-w-5 items-center justify-center rounded-sm border border-hairline bg-control px-1 font-mono text-micro leading-none text-muted-foreground"
