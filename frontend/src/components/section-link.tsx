import Link from "next/link"
import { cn } from "@/lib/utils"
import { ArrowRight } from "@/components/icons"

/**
 * The way on to the page a block stands for, at the end of its head: "All
 * metrics →", "Audit log →".
 *
 * It was typed by hand about thirty times, and a few copies had lost the
 * focus ring or carried a fixed height to line up with a button. The line is
 * the hint size, 17px, which is too small to press: `hit-area` makes it 24px
 * under a mouse and 44px under a finger without growing the head it sits in.
 * `PanelHeader`'s `link` draws it, so a head never needs to.
 */
export function SectionLink({
  href,
  children,
  className,
}: {
  href: string
  children: React.ReactNode
  className?: string
}) {
  return (
    <Link
      href={href}
      data-slot="section-link"
      className={cn(
        "hit-area flex items-center gap-1 rounded-md text-hint font-medium whitespace-nowrap text-muted-foreground focus-ring hover:text-foreground",
        className,
      )}
    >
      {children} <ArrowRight className="size-3" />
    </Link>
  )
}
