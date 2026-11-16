import Link from "next/link"
import { cn } from "@/lib/utils"
import { ArrowRight } from "@/components/icons"

/**
 * A link inside a sentence: underlined at rest, in the sentence's own ink.
 *
 * A link told apart from its words only by colour, or only once the pointer
 * is on it (`hover:underline`), is not a link to anybody who cannot see the
 * colour or has no pointer (WCAG 1.4.1, axe's `link-in-text-block`). It was
 * spelled about fifty ways — `underline underline-offset-2`, `-4`,
 * `text-brand hover:underline`, `text-foreground hover:underline` — and two
 * in three had no focus ring. The class is for a `Link` that cannot be a
 * `TextLink`, a Radix `asChild` one.
 */
export const INLINE_LINK =
  "rounded-sm underline decoration-1 underline-offset-2 focus-ring hover:decoration-2"

/**
 * The way on to the page a block stands for, at the end of its head ("All
 * metrics →"): words and an arrow, quiet until the pointer is on it. The
 * arrow is what says it is a link, so it needs no underline. The hint size is
 * 17px, too small to press: `hit-area` makes it 24px under a mouse and 44px
 * under a finger without growing the head it sits in.
 */
export const ONWARD_LINK =
  "hit-area flex items-center gap-1 rounded-md text-hint font-medium whitespace-nowrap text-muted-foreground focus-ring hover:text-foreground"

const external = (href: string) => /^[a-z][a-z\d+.-]*:/i.test(href)

/**
 * A link drawn as text, in one of two faces.
 *
 * - `inline` (the default): part of a sentence, underlined at rest.
 * - `onward`: the way on from a block's head, with its arrow
 *   (`SectionLink`, and `PanelHeader`'s `link`).
 *
 * An address with a scheme (`https:`, `mailto:`) is a plain anchor and, on
 * the web, opens beside the dashboard rather than in place of it.
 */
export function TextLink({
  href,
  face = "inline",
  className,
  children,
  ...props
}: {
  href: string
  face?: "inline" | "onward"
  className?: string
  children: React.ReactNode
} & Omit<React.AnchorHTMLAttributes<HTMLAnchorElement>, "href" | "className" | "children">) {
  const classes = cn(face === "onward" ? ONWARD_LINK : INLINE_LINK, className)
  const body =
    face === "onward" ? (
      <>
        {children} <ArrowRight aria-hidden className="size-3" />
      </>
    ) : (
      children
    )
  if (external(href)) {
    const web = /^https?:/i.test(href)
    return (
      <a
        href={href}
        target={web ? "_blank" : undefined}
        rel={web ? "noreferrer" : undefined}
        data-slot={face === "onward" ? "section-link" : "text-link"}
        className={classes}
        {...props}
      >
        {body}
      </a>
    )
  }
  return (
    <Link
      href={href}
      data-slot={face === "onward" ? "section-link" : "text-link"}
      className={classes}
      {...props}
    >
      {body}
    </Link>
  )
}
