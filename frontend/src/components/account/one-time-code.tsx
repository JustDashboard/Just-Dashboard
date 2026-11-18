"use client"

import { Check, Copy, Key } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useCopy } from "@/hooks/use-copy"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** A code an authenticator shows: six digits. */
export const CODE_LENGTH = 6

/**
 * The size of a code field in each register (§16). On the sign-in flow the
 * code is the whole step, so it is set large across the card; in a settings
 * column it is one field among others, at a field's height. One letter
 * spacing for both, and the same indent at its start, so six digits centred
 * in the box are centred and not pushed left by the space after the last.
 *
 * The heights are given at every breakpoint and pointer: `Input` sets its
 * own there, and a code field is the one place its size is not a field's.
 */
const SIZE = {
  flow: "h-14 text-2xl sm:h-14 sm:text-2xl pointer-coarse:h-14",
  reading: "h-11 max-w-48 text-lg sm:h-11 sm:text-lg pointer-coarse:h-11",
} as const

/**
 * The six-digit code from an authenticator.
 *
 * It was written five times at three sizes, and two of them left out
 * `autocomplete="one-time-code"`, so a phone offered the code it had just
 * received on three of the five and not on the other two. Every one now takes
 * the numeric keypad, the phone's code suggestion and six characters, whatever
 * the caller remembers. Inside a `Field` it is named and described by it, as
 * any `Input` is.
 *
 * Anything but a digit is dropped as it is typed or pasted, so "123 456" from
 * a message arrives as the code.
 */
export function OneTimeCodeInput({
  id,
  value,
  onChange,
  size = "reading",
  autoFocus,
  required,
  className,
}: {
  id?: string
  value: string
  onChange: (value: string) => void
  /** `flow` on a sign-in step, `reading` in a settings column or a dialog. */
  size?: keyof typeof SIZE
  autoFocus?: boolean
  required?: boolean
  className?: string
}) {
  return (
    <Input
      id={id}
      autoFocus={autoFocus}
      required={required}
      inputMode="numeric"
      autoComplete="one-time-code"
      pattern="[0-9]*"
      maxLength={CODE_LENGTH}
      placeholder="000000"
      spellCheck={false}
      className={cn("text-center indent-[0.4em] font-mono tracking-[0.4em]", SIZE[size], className)}
      value={value}
      onChange={(event) => onChange(event.target.value.replace(/\D/g, ""))}
    />
  )
}

/**
 * The recovery codes two-factor hands out, shown once: a grid that reads
 * down, a Copy for the whole set, and the one press that says they are kept.
 *
 * Drawn on the sign-in step and on the account's Security page, which each
 * had their own copy at different sizes. `framed` is the settings column's
 * form: there no heading above says what these are, so it carries its own
 * warning, and its buttons are a section's. Unframed, the sign-in step's own
 * heading and line say it, and the buttons are the flow's, across the card.
 */
export function RecoveryCodes({
  codes,
  onDone,
  framed = false,
  busy = false,
}: {
  codes: string[]
  onDone: () => void
  framed?: boolean
  /** The press is being answered: the done button waits. */
  busy?: boolean
}) {
  const { copy, copied } = useCopy()
  const size = framed ? "sm" : "lg"
  return (
    <div data-slot="recovery-codes" className="space-y-4">
      {framed && (
        <Notice tone="warning" icon={Key} title="Save these recovery codes">
          Each one works once, in place of your authenticator. Any previous set no longer works, and
          this is the only time these are shown.
        </Notice>
      )}
      <Well className="grid max-w-md grid-cols-2 gap-x-4 gap-y-1.5">
        {codes.map((code) => (
          <span key={code} className="tracking-wider">
            {code}
          </span>
        ))}
      </Well>
      <div className={cn("flex gap-2", framed ? "flex-wrap" : "flex-col sm:flex-row")}>
        <Button
          type="button"
          size={size}
          variant="outline"
          className={cn(!framed && "sm:flex-1")}
          onClick={() => void copy(codes.join("\n"))}
        >
          {copied ? <Check /> : <Copy />}
          {copied ? "Copied" : "Copy all"}
        </Button>
        <Button
          type="button"
          size={size}
          className={cn(!framed && "sm:flex-1")}
          onClick={onDone}
          pending={busy}
        >
          {!framed && <Key />}I have saved them
        </Button>
      </div>
    </div>
  )
}
