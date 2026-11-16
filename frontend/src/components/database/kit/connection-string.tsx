"use client"

import { Check, Copy, Eye, EyeOff } from "@/components/icons"
import { useCopy } from "@/hooks/use-copy"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { Well } from "@/components/panel"
import { Segments } from "@/components/segments"
import { Button } from "@/components/ui/button"
import type { ConnectFormat } from "@/components/database/shell/connection-string"

/**
 * A connection string in the shape a program reads it in: the shapes as one
 * segmented control (a URL, an `.env` line, the engine's shell, a client
 * library), the string in a well, and Copy with its tick. Where the password
 * is hidden, an eye shows it.
 *
 * The Connect popover and an account's "connect as" were two blocks with
 * different controls for the one string: chips in a group against a chip
 * strip, an outline Copy with a tick against a ghost one without, a well that
 * selects all against one that does not. A choice of a few short words is a
 * segmented control everywhere else on these pages.
 *
 * `onCopy` is for a string whose drawn form is not what is copied: the
 * popover draws the password masked and reads the real one when Copy is
 * pressed. It answers the text to copy, or `null` to copy nothing (the read
 * came back after the popover closed). Without it the drawn text is copied.
 */
export function ConnectionString({
  formats,
  shape,
  onShape,
  text,
  muted,
  copy = true,
  onCopy,
  busy,
  reveal,
  className,
}: {
  formats: ConnectFormat[]
  shape: string
  onShape: (shape: string) => void
  text: string
  /** The string is drawn masked: in the muted ink until it is shown. */
  muted?: boolean
  /** Offer Copy; a reader who may not read the password is not offered it. */
  copy?: boolean
  onCopy?: () => Promise<string | null>
  /** A read for the real string is out: the controls wait for it. */
  busy?: boolean
  /** The eye that shows the hidden password, and what it does. */
  reveal?: { shown: boolean; onToggle: () => void }
  className?: string
}) {
  const { copy: write, copied } = useCopy()
  const press = async () => {
    const value = onCopy ? await onCopy() : text
    if (value !== null) await write(value, "Connection string copied")
  }
  return (
    <div data-slot="connection-string-block" className={cn("min-w-0 space-y-2", className)}>
      {/* The controls step under the shapes where both do not fit: a
          phone's popover is narrower than five shapes and Copy. */}
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <Segments
          label="Shape"
          value={formats.some((one) => one.id === shape) ? shape : ""}
          onChange={onShape}
          options={formats.map((one) => ({
            value: one.id,
            label: one.label,
            mono: one.id === "cli",
          }))}
        />
        {(copy || reveal) && (
          <div className="ml-auto flex shrink-0 items-center gap-1">
            {reveal && (
              <IconAction
                label={reveal.shown ? "Hide the password" : "Show the password"}
                onClick={reveal.onToggle}
                disabled={busy}
              >
                {reveal.shown ? <EyeOff /> : <Eye />}
              </IconAction>
            )}
            {copy && (
              <Button size="xs" variant="outline" onClick={() => void press()} disabled={busy}>
                {copied ? <Check /> : <Copy />}
                Copy
              </Button>
            )}
          </div>
        )}
      </div>
      <Well
        data-slot="connection-string"
        className={cn(
          "max-h-48 break-all whitespace-pre-wrap select-all",
          muted ? "text-muted-foreground" : "text-foreground",
        )}
      >
        {text}
      </Well>
    </div>
  )
}
