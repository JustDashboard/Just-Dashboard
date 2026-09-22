"use client"

import { External } from "@/components/icons"
import type { useConfirm } from "@/components/confirm-dialog"
import { Hint } from "@/components/docker/explain"
import { Tag } from "@/components/tag"

/**
 * The small things every Docker tab needs: the confirmation opener handed down
 * from the page, and the one shared cell renderer.
 *
 * The tabs used to live in the page component alongside it — five independent
 * bodies, each with its own poll, dialogs and table, in one 880-line file.
 * They now share the panel primitives with the rest of the product, so what is
 * left here is only what genuinely crosses between them.
 */

/** The confirm() opener from useConfirm, passed down from the page. */
export type ConfirmFn = ReturnType<typeof useConfirm>["confirm"]

/**
 * A published port as a link you can click.
 *
 * Obvious once seen, and absent everywhere: the panel knows the address and
 * the port, and an operator's next move after "it is running on 3000" is
 * always to open it. Only loopback and explicit addresses become links —
 * a port on every interface has no single URL that is right, so it stays a
 * label rather than guessing one that leads somewhere else.
 */
export function PortLink({ ip, port, target }: { ip?: string; port: number; target: number }) {
  const label = `${port} → ${target}`
  const host = !ip || ip === "0.0.0.0" || ip === "::" ? "" : ip
  if (!host) {
    return <Tag mono>{label}</Tag>
  }
  return (
    <Tag mono asChild className="transition-colors hover:bg-accent hover:text-primary">
      <a href={`http://${host}:${port}`} target="_blank" rel="noreferrer">
        {label}
        <External className="size-2.5" />
      </a>
    </Tag>
  )
}

/**
 * Names that suggest storage holds a database's own files.
 *
 * A guess, and treated as one: it decides whether a warning is shown, never
 * whether an action is allowed. Getting it wrong in one direction costs a
 * sentence somebody did not need; in the other it costs a corrupted database,
 * so it leans towards warning.
 */
const DATABASE_HINTS =
  /(postgres|mysql|mariadb|mongo|redis|elastic|clickhouse|cassandra|influx|couch)/i

export function looksLikeDatabase(...hints: (string | undefined)[]): boolean {
  return hints.some((hint) => hint !== undefined && DATABASE_HINTS.test(hint))
}

/**
 * The sentence that has to sit above a file browser pointed at a database.
 *
 * A database's files are consistent only from the database's point of view;
 * editing one underneath a running Postgres is how a volume stops being
 * restorable, and neither the file manager nor the browser embedded in these
 * panels gives any hint that this directory is different from any other.
 */
export function DatabaseStorageWarning() {
  return (
    <Hint className="text-warning">
      This looks like a database&apos;s own files. Reading is safe; changing or deleting one
      underneath a running database corrupts it in ways that only show up later. Stop the container
      first if you need to write here.
    </Hint>
  )
}
