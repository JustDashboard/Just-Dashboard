"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { RefreshClockwise } from "@/components/icons"
import { relativeTime } from "@/lib/format"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { sectionHref } from "@/components/database/engine"
import { FoundList } from "@/components/database/connect/found-list"
import { connectsItself, foundShelves } from "@/components/database/connect/inventory"
import { useFound } from "@/components/database/connect/use-found"
import { useInventory } from "@/components/database/fleet/use-fleet"

/**
 * Take a database that is already on this server: everything discovery found
 * that no saved connection points at, with connecting it as the press.
 *
 * The same list the control center draws under its fleet. Here it is the
 * step of a sequence, so a connection made lands on the new database's home
 * rather than leaving the reader on the list, and a link that names one
 * server (`?key=`) opens on it — on its password form, when a password is
 * what it is waiting for.
 */
export function FoundHere({
  first,
  onStep,
}: {
  /** The inventory key the address names. */
  first: string
  onStep: (step: number) => void
}) {
  const router = useRouter()
  const inventory = useInventory(true)
  const found = useFound({
    inventory,
    onConnected: (connection) => {
      onStep(2)
      router.push(sectionHref(connection.id))
    },
  })
  const shelves = foundShelves(inventory.data)
  const ready = shelves.servers.filter(connectsItself).length

  // The server the link came for is asked for its password as the page
  // opens, once: closing the form does not bring it back.
  const [opened, setOpened] = useState(false)
  const named = first ? inventory.data?.instances.find((one) => one.key === first) : undefined
  if (!opened && named && named.connections.length === 0) {
    setOpened(true)
    if (found.actionOf(named).kind === "credentials") found.ask(named)
  }

  return (
    <Panel plain className="min-w-0 xl:flex xl:h-full xl:min-h-0 xl:flex-col">
      <PanelHeader
        title="On this server"
        actions={
          <>
            {inventory.data && (
              <span className="text-hint text-muted-foreground max-sm:hidden">
                looked {relativeTime(inventory.data.checkedAt)}
              </span>
            )}
            {inventory.scanError && (
              <span className="text-hint text-destructive">{inventory.scanError.message}</span>
            )}
            {ready > 1 && (
              <Button
                size="sm"
                variant="outline"
                pending={found.syncing}
                onClick={() => void found.connectAll()}
              >
                Connect all {ready}
              </Button>
            )}
            <Button
              size="sm"
              variant="outline"
              pending={inventory.scanning}
              onClick={() => void inventory.scan()}
            >
              <RefreshClockwise />
              Scan again
            </Button>
          </>
        }
      />
      {/* A hair of padding, pulled back out: a scroll box clips the focus
          ring a row draws outside its own edge. */}
      <PanelBody className="-mx-1 group-data-[plain]/panel:px-1 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
        <FoundList inventory={inventory} found={found} fresh first={first} />
      </PanelBody>
      {found.dialog}
    </Panel>
  )
}
