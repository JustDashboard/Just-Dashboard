import Link from "next/link"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import type { Engine } from "@/components/database/engine"
import { EngineMark } from "@/components/database/kit/engine-mark"

/**
 * "<Engine> has no <thing>": what stands where a page or a feature would be
 * on an engine that does not have it.
 *
 * It is a statement about the engine, not a failure — Redis has no schema the
 * way a file has no port — so it is drawn as the engine itself with one
 * sentence of why and the place that does the job instead. Nothing is asked
 * of the server to draw it, which is the point: the section used to mount a
 * SQL page over a key–value store and show whatever error came back.
 */
export function BlockedState({
  engine,
  thing,
  children,
  href,
  action,
  className,
}: {
  engine: Engine
  /** What is missing, as the title's last words: "schema", "roles", "transactions". */
  thing: string
  /** Why, and what to use instead. */
  children?: React.ReactNode
  /** Where the job is done instead. */
  href?: string
  /** The link's words: "Open Keys". */
  action?: string
  className?: string
}) {
  return (
    <EmptyState
      className={className}
      mark={<EngineMark engine={engine} />}
      title={`${engine.label} has no ${thing}`}
      description={children}
      action={
        href &&
        action && (
          <Button size="sm" variant="outline" asChild>
            <Link href={href}>{action}</Link>
          </Button>
        )
      }
    />
  )
}
