"use client"

import { useState } from "react"
import { Plus, SidebarLeftOpen } from "@/components/icons"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePanelSize } from "@/lib/panel-size"
import { useViewState } from "@/lib/view-state"
import { useConfirm } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { ResizeHandle } from "@/components/resize-handle"
import { EmptyState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import { keyFromAddress, keyToAddress } from "@/components/database/redis/bytes"
import { BulkDialog } from "@/components/database/redis/keys/bulk"
import { KeyPane } from "@/components/database/redis/keys/key-pane"
import { KeyRail } from "@/components/database/redis/keys/key-rail"
import { NewKeyDialog } from "@/components/database/redis/keys/new-key"
import type { RedisBytes } from "@/components/database/redis/types"
import { useRedis } from "@/components/database/redis/use-redis"

const RAIL = { base: 320, min: 240, max: 560 }

/**
 * The key browser: the keys of one logical database beside the one that is
 * open.
 *
 * One frame, two columns with a hairline between them — the rail finds a key
 * (which database, a pattern, a type, as a tree of namespaces or a list) and
 * the pane reads and edits it with the editor its type calls for. Which
 * database and which key are in the address, so a pasted link opens on the
 * same key and Back returns to the one before.
 *
 * The readings a report page would carry as tiles are where the reader is
 * already looking: key counts per database in the picker, per type on the
 * chips, per namespace in the tree, and how far the scan has come at the
 * rail's foot. A workbench has no row of tiles above it.
 */
export function RedisKeys() {
  const redis = useRedis()
  const { id, db, server, selection, param, goto, select, engine, canWrite } = redis
  const { confirm, dialog } = useConfirm()

  const selected = keyFromAddress(selection.key, param("keyB64"))
  const [epoch, setEpoch] = useState(0)
  const refresh = () => setEpoch((n) => n + 1)

  const [creating, setCreating] = useState(false)
  const [bulk, setBulk] = useState(false)

  // The rail beside the key where there is room for both, and over it where
  // there is not: a phone shows the keys until one is opened, then the key.
  const wide = useMediaQuery("(min-width: 1024px)")
  const [railShown, setRailShown] = useViewState(`databases.${id}.redis.rail`, true)
  const [railOver, setRailOver] = useState(false)
  const railVisible = wide ? railShown : railOver || !selected
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize("databases.redis.rail", RAIL.base)
  const railPx = Math.min(Math.max(railWidth, RAIL.min), RAIL.max)

  const open = (key: RedisBytes | undefined) => {
    setRailOver(false)
    // A key's address names its database too, so the link is the same key
    // wherever it is pasted — and a new history entry, so Back is the key
    // that was open before.
    goto("data", {
      db: db === undefined ? undefined : String(db),
      pattern: param("pattern"),
      type: param("type"),
      ...keyToAddress(key),
    })
  }
  const close = () => select(keyToAddress(undefined))

  const railToggle = !railVisible && (
    <IconAction
      label="Show the keys"
      aria-pressed={false}
      className="size-7 shrink-0"
      onClick={() => (wide ? setRailShown(true) : setRailOver(true))}
    >
      <SidebarLeftOpen />
    </IconAction>
  )

  const sentinel = server.data?.mode === "sentinel"

  return (
    <SectionFrame section="data">
      {dialog}
      {server.data?.notice && !sentinel && (
        <Notice title="This server is one node of a cluster" className="shrink-0">
          {server.data.notice}
        </Notice>
      )}
      {sentinel ? (
        <EmptyState
          mark={<EngineMark engine={engine} />}
          title="A sentinel holds no keys"
          description={server.data?.notice}
          className="min-h-0 flex-1"
        />
      ) : (
        // One frame around the workbench: the rail and the key are two
        // columns of one working surface, with a hairline between them.
        <div
          style={{ "--jd-redis-rail": `${railPx}px` } as React.CSSProperties}
          className="relative flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-xl border bg-card"
        >
          {railVisible && (
            <div className="relative flex shrink-0 border-hairline max-lg:absolute max-lg:inset-0 max-lg:z-20 lg:w-(--jd-redis-rail) lg:border-r">
              <KeyRail
                redis={redis}
                selected={selected}
                epoch={epoch}
                onOpen={open}
                onRefresh={refresh}
                onNew={() => setCreating(true)}
                onBulk={() => setBulk(true)}
                onHide={() => (wide ? setRailShown(false) : setRailOver(false))}
              />
              <ResizeHandle
                side="left"
                label="Keys panel width"
                value={railPx}
                min={RAIL.min}
                max={RAIL.max}
                onChange={(px, commit) => setRailWidth(px, commit)}
                onReset={resetRailWidth}
                className="absolute inset-y-0 -right-1 z-20"
              />
            </div>
          )}
          {selected !== undefined ? (
            <KeyPane
              // A key is one editor's worth of state: another key starts clean.
              key={`${db ?? "default"}:${JSON.stringify(selected)}`}
              redis={redis}
              name={selected}
              leading={railToggle}
              confirm={confirm}
              onRail={refresh}
              onOpen={open}
              onClose={() => {
                close()
                refresh()
              }}
            />
          ) : (
            <div className="flex min-h-0 min-w-0 flex-1 flex-col">
              {railToggle && (
                <div className="flex h-9 shrink-0 items-center px-2">{railToggle}</div>
              )}
              <EmptyState
                mark={<EngineMark engine={engine} />}
                className="min-h-0 flex-1 border-0"
                title="No key is open"
                description={`Pick a key on the left to read and edit its value${canWrite ? ", or make a new one" : ""}.`}
                action={
                  canWrite && (
                    <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
                      <Plus />
                      New key
                    </Button>
                  )
                }
              />
            </div>
          )}
        </div>
      )}
      {canWrite && (
        <NewKeyDialog
          redis={redis}
          open={creating}
          onOpenChange={setCreating}
          onCreated={(key) => {
            refresh()
            open(key)
          }}
        />
      )}
      {canWrite && (
        <BulkDialog
          redis={redis}
          open={bulk}
          onOpenChange={setBulk}
          confirm={confirm}
          onDone={() => {
            refresh()
          }}
        />
      )}
    </SectionFrame>
  )
}
