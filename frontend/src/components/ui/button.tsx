import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { Slot } from "radix-ui"

import { cn } from "@/lib/utils"
import { LoaderCircle } from "@/components/icons"

/**
 * The press box of a control smaller than a finger: 24px under a mouse and
 * 44px under a finger, centred on the control, drawn by an `::after` so the
 * row it sits in does not grow. The `min(0px, …)` keeps a control that is
 * already big enough from reaching past its own edge.
 *
 * It is `@utility hit-area` in globals.css. The utility is emitted before the
 * position utilities, so a caller's `absolute` or `sticky` still places the
 * control.
 */
const hitAreaClasses = "hit-area"

const buttonVariants = cva(
  // The label is the body size, the size of the field and the menu item beside
  // it: a 14px word next to a 13px field read as two type scales in one row.
  //
  // The height is a custom property rather than an `h-*` per pointer so that a
  // caller's own height still wins at every pointer: a `pointer-coarse:h-10`
  // would survive a caller's `h-auto` and stand an inline text button 40px tall
  // on a phone. Each size sets `--control-h` and its coarse step; `h-*`,
  // `size-*` or `h-auto` from a caller replaces the one height class.
  "inline-flex h-(--control-h) shrink-0 items-center justify-center gap-2 rounded-md text-body font-medium whitespace-nowrap focus-ring transition-all disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      // A button is a face a hair above the page: its fill and one lit pixel
      // along its top edge (`--edge`), as light catches the top of a key, and
      // no border round it. The lift it once carried — a gloss, a shine, a
      // lip, a drop shadow and a one-pixel translate on press — stays gone:
      // 553 controls all standing off the page is a page with no foreground.
      // The edge is the whole of the height it takes.
      //
      // Feedback is colour, in two steps. Hover moves the face *away* from the
      // card it sits on; press moves it back past its resting value, so the
      // gesture still has a beginning and an end without anything moving.
      // Ghost and link have no face to move, so they borrow the accent wash.
      variant: {
        // The command face is the brand blue, not ink: the product's one
        // pressable action wears its one colour, and the token carries the
        // hover and press steps so no call site re-mixes an opacity pair.
        default:
          "bg-brand text-brand-foreground shadow-edge-brand hover:bg-brand-hover active:bg-brand-active",
        //
        // The danger face has the same three steps as opaque colours. At rest
        // it is the red a sixth of the way to black, because white on the
        // token itself is 3.8:1 and the label is 13px; hover lifts it to the
        // token, press settles it past rest. The steps are tokens in
        // globals.css (`--destructive-face`, `--destructive-active`).
        destructive:
          "bg-destructive-face text-destructive-foreground shadow-edge-danger hover:bg-destructive active:bg-destructive-active",
        // The border box stays, transparent at the sides and the foot, so the
        // button is the size it was and its top border is the lit edge.
        outline:
          "border border-transparent border-t-edge bg-control hover:bg-control-hover hover:text-accent-foreground active:bg-control-active",
        /**
         * @deprecated one caller (xterm-pane.tsx); use `outline`. Kept on the
         * control steps until it moves.
         */
        secondary:
          "bg-secondary text-secondary-foreground shadow-edge hover:bg-control-hover active:bg-control-active",
        ghost: "hover:bg-accent hover:text-accent-foreground active:bg-menu-hover",
        // Press steps the ink down to the muted one rather than fading it:
        // text never takes an opacity.
        link: "text-primary underline-offset-4 hover:underline active:text-muted-foreground",
      },
      // One step per density, fine pointer then coarse (the control-size
      // table in the foundations). Dense and compact stay short under a finger
      // too, 32 and 40, so a toolbar keeps its proportions; the hit area makes
      // up the rest of the 44 without growing the row they sit in.
      //
      // Icons follow the density: 12 dense, 14 compact, 16 regular and large.
      size: {
        default:
          "px-4 py-2 [--control-h:2.25rem] has-[>svg]:px-3 pointer-coarse:[--control-h:2.75rem]",
        xs: `gap-1 rounded-md px-2 text-xs [--control-h:1.5rem] has-[>svg]:px-1.5 pointer-coarse:[--control-h:2rem] [&_svg:not([class*='size-'])]:size-3 ${hitAreaClasses}`,
        sm: `gap-1.5 rounded-md px-3 [--control-h:2rem] has-[>svg]:px-2.5 pointer-coarse:[--control-h:2.5rem] [&_svg:not([class*='size-'])]:size-3.5 ${hitAreaClasses}`,
        lg: "rounded-md px-6 [--control-h:2.5rem] has-[>svg]:px-4 pointer-coarse:[--control-h:3rem]",
        icon: "size-(--control-h) [--control-h:2.25rem] pointer-coarse:[--control-h:2.75rem]",
        "icon-xs": `size-(--control-h) rounded-md [--control-h:1.5rem] pointer-coarse:[--control-h:2rem] [&_svg:not([class*='size-'])]:size-3 ${hitAreaClasses}`,
        "icon-sm": `size-(--control-h) [--control-h:2rem] pointer-coarse:[--control-h:2.5rem] [&_svg:not([class*='size-'])]:size-3.5 ${hitAreaClasses}`,
        "icon-lg": "size-(--control-h) [--control-h:2.5rem] pointer-coarse:[--control-h:3rem]",
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

export { Button, buttonVariants, hitAreaClasses }
