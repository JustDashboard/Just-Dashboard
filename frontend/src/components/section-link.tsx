import { TextLink } from "@/components/inline-link"

/**
 * The way on to the page a block stands for, at the end of its head: "All
 * metrics →", "Audit log →". `TextLink`'s onward face, under the name its
 * thirty callers know; `PanelHeader`'s `link` draws it, so a head never needs
 * to.
 *
 * It was typed by hand about thirty times, and a few copies had lost the
 * focus ring or carried a fixed height to line up with a button.
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
    <TextLink href={href} face="onward" className={className}>
      {children}
    </TextLink>
  )
}
