"use client"

import { createContext, useContext } from "react"
import { AnimatePresence, motion, useReducedMotion } from "motion/react"
import { StickToBottom, useStickToBottomContext } from "use-stick-to-bottom"
import { ArrowDown } from "@/components/icons"
import { cn } from "@/lib/utils"

/**
 * AI Elements' `conversation` (vercel/ai-elements, Apache-2.0), from its registry,
 * in this product's marks: a column that stays on the newest line while it arrives,
 * lets go when the reader scrolls up, and offers the way back down.
 *
 * It is the only thing on its page that scrolls. A chat opened from the history
 * lands on its last line at once rather than travelling down through it; while an
 * answer arrives the column eases after it.
 */
export function Conversation({
  className,
  contentClassName,
  label,
  busy,
  children,
}: {
  className?: string
  contentClassName?: string
  label: string
  busy?: boolean
  children: React.ReactNode
}) {
  // Easing after the newest line is motion too; asked for less, it simply keeps up.
  const reduced = useReducedMotion()
  return (
    <StickToBottom
      data-slot="conversation"
      className={cn("relative min-h-0 flex-1 overflow-y-hidden", className)}
      initial="instant"
      resize={reduced ? "instant" : "smooth"}
      role="log"
      aria-label={label}
      aria-busy={busy}
    >
      <StickToBottom.Content
        className={cn("flex min-h-full flex-col gap-6 p-4", contentClassName)}
        scrollClassName="overscroll-contain"
      >
        <Release>{children}</Release>
      </StickToBottom.Content>
      <ScrollButton />
    </StickToBottom>
  )
}

const ReleaseContext = createContext<() => void>(() => {})

/**
 * Opening something in the column lets go of the bottom first. Held there, a row
 * unfolding near the end would push itself up and away from the pointer that
 * opened it; let go, it unfolds downward where it is, and the way back down
 * appears.
 */
function Release({ children }: { children: React.ReactNode }) {
  const { stopScroll } = useStickToBottomContext()
  return <ReleaseContext.Provider value={stopScroll}>{children}</ReleaseContext.Provider>
}

/** Called before something in the conversation grows because the reader asked it to. */
export function useConversationRelease() {
  return useContext(ReleaseContext)
}

function ScrollButton() {
  const { isAtBottom, scrollToBottom } = useStickToBottomContext()
  return (
    <AnimatePresence>
      {!isAtBottom && (
        <motion.button
          key="latest"
          type="button"
          data-slot="conversation-latest"
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: 8 }}
          transition={{ duration: 0.16, ease: [0.16, 1, 0.3, 1] }}
          className="absolute bottom-3 left-1/2 z-10 inline-flex h-8 -translate-x-1/2 items-center gap-1.5 rounded-md border border-border-strong bg-popover px-3 text-hint font-medium text-popover-foreground shadow-md focus-ring transition-colors hover:bg-control-hover"
          onClick={() => void scrollToBottom()}
        >
          <ArrowDown aria-hidden className="size-3.5" />
          Jump to latest
        </motion.button>
      )}
    </AnimatePresence>
  )
}
