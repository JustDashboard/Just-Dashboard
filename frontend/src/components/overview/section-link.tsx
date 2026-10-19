import Link from "next/link"
import { ArrowRight } from "@/components/icons"

/** The way on to the page a block stands for, at the end of its head. */
export function SectionLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="flex items-center gap-1 rounded-md text-hint font-medium whitespace-nowrap text-muted-foreground focus-ring hover:text-foreground"
    >
      {children} <ArrowRight className="size-3" />
    </Link>
  )
}
