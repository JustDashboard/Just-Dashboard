"use client"

import { useCallback, useSyncExternalStore } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ApiError, del, get } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbTopology } from "@/lib/types"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { Notice } from "@/components/state"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DATABASES_HREF } from "@/components/database/shell/routes"

/** A deployment that holds the connection through a managed database network. */
export type LinkedDeployment = { name: string; href: string }

/**
 * What the server said when it turned a confirmed action down, held where the
 * open dialog can read it: the dialog's body is handed over once, when the
 * question is asked, and this is how the answer reaches it afterwards.
 */
export function createLinked() {
  let said: LinkedDeployment[] | undefined
  const listeners = new Set<() => void>()
  return {
    read: () => said,
    say(next: LinkedDeployment[] | undefined) {
      said = next
      listeners.forEach((listener) => listener())
    },
    subscribe(listener: () => void) {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
  }
}

export type Linked = ReturnType<typeof createLinked>

/**
 * The deployments bound to a connection, with the page of each where the
 * link is removed. Read only once the server has refused: the refusal says
 * that a link exists, and this says whose.
 */
export async function linkedDeployments(id: number): Promise<LinkedDeployment[]> {
  try {
    const topology = await get<DbTopology>(`/databases/${id}/consumers`)
    const nodes = new Map(topology.nodes.map((node) => [node.id, node]))
    const bound = topology.edges
      .filter((edge) => edge.via.includes("binding"))
      .flatMap((edge) => [nodes.get(edge.from), nodes.get(edge.to)])
      .filter((node) => node?.kind === "deployment" && node.href)
    const seen = new Set<string>()
    return bound.flatMap((node) => {
      if (!node?.href || seen.has(node.href)) return []
      seen.add(node.href)
      return [{ name: node.name, href: `${node.href}/settings/databases` }]
    })
  } catch {
    // The refusal is still explained, without the names.
    return []
  }
}

/**
 * Why the server would not forget or delete a linked database, beside the
 * question that was asked: which deployment holds it, and the page where the
 * link is taken away.
 */
export function LinkedNotice({ linked, what }: { linked: Linked; what: string }) {
  const refused = useSyncExternalStore(linked.subscribe, linked.read, () => undefined)
  if (!refused) return null
  return (
    <Notice tone="danger" title={`It is linked to a deployment, so it was not ${what}`}>
      <p>
        A deployment reaches this database through a managed database network, and{" "}
        {what === "forgotten" ? "forgetting" : "deleting"} it would leave that deployment pointing
        at nothing. Remove the link first, then come back.
      </p>
      {refused.length > 0 ? (
        <ul className="mt-1.5 space-y-0.5">
          {refused.map((deployment) => (
            <li key={deployment.href}>
              <Link
                href={deployment.href}
                className="rounded-sm font-medium text-foreground underline focus-ring"
              >
                {deployment.name} › Settings › Databases
              </Link>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-1.5">
          The link is under that deployment&rsquo;s Settings › Databases &amp; backups.
        </p>
      )}
    </Notice>
  )
}

/**
 * A request that may be refused because a deployment is linked. The refusal
 * is explained in the dialog (`linked`), and what the dialog announces is one
 * plain sentence rather than the server's instruction.
 */
export async function refusingLinked<T>(
  id: number,
  name: string,
  linked: Linked,
  request: () => Promise<T>,
): Promise<T> {
  linked.say(undefined)
  try {
    return await request()
  } catch (err) {
    if (err instanceof ApiError && err.code === "database_linked") {
      linked.say(await linkedDeployments(id))
      throw new ApiError(err.status, err.code, `${name} is linked to a deployment.`)
    }
    throw err
  }
}

/**
 * Forgetting this connection: the dashboard drops its saved address and
 * password, and the database is not touched. Asked first, with the database
 * named, and — where the server refuses because a deployment is linked to it
 * — answered in the same dialog with the way to that deployment.
 *
 * The database's menu on every page and the Settings page's danger zone both
 * ask through this, so the question is one question.
 */
export function useForgetConnection(confirm: (request: ConfirmRequest) => void) {
  const { id, conn, engine, summary } = useDatabase()
  const { connections, refresh } = useDatabases()
  const router = useRouter()

  return useCallback(() => {
    // The server puts the found server a connection came from on its ignore
    // list only when this was the last connection to it, and never for a
    // file: the dialog promises it only where it will happen.
    const ignores =
      Boolean(conn.origin) &&
      !conn.origin.startsWith("file:") &&
      !connections.some((other) => other.id !== id && other.origin === conn.origin)
    const consumers = summary?.consumers ?? 0
    const linked = createLinked()
    confirm({
      title: `Forget ${conn.name}`,
      confirmLabel: "Forget",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: conn.name,
        facts: (
          <>
            <FormFact label="Engine">
              {engine.label}
              {summary?.versionNumber ? ` ${summary.versionNumber}` : ""}
            </FormFact>
            {conn.host && (
              <FormFact label="At" mono>
                {conn.host}
                {conn.port ? `:${conn.port}` : ""}
              </FormFact>
            )}
          </>
        ),
      },
      description: (
        <>
          <LinkedNotice linked={linked} what="forgotten" />
          <p>
            Removes the saved connection, its stored password and its saved queries from the
            dashboard. The database itself and its data are not touched
            {ignores
              ? ", and the server it was found as is put on the ignore list so discovery does not connect it again."
              : "."}
          </p>
          {consumers > 0 && (
            <p>
              {plural(consumers, "deployment environment")} {consumers === 1 ? "is" : "are"} bound
              to it.
            </p>
          )}
        </>
      ),
      action: async () => {
        await refusingLinked(id, conn.name, linked, () => del(`/databases/${id}`))
        notify.success(`Forgot ${conn.name}`)
        refresh()
        router.push(DATABASES_HREF)
        return "reported"
      },
    })
  }, [confirm, id, conn, engine, summary, connections, refresh, router])
}
