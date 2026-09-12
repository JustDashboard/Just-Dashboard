"use client"

import { cn } from "@/lib/utils"

/**
 * A block you pick one of: a deployment source, a container template, a database
 * driver, a colour theme, a keyboard preset.
 *
 * Six places had built this and none of them agreed. Three radii
 * (`rounded-lg`, `rounded-xl`, and one with none at all), two grounds
 * (`bg-card` and `bg-control`), three paddings, and only the deployment wizard
 * set a minimum height — so the same gesture looked like a card in Docker, a
 * button in the wizard and a swatch on the appearance page.
 *
 * It is still not a `Panel`: a panel is a block of content that happens to be
 * framed, and this is a button that happens to be large. Nothing lifts any
 * more, so the distinction is carried by the ground and the press instead —
 * a choice sits on `--control` and answers the pointer, a panel sits on
 * `--card` and does not.
 *
 * Selected is `bg-accent` with a full-strength border, which is the same mark
 * the terminal rail puts on the active session and `ToggleGroup` puts on the
 * pressed item. The primary tint is reserved for a command.
 *
 * `choiceCardClasses` is exported separately because two of these are a `<label>`
 * around a visually hidden radio — the right markup for "pick exactly one of
 * these", which a `<button>` cannot express. Those keep their semantics and take
 * only the look.
 */
export function choiceCardClasses(selected?: boolean) {
  return cn(
    "flex min-h-20 min-w-0 flex-col items-start gap-1.5 rounded-lg border p-3 text-left focus-ring transition-colors",
    selected
      ? "border-brand bg-accent"
      : "border-hairline bg-control hover:bg-control-hover active:bg-control-active",
  )
}

export function ChoiceCard({
  selected,
  className,
  children,
  ...props
}: React.ComponentProps<"button"> & { selected?: boolean }) {
  return (
    <button
      type="button"
      data-slot="choice-card"
      aria-pressed={selected}
      data-selected={selected || undefined}
      className={cn(choiceCardClasses(selected), className)}
      {...props}
    >
      {children}
    </button>
  )
}

/** The choice's name. Always the first line, so a row of them scans. */
export function ChoiceCardTitle({ className, ...props }: React.ComponentProps<"span">) {
  return <span className={cn("text-body font-medium", className)} {...props} />
}

/** One line on what picking it means. Two at most — this is not a panel body. */
export function ChoiceCardHint({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span className={cn("text-hint leading-relaxed text-muted-foreground", className)} {...props} />
  )
}
