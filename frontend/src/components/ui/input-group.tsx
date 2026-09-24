"use client"

import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"
import type { Icon } from "@/components/icons"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Toggle } from "@/components/ui/toggle"

/**
 * A field and the controls that belong to it, inside one edge.
 *
 * Ported from shadcn's `input-group` and re-sized onto this product's own
 * control ladder. What it fixes is a shape the deployment forms had in four
 * places: an `Input` with a 14px `Switch` and a 36px `Button` standing beside
 * it in the same flex row. Three heights, three edges and three grounds for
 * one decision — "this host, over HTTPS, re-check it" — and the switch, being
 * the smallest thing in the row, read as an unrelated control that had drifted
 * next to the field rather than as part of it. Laid out here they are one box:
 * one border, one height, one focus ring, and the addons separated from the
 * field by a hairline rather than by a gap.
 *
 * The group owns the border, so `InputGroupInput` drops its own — and with it
 * its own focus ring, which is why the box takes `focus-ring-within` (§6's
 * focus mechanism, moved out to the element that actually has an edge to
 * paint).
 *
 * It is not a replacement for `Field`: a group still sits inside one and takes
 * its label, its hint and its error. What it replaces is the row of loose
 * controls that used to sit under the label.
 */
function InputGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="input-group"
      role="group"
      className={cn(
        // The same 44-on-touch, 36-under-a-pointer measure `Input` sets, and
        // for the same reason: the group *is* the field now.
        // `overflow-hidden` so a square-cornered control at either end clips
        // to the group's own radius. It also clips an inner control's outset
        // ring, which is why the two controls that can hold focus in here —
        // the button below and whatever a caller puts in an addon — draw
        // theirs inset: the group says "focus is in this field", the inset
        // ring says which control has it.
        "group/input-group flex h-11 w-full min-w-0 items-stretch overflow-hidden rounded-md border border-input bg-transparent shadow-xs focus-ring-within transition-colors sm:h-9 dark:bg-input/30",
        "has-[[data-slot=input-group-control][aria-invalid=true]]:border-destructive",
        className,
      )}
      {...props}
    />
  )
}

const addonVariants = cva(
  [
    // `cursor-text` so the padding beside the field still focuses it, which is
    // what the click handler below is for.
    "flex shrink-0 cursor-text items-center gap-2 px-2 text-hint text-muted-foreground select-none [&>svg:not([class*='size-'])]:size-3.5",
    // Every rule *inside* the addon is drawn here too, so a caller putting two
    // controls in one addon cannot produce a two-pixel divider by adding its
    // own — which is exactly what the environment rows did: the addon's edge
    // and the first button's `border-l` landed flush at zero padding, so one
    // screen showed the same hairline at one pixel next to the Hostname field
    // and at two beside every Value field. §2 says a hairline, singular.
    "[&>*+*]:border-l [&>*+*]:border-hairline",
  ],
  {
    variants: {
      align: {
        // A hairline, not a gap: the addon is inside the field's edge, and
        // what separates two things inside one surface is a rule (§2).
        "inline-start": "order-first border-r border-hairline",
        "inline-end": "order-last border-l border-hairline",
      },
    },
    defaultVariants: { align: "inline-start" },
  },
)

/**
 * A control or a reading at one end of the field: a unit, a protocol switch, a
 * re-check, a remove.
 */
function InputGroupAddon({
  className,
  align = "inline-start",
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof addonVariants>) {
  return (
    <div
      data-slot="input-group-addon"
      data-align={align}
      className={cn(addonVariants({ align }), className)}
      onClick={(event) => {
        // A press on the addon's own padding belongs to the field; a press on
        // a control inside it belongs to that control.
        if ((event.target as HTMLElement).closest("button, [role='switch'], a, input")) return
        event.currentTarget.parentElement?.querySelector("input")?.focus()
      }}
      {...props}
    />
  )
}

/**
 * The field. Borderless, groundless and ringless — the group draws all three —
 * and it keeps `Input`'s 16px-on-narrow size, because the iOS zoom that rule
 * exists for does not care which element owns the border.
 */
function InputGroupInput({ className, ...props }: React.ComponentProps<"input">) {
  return (
    <Input
      data-slot="input-group-control"
      className={cn(
        "h-full min-w-0 flex-1 rounded-none border-0 bg-transparent shadow-none outline-none sm:h-full dark:bg-transparent",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A command inside the group — "Re-check", "Import", a remove.
 *
 * Ghost by default: the group's own border is the edge, and a second bordered
 * face inside it is the stack of boxes this component exists to undo. A
 * caller whose button *is* the advance passes `variant="default"` and gets the
 * brand face (§16: one command, and it wears the brand).
 *
 * It draws no divider of its own: the addon around it does, for every child
 * after the first.
 */
function InputGroupButton({
  className,
  type = "button",
  variant = "ghost",
  ...props
}: React.ComponentProps<typeof Button>) {
  return (
    <Button
      type={type}
      variant={variant}
      data-slot="input-group-button"
      className={cn("h-full rounded-none px-3 text-xs focus-ring-inset", className)}
      {...props}
    />
  )
}

/**
 * A binary that belongs to the field — serve this hostname over HTTPS, mount
 * this path read-only, take this value from a stored one.
 *
 * The height of the field and inside its edge, never a `Switch` beside it (§7):
 * a switch is the control for an option in a list of options, and a 14px one
 * standing next to a 36px field was the defect this file was written for. It
 * began as `/deploy/new`'s HTTPS toggle and the settings pages needed the same
 * control for three other fields.
 *
 * Pressed is `bg-accent` with the mark in the brand — §3's selection. The
 * ground alone is a step a reader has to compare with the field beside it to
 * see; the tinted mark says it without the comparison. The word goes on a
 * phone, where 390px of field had become 140px of field and 250px of labels;
 * the mark stays, and `aria-label` names the control either way.
 *
 * Goes in an `InputGroupAddon align="inline-end" className="gap-0 p-0"`, whose
 * own rule separates it from its neighbours.
 */
function InputGroupToggle({
  icon: Mark,
  label,
  pressed,
  onPressedChange,
  "aria-label": ariaLabel,
  disabled,
}: {
  icon: Icon
  /** The word beside the mark, from `sm`. */
  label: string
  pressed: boolean
  onPressedChange: (pressed: boolean) => void
  /** What pressing it does, in full: "Serve this hostname over HTTPS". */
  "aria-label": string
  disabled?: boolean
}) {
  return (
    <Toggle
      aria-label={ariaLabel}
      pressed={pressed}
      onPressedChange={onPressedChange}
      disabled={disabled}
      className="h-full gap-1.5 rounded-none px-3 text-xs font-medium focus-ring-inset data-[state=off]:text-muted-foreground"
    >
      <Mark className={cn("size-3.5", pressed ? "text-brand" : "text-muted-foreground")} />
      <span className="max-sm:hidden">{label}</span>
    </Toggle>
  )
}

/** A fixed word in the group: a scheme, a unit, a suffix. */
function InputGroupText({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span className={cn("flex items-center gap-1.5 whitespace-nowrap", className)} {...props} />
  )
}

export {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
  InputGroupText,
  InputGroupToggle,
}
