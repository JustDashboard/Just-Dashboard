"use client"

import { useCallback, useEffect, useState } from "react"
import { ApiError, errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbConnection } from "@/lib/types"
import { engineOf, type Engine } from "@/components/database/engine"
import { FoundConnectDialog } from "@/components/database/connect/found-connect"
import { foundAction, type FoundAction } from "@/components/database/connect/inventory"
import type { DbInstance, DbSyncReport } from "@/components/database/fleet/types"
import type { InventoryData } from "@/components/database/fleet/use-fleet"
import { useDatabases } from "@/components/database/shell/databases-context"

/** How long a started server is given to show as running before its row stops waiting. */
const SETTLE_MS = 45_000

/**
 * What can be done with the things discovery found, with the row's own "this
 * is happening" state attached: connect one, give it a password, start the
 * container or unit behind it, leave it alone.
 *
 * One hook, because two lists draw the same instances — the control center's
 * and Add a database's — and its attention list offers the same Connect: the
 * password dialog, the row that is mid-sign-in and the sentence a refusal
 * left behind are the same wherever the press came from.
 */
export function useFound({
  inventory,
  onConnected,
  onSynced,
}: {
  inventory: InventoryData
  /** A connection was made (or an existing one found); the lists are already being re-read. */
  onConnected?: (connection: DbConnection, instance: DbInstance) => void
  /** Several were made at once, by the one request that connects what states its credentials. */
  onSynced?: () => void
}) {
  const { drivers, refresh: refreshConnections } = useDatabases()
  const [pending, setPending] = useState<Record<string, string>>({})
  const [settling, setSettling] = useState<Record<string, string>>({})
  const [failures, setFailures] = useState<Record<string, string>>({})
  const [asking, setAsking] = useState<{ instance: DbInstance; message?: string } | null>(null)
  const [syncing, setSyncing] = useState(false)
  const refreshInventory = inventory.refresh

  const engineOfInstance = useCallback(
    (instance: DbInstance): Engine => engineOf(instance.flavor ?? instance.engine, drivers),
    [drivers],
  )
  const actionOf = useCallback(
    (instance: DbInstance): FoundAction =>
      foundAction(instance, engineOfInstance(instance).can("hostAccount")),
    [engineOfInstance],
  )

  const mark = (key: string, word?: string) =>
    setPending((held) => {
      const next = { ...held }
      if (word) next[key] = word
      else delete next[key]
      return next
    })
  const fail = (key: string, message?: string) =>
    setFailures((held) => {
      const next = { ...held }
      if (message) next[key] = message
      else delete next[key]
      return next
    })

  const landed = useCallback(
    (connection: DbConnection, instance: DbInstance) => {
      notify.success(`Connected ${connection.name}`)
      refreshConnections()
      refreshInventory()
      onConnected?.(connection, instance)
    },
    [refreshConnections, refreshInventory, onConnected],
  )

  /** Sign in with what the instance states itself, and open the form when that is not enough. */
  const connect = async (instance: DbInstance) => {
    mark(instance.key, "Connecting")
    fail(instance.key)
    try {
      const connection = await post<DbConnection>("/databases/inventory/connect", {
        key: instance.key,
      })
      landed(connection, instance)
    } catch (err) {
      const refused =
        err instanceof ApiError &&
        (err.code === "credentials_required" ||
          // The server's two wordings for a failed sign-in with nothing typed:
          // only this one is about the credentials, and a password answers it.
          (err.code === "sign_in_failed" && err.message.includes("did not accept the credentials")))
      if (refused) setAsking({ instance, message: errorMessage(err) })
      else {
        fail(instance.key, errorMessage(err))
        // Whatever it was, the list is older than the server's answer.
        refreshInventory()
      }
    } finally {
      mark(instance.key)
    }
  }

  /** Start the container or the unit behind a server that is down. */
  const start = async (instance: DbInstance, path: string) => {
    mark(instance.key, "Starting")
    fail(instance.key)
    try {
      await post(path)
      // The inventory is read from a cache for some seconds, so the row keeps
      // saying it is starting until a reading shows it running.
      setSettling((held) => ({ ...held, [instance.key]: "Starting" }))
      window.setTimeout(
        () =>
          setSettling((held) => {
            const next = { ...held }
            delete next[instance.key]
            return next
          }),
        SETTLE_MS,
      )
      refreshInventory()
    } catch (err) {
      fail(instance.key, errorMessage(err))
    } finally {
      mark(instance.key)
    }
  }

  const waiting = Object.keys(settling).length > 0
  useEffect(() => {
    if (!waiting) return
    const timer = window.setInterval(refreshInventory, 5_000)
    return () => window.clearInterval(timer)
  }, [waiting, refreshInventory])

  /** The one press a row offers. */
  const act = (instance: DbInstance) => {
    const action = actionOf(instance)
    switch (action.kind) {
      case "connect":
      case "open":
        void connect(instance)
        break
      case "credentials":
        setAsking({ instance })
        break
      case "start-container":
        void start(instance, `/docker/containers/${encodeURIComponent(action.id)}/start`)
        break
      case "start-unit":
        void start(instance, `/systemd/${encodeURIComponent(action.unit)}/start`)
        break
    }
  }

  const ignore = async (key: string, ignored: boolean) => {
    mark(key, ignored ? "Ignoring" : "Restoring")
    try {
      await post("/databases/inventory/ignore", { key, ignored })
      refreshInventory()
    } catch (err) {
      notify.error(ignored ? "Could not ignore it" : "Could not bring it back", err)
    } finally {
      mark(key)
    }
  }

  /** Connect every server that states its own credentials, in one request. */
  const connectAll = async () => {
    setSyncing(true)
    try {
      const report = await post<DbSyncReport>("/databases/sync", {})
      refreshConnections()
      refreshInventory()
      onSynced?.()
      if (report.added.length > 0) {
        notify.success(
          report.added.length === 1
            ? `Connected ${report.added[0]}`
            : `Connected ${report.added.length} databases`,
          { description: report.added.join(", ") },
        )
      } else if (report.unreachable.length === 0) {
        notify.info("Nothing new could be connected")
      }
      for (const refused of report.unreachable) {
        notify.warning(`${refused.container} was not connected`, { description: refused.reason })
      }
    } catch (err) {
      notify.error("Could not connect them", err)
    } finally {
      setSyncing(false)
    }
  }

  /** What a row says it is doing, when it is doing something. */
  const busyWord = (instance: DbInstance): string | undefined =>
    pending[instance.key] ?? (instance.state !== "running" ? settling[instance.key] : undefined)

  const dialog = asking && (
    <FoundConnectDialog
      key={asking.instance.key}
      instance={asking.instance}
      engine={engineOfInstance(asking.instance)}
      choice={choiceOf(actionOf(asking.instance))}
      message={asking.message}
      onClose={() => setAsking(null)}
      onConnected={(connection) => {
        const instance = asking.instance
        setAsking(null)
        landed(connection, instance)
      }}
    />
  )

  return {
    act,
    actionOf,
    engineOfInstance,
    ask: (instance: DbInstance) => setAsking({ instance }),
    ignore,
    connectAll,
    syncing,
    busyWord,
    failures,
    dialog,
  }
}

/** Whether the password form offers the account made from this machine as well. */
function choiceOf(action: FoundAction) {
  return action.kind === "credentials" && action.choice
}

export type Found = ReturnType<typeof useFound>
