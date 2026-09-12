import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { Slot } from "radix-ui"

import { cn } from "@/lib/utils"
import { LoaderCircle } from "@/components/icons"

const buttonVariants = cva(
  "inline-flex shrink-0 items-center justify-center gap-2 rounded-md text-sm font-medium whitespace-nowrap focus-ring transition-all disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      // A button is a rectangle of ground with a border, and nothing else. The
      // lift it used to carry — a gloss, a shine, a lip, a drop shadow and a
      // one-pixel translate on press — is gone: 553 controls all standing off
      // the page is a page with no foreground.
      //
      // Feedback is colour, in two steps. Hover moves the face *away* from the
      // card it sits on; press moves it back past its resting value, so the
      // gesture still has a beginning and an end without anything moving.
      // Ghost and link have no face to move, so they borrow the accent wash.
      variant: {
        default:
          "bg-primary text-primary-foreground hover:bg-primary/90 active:bg-primary/80",
        destructive:
          "bg-destructive/85 text-white hover:bg-destructive active:bg-destructive/70",
        outline:
          "border border-input bg-control hover:bg-control-hover hover:text-accent-foreground active:bg-control-active",
        secondary:
          "bg-secondary text-secondary-foreground hover:bg-secondary/80 active:bg-secondary/60",
        ghost:
          "hover:bg-accent hover:text-accent-foreground active:bg-menu-hover",
        link: "text-primary underline-offset-4 hover:underline active:text-primary/80",
      },
      size: {
        default: "h-9 px-4 py-2 has-[>svg]:px-3",
        xs: "h-6 gap-1 rounded-md px-2 text-xs has-[>svg]:px-1.5 [&_svg:not([class*='size-'])]:size-3",
        sm: "h-8 gap-1.5 rounded-md px-3 has-[>svg]:px-2.5",
        lg: "h-10 rounded-md px-6 has-[>svg]:px-4",
        icon: "size-9",
        "icon-xs": "size-6 rounded-md [&_svg:not([class*='size-'])]:size-3",
        "icon-sm": "size-8",
        "icon-lg": "size-10",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
)

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  pending = false,
  disabled,
  children,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean
    /**
     * The request this button started has not come back yet.
     *
     * Sixty-two call sites wrote this out: `disabled={busy}` next to
     * `{busy && <Spinner className="size-4" />}`, at four different spinner
     * sizes, and a handful that disabled without showing anything — a button
     * that goes inert and says nothing reads as broken rather than as busy.
     *
     * The spinner replaces the button's own icon rather than sitting beside it,
     * so the button does not change width mid-request and shift the row it is
     * in. `aria-busy` is what says the same thing to a screen reader; the
     * `disabled` attribute alone only says "you cannot press this", not why.
     */
    pending?: boolean
  }) {
  const Comp = asChild ? Slot.Root : "button"

  // `Slot` takes exactly one child, so the spinner is only ever wrapped in when
  // there is actually one to add: returning `[false, children]` from the
  // not-pending path would break every `asChild` button in the app.
  const spinning = pending && !asChild

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      data-pending={pending ? "" : undefined}
      aria-busy={pending || undefined}
      disabled={disabled || pending}
      className={cn(
        buttonVariants({ variant, size }),
        // The button's own glyph steps aside for the spinner rather than making
        // room beside it, so the button does not change width mid-request and
        // shift the row it sits in. Keyed on the spin class so a caller passing
        // several icons loses all of them rather than an arbitrary one.
        spinning && "[&>svg:not(.animate-spin)]:hidden",
        className,
      )}
      {...props}
    >
      {spinning ? (
        <>
          <LoaderCircle className="animate-spin" aria-hidden />
          {children}
        </>
      ) : (
        children
      )}
    </Comp>
  )
}

export { Button, buttonVariants }
