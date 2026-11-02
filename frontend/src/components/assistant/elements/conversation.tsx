"use client"

import { StickToBottom, useStickToBottomContext } from "use-stick-to-bottom"
import { ArrowDown } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

/**
 * AI Elements' `conversation` (vercel/ai-elements, Apache-2.0), from its registry,
 * in this product's marks: a column that stays on the newest line while it arrives,
 * lets go when the reader scrolls up, and offers the way back down.
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
  return (
    <StickToBottom
      className={cn("relative min-h-0 flex-1 overflow-y-hidden", className)}
      initial="smooth"
      resize="smooth"
      role="log"
      aria-label={label}
      aria-busy={busy}
    >
      <StickToBottom.Content className={cn("flex flex-col gap-6 p-4", contentClassName)}>
        {children}
      </StickToBottom.Content>
      <ScrollButton />
    </StickToBottom>
  )
}

function ScrollButton() {
  const { isAtBottom, scrollToBottom } = useStickToBottomContext()
  if (isAtBottom) return null
  return (
    <Button
      type="button"
      size="icon"
      variant="outline"
      aria-label="Scroll to the newest"
      className="absolute bottom-3 left-1/2 -translate-x-1/2 animate-rise rounded-full bg-popover"
      onClick={() => void scrollToBottom()}
    >
      <ArrowDown className="size-4" />
    </Button>
  )
}
