"use client"

import { createPortal } from "react-dom"
import { CheckCircle, CrossCircle, Information, LoaderCircle, Warning } from "@/components/icons"
import { usePortalContainer } from "@/lib/portal-container"
import { Toaster as Sonner, type ToasterProps } from "sonner"

/**
 * The one place a toast is rendered — shadcn's sonner, kept close to stock.
 *
 * It used to run with `richColors`, which floods the whole surface with the
 * status hue: a green card with a green border and green body text for every
 * "Installed", a red one for every failure. Four saturated surfaces competing
 * with a product whose own palette is ink and grey. The surface is now the
 * same `--popover` as every other floating panel here — menu, tooltip,
 * dropdown — and status is carried by the icon alone, the way `text-warning`
 * and `text-destructive` already carry it in the tables and badges.
 *
 * The rest is the shadcn default. The deviations that remain are not
 * cosmetic:
 *
 * **The icons are this app's**, so a check in a toast is the same check as
 * everywhere else rather than sonner's own set.
 *
 * **The description colour is a literal in sonner's stylesheet** (`#3f3f3f`,
 * and a fixed grey in dark mode) rather than a variable, so it is the one
 * piece of the palette that cannot be handed over through `--normal-*` and
 * has to be overridden by class.
 *
 * **The icon is centred against the whole toast**, which is right for a
 * one-line toast and wrong for every other. A failure here carries its reason
 * underneath it — a `dpkg` refusal runs to three lines — and a centred icon
 * ends up beside the middle of the explanation, pointing at nothing. It
 * aligns to the first line, which is the one it is about.
 *
 * **Sonner renders where it is mounted rather than through a portal**, and
 * this is mounted in the root layout — outside whatever element is in the
 * browser's fullscreen, which is the only thing the compositor paints. So
 * while something is fullscreen the toaster moves inside it; otherwise every
 * "Saved" and every "Could not save" from a fullscreen workspace is silent.
 * The move remounts it, which sonner survives: live toasts live in its own
 * store, not in the element.
 */
const Toaster = ({ ...props }: ToasterProps) => {
  // Sonner paints its own surface, so it has to be told which way the active
  // palette leans or a light theme gets black toasts.
  const fullscreen = usePortalContainer()

  const toaster = (
    <Sonner
      theme="dark"
      className="toaster group"
      icons={{
        success: <CheckCircle className="size-4 text-success" />,
        info: <Information className="size-4 text-muted-foreground" />,
        warning: <Warning className="size-4 text-warning" />,
        error: <CrossCircle className="size-4 text-destructive" />,
        loading: <LoaderCircle className="size-4 animate-spin text-muted-foreground" />,
      }}
      toastOptions={{
        classNames: {
          toast: "!items-start",
          // Half the difference between the 16px icon box and the title's
          // 19.5px line box, so the two share a centre rather than the icon
          // hanging above it.
          icon: "mt-0.5",
          description: "!text-muted-foreground",
        },
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as React.CSSProperties
      }
      {...props}
    />
  )

  return fullscreen ? createPortal(toaster, fullscreen) : toaster
}

export { Toaster }
