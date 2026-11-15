"use client"

import { Check, Copy } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { useCopy } from "@/hooks/use-copy"
import { cn } from "@/lib/utils"

/*
 * The copy controls. Each answers with a tick where it stands (`useCopy`),
 * which is what a value about to be pasted into a terminal wants: the reader's
 * eye stays on it. A bulk copy ("340 rows as SQL") calls `copyText` from
 * lib/clipboard and says in a toast what was copied; that is a different job
 * and stays there.
 *
 * A label is "Copy" and the thing, with no article: "Copy command", "Copy
 * fingerprint".
 */

/**
 * The word Copy on an outline button, which turns into Copied with a tick: a
 * command, a code or a URL a dialog hands the reader, where the button is the
 * next thing to press.
 */
export function CopyButton({
  text,
  label,
  size = "xs",
  className,
}: {
  text: string
  /** What the button copies, for a screen reader: "Copy command". */
  label: string
  size?: "xs" | "sm"
  className?: string
}) {
  const { copy, copied } = useCopy()
  return (
    <Button
      type="button"
      size={size}
      variant="outline"
      onClick={() => void copy(text)}
      aria-label={label}
      className={className}
    >
      {copied ? <Check /> : <Copy />}
      {copied ? "Copied" : "Copy"}
    </Button>
  )
}

/** The copy icon as a row's or a value's action, whose icon becomes a tick once copied. */
export function CopyAction({
  text,
  label,
  reveal,
  className,
}: {
  text: string
  label: string
  /** Shown on the row's hover and focus, as `IconAction`'s `reveal`. */
  reveal?: boolean
  className?: string
}) {
  const { copy, copied } = useCopy()
  return (
    <IconAction
      label={copied ? "Copied" : label}
      reveal={reveal}
      onClick={() => void copy(text)}
      className={className}
    >
      {copied ? <Check /> : <Copy />}
    </IconAction>
  )
}

/**
 * A literal value in mono with its copy action beside it: a fingerprint, a
 * path, an address. `framed` draws the hairline card box a value stands in
 * when it is the block's subject rather than one fact of a list. `wrap` is
 * how a long value breaks: anywhere for a hash with no word in it, `anywhere`
 * (at a break opportunity first) for a path or a URL.
 */
export function CopyValue({
  value,
  label,
  framed,
  wrap = "all",
  className,
}: {
  value: string
  label: string
  framed?: boolean
  wrap?: "all" | "anywhere"
  className?: string
}) {
  return (
    <span
      className={cn(
        "flex min-w-0 items-start gap-1",
        framed && "rounded-md border border-hairline bg-card px-3 py-2",
        className,
      )}
    >
      <code
        className={cn(
          "min-w-0 flex-1 font-mono text-hint",
          wrap === "all" ? "break-all" : "wrap-anywhere",
        )}
      >
        {value}
      </code>
      <CopyAction text={value} label={label} className="-my-1 shrink-0" />
    </span>
  )
}
