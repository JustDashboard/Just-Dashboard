"use client"

import * as React from "react"
import { Check, ChevronDown, ChevronUp } from "@/components/icons"
import { Select as SelectPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { MenuItemText } from "@/components/ui/menu-item-text"
import {
  menuIndicatorClasses,
  menuItemClasses,
  menuSurfaceClasses,
} from "@/components/ui/menu-styles"
import { usePortalContainer } from "@/lib/portal-container"

function Select({ ...props }: React.ComponentProps<typeof SelectPrimitive.Root>) {
  return <SelectPrimitive.Root data-slot="select" {...props} />
}

function SelectGroup({ ...props }: React.ComponentProps<typeof SelectPrimitive.Group>) {
  return <SelectPrimitive.Group data-slot="select-group" {...props} />
}

function SelectValue({ ...props }: React.ComponentProps<typeof SelectPrimitive.Value>) {
  return <SelectPrimitive.Value data-slot="select-value" {...props} />
}

/**
 * The select's text is the field's text: the 13px body, and 16px on a phone
 * where `Input` goes to 16 for iOS's sake. It was 14px throughout, so a
 * `FieldRow` of an `Input` and a `Select` read in two sizes side by side (§8).
 * The phone size is the variant rather than the base so that a toolbar's
 * compact select, which passes `text-xs`, keeps it from `sm` up.
 */
function SelectTrigger({
  className,
  size = "default",
  children,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Trigger> & {
  size?: "sm" | "default"
}) {
  return (
    <SelectPrimitive.Trigger asChild {...props}>
      <Button
        data-slot="select-trigger"
        variant="outline"
        size={size === "sm" ? "sm" : "default"}
        className={cn(
          "w-fit min-w-0 justify-between text-body font-normal data-[placeholder]:text-muted-foreground *:data-[slot=select-value]:flex *:data-[slot=select-value]:min-w-0 *:data-[slot=select-value]:items-center *:data-[slot=select-value]:gap-2 *:data-[slot=select-value]:truncate max-sm:h-11 max-sm:text-base [&_svg:not([class*='text-'])]:text-muted-foreground",
          size === "sm" && "max-sm:h-10",
          className,
        )}
      >
        {children}
        <SelectPrimitive.Icon asChild>
          <ChevronDown
            aria-hidden
            className="size-4 transition-transform duration-150 in-data-[state=open]:rotate-180 motion-reduce:transition-none"
          />
        </SelectPrimitive.Icon>
      </Button>
    </SelectPrimitive.Trigger>
  )
}

function SelectContent({
  className,
  children,
  position = "popper",
  align = "start",
  sideOffset = 4,
  collisionPadding = 8,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Content>) {
  return (
    <SelectPrimitive.Portal container={usePortalContainer()}>
      <SelectPrimitive.Content
        data-slot="select-content"
        className={cn(
          menuSurfaceClasses,
          "relative max-h-(--radix-select-content-available-height) max-w-(--radix-select-content-available-width) origin-(--radix-select-content-transform-origin)",
          position === "popper" && "min-w-(--radix-select-trigger-width)",
          className,
        )}
        position={position}
        align={align}
        sideOffset={sideOffset}
        collisionPadding={collisionPadding}
        {...props}
      >
        <SelectScrollUpButton />
        <SelectPrimitive.Viewport className="w-full scroll-my-1">
          {children}
        </SelectPrimitive.Viewport>
        <SelectScrollDownButton />
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  )
}

/** A group's name in the list, as the eyebrow that names a group everywhere else. */
function SelectLabel({ className, ...props }: React.ComponentProps<typeof SelectPrimitive.Label>) {
  return (
    <SelectPrimitive.Label
      data-slot="select-label"
      className={cn("eyebrow px-2 pt-2 pb-1", className)}
      {...props}
    />
  )
}

/** Keep metadata below its name and out of the value copied into the trigger. */
function SelectItem({
  className,
  children,
  hint,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Item> & { hint?: React.ReactNode }) {
  return (
    <SelectPrimitive.Item
      data-slot="select-item"
      className={cn(
        menuItemClasses,
        "w-full pr-8 [&_[data-slot=select-item-text]]:flex [&_[data-slot=select-item-text]]:min-w-0 [&_[data-slot=select-item-text]]:items-center [&_[data-slot=select-item-text]]:gap-2",
        className,
      )}
      {...props}
    >
      <span data-slot="select-item-indicator" className={menuIndicatorClasses}>
        <SelectPrimitive.ItemIndicator>
          <Check aria-hidden className="size-4" />
        </SelectPrimitive.ItemIndicator>
      </span>
      <MenuItemText hint={hint}>
        <SelectPrimitive.ItemText data-slot="select-item-text">{children}</SelectPrimitive.ItemText>
      </MenuItemText>
    </SelectPrimitive.Item>
  )
}

function SelectSeparator({
  className,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Separator>) {
  return (
    <SelectPrimitive.Separator
      data-slot="select-separator"
      className={cn("pointer-events-none -mx-1 my-1 h-px bg-border", className)}
      {...props}
    />
  )
}

function SelectScrollUpButton({
  className,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.ScrollUpButton>) {
  return (
    <SelectPrimitive.ScrollUpButton
      data-slot="select-scroll-up-button"
      className={cn("flex items-center justify-center py-1", className)}
      {...props}
    >
      <ChevronUp className="size-4" />
    </SelectPrimitive.ScrollUpButton>
  )
}

function SelectScrollDownButton({
  className,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.ScrollDownButton>) {
  return (
    <SelectPrimitive.ScrollDownButton
      data-slot="select-scroll-down-button"
      className={cn("flex items-center justify-center py-1", className)}
      {...props}
    >
      <ChevronDown className="size-4" />
    </SelectPrimitive.ScrollDownButton>
  )
}

export {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectScrollDownButton,
  SelectScrollUpButton,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
}
