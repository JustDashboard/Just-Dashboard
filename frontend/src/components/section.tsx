import { cn } from "@/lib/utils"
import { PanelHeader } from "@/components/panel"

/**
 * A labelled group of panels inside a page, for the pages that hold more than
 * one idea (account, certificates, the dashboard's own configuration), and
 * the section of a detail page whose body is not one panel.
 *
 * Its head is the section head, `PanelHeader` plain: 15px at 600 over a
 * hairline. It was 16px semibold with no rule, so on the host Overview half
 * the heads were ruled and half were not, and a service's page and a
 * container's page, one click apart, were headed two ways. A page that nests
 * a `Section` over panels with their own heads gives those panels `level={3}`.
 */
export function Section({
  title,
  count,
  link,
  actions,
  level,
  className,
  children,
}: {
  title: React.ReactNode
  /** How many the section holds, at the far end of its title line. */
  count?: React.ReactNode
  /** The way on to the page this section stands for. */
  link?: { href: string; label: string }
  actions?: React.ReactNode
  /** The heading element; 2 unless the section sits under another head. */
  level?: 2 | 3
  className?: string
  children: React.ReactNode
}) {
  return (
    <section className={cn("flex min-w-0 flex-col gap-4", className)}>
      <PanelHeader plain level={level} title={title} count={count} link={link} actions={actions} />
      {children}
    </section>
  )
}
