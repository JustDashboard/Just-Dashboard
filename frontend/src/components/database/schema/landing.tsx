"use client"

import { useMemo } from "react"
import Link from "next/link"
import { SidebarLeftOpen } from "@/components/icons"
import { bytes } from "@/lib/format"
import type { DbCatalogGroup } from "@/lib/types"
import { cn } from "@/lib/utils"
import { BarList } from "@/components/bar-list"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import { schemaSummary } from "@/components/database/data/summary"
import { compactCount, grouped } from "@/components/database/data/view"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { objectParams, tableParams } from "@/components/database/schema/address"
import { GroupGlyph, groupSpec } from "@/components/database/schema/kinds"
import type { DbCatalog, SchemaObject } from "@/components/database/schema/types"

/** How many labels of an enum are printed before the rest are counted. */
const LABELS = 6

/**
 * What stands where an object would, before one is chosen: the schema itself.
 *
 * How many of each kind it holds, in the tree's own glyphs and hues; its
 * largest tables, a press from open; and its enum types with their labels,
 * which are the part of a schema most often looked up and least often
 * remembered. The commands that make something new are here too, beside the
 * schema they act on, and the one that drops it.
 */
export function SchemaLanding({
  catalog,
  railShown,
  onShowRail,
  creations,
  verbs,
  note,
}: {
  catalog: DbCatalog
  railShown: boolean
  onShowRail: () => void
  /** What can be made here; the first is the page's one command. */
  creations: Verb[]
  /** What can be done to the schema itself. */
  verbs: Verb[]
  /** What cannot be made here, and where it is made instead. */
  note?: string
}) {
  const { engine, href, goto } = useDatabase()
  const summary = useMemo(() => schemaSummary(catalog), [catalog])
  const info = catalog.schemas.find((schema) => schema.name === catalog.schema)
  const name = catalog.schema || engine.label
  const order = engine.capabilities.catalogGroups.filter((group) => catalog.objects[group])
  const total = order.reduce((sum, group) => sum + (catalog.objects[group]?.length ?? 0), 0)
  const enums = ((catalog.objects.types ?? []) as SchemaObject[]).filter(
    (type) => type.kind === "enum" && type.values && type.values.length > 0,
  )
  const groupOf = (group: DbCatalogGroup) => groupSpec(group)
  const [first, ...rest] = creations

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      {!railShown && (
        <div className="flex h-10 shrink-0 items-center border-b border-hairline bg-surface-header px-1.5">
          <IconAction
            label="Show the objects"
            className="size-7 max-sm:size-8"
            onClick={onShowRail}
          >
            <SidebarLeftOpen />
          </IconAction>
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div
          className={cn(
            "mx-auto flex w-full max-w-2xl animate-rise flex-col gap-6 px-6 py-10",
            total === 0 && "min-h-full justify-center",
          )}
        >
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            <EngineMark engine={engine} />
            <div className="min-w-0 flex-1">
              <h2 className="truncate font-mono text-title font-medium">{name}</h2>
              <p className="truncate text-body text-muted-foreground">
                {total === 0
                  ? `Nothing in this ${engine.nouns.container} yet.`
                  : (info?.comment ??
                      [
                        info?.owner && `Owned by ${info.owner}`,
                        info?.detail,
                        info?.default && "names resolve here by default",
                      ]
                        .filter(Boolean)
                        .join(" · ")) ||
                    `${grouped(total)} objects`}
                {summary.size !== null && total > 0 && ` · ${bytes(summary.size)} on disk`}
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-1.5">
              {first && (
                <Button size="sm" onClick={first.run}>
                  <first.icon className="size-3.5" />
                  {first.label}
                </Button>
              )}
              {rest.length + verbs.length > 0 && (
                <VerbMenu verbs={[...rest, ...verbs]} label={`More for ${name}`} />
              )}
            </div>
          </div>

          {note && <p className="text-hint leading-relaxed text-muted-foreground">{note}</p>}

          {total > 0 && (
            // How many of each kind, in the tree's own legend. A run of words
            // with their figures rather than ruled cells: eight kinds wrap, and
            // a rule that opens a wrapped line divides nothing.
            <ul aria-label="What it holds" className="flex flex-wrap gap-x-5 gap-y-2">
              {order.map((group) => (
                <li key={group} className="flex items-center gap-1.5 text-xs">
                  <GroupGlyph group={group} />
                  <span className="text-muted-foreground">{groupOf(group).plural}</span>
                  <span className="numeric font-medium">
                    {grouped(catalog.objects[group]?.length ?? 0)}
                  </span>
                </li>
              ))}
            </ul>
          )}

          {summary.largest.length > 0 && (
            <section aria-label={`Largest in ${name}`} className="min-w-0">
              <h3 className="eyebrow pb-1">Largest in {name}</h3>
              <BarList
                className="-mx-2"
                items={summary.largest.map(({ object, group, share }) => {
                  const rows = object.estimatedRows
                  const counted = rows !== undefined && rows >= 0
                  return {
                    key: `${group}:${object.name}`,
                    label: object.name,
                    mark: <GroupGlyph group={group} />,
                    hint:
                      summary.rankedBy === "size" && counted
                        ? `~${compactCount(rows)} rows`
                        : undefined,
                    value:
                      summary.rankedBy === "size"
                        ? bytes(object.size ?? 0)
                        : `~${compactCount(rows ?? 0)}`,
                    share,
                    title: `Open ${object.name}`,
                    onClick: () => goto("schema", tableParams(object.schema, object.name)),
                  }
                })}
              />
            </section>
          )}

          {enums.length > 0 && (
            <section aria-label="Enum types" className="min-w-0">
              <h3 className="eyebrow pb-1">Enum types</h3>
              <ul className="divide-y divide-hairline">
                {enums.map((type) => (
                  <li key={type.name}>
                    <Link
                      href={href("schema", objectParams(type))}
                      className="-mx-2 flex min-h-9 flex-wrap items-center gap-x-3 gap-y-1 rounded-md px-2 py-1.5 focus-ring-inset transition-colors hover:bg-row-hover"
                    >
                      <span className="flex min-w-0 items-center gap-1.5">
                        <GroupGlyph group="types" />
                        <span className="truncate font-mono text-xs font-medium">{type.name}</span>
                      </span>
                      <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                        {type.values!.slice(0, LABELS).map((label) => (
                          <Tag key={label} mono>
                            {label}
                          </Tag>
                        ))}
                        {type.values!.length > LABELS && (
                          <span className="numeric text-hint text-muted-foreground">
                            +{type.values!.length - LABELS}
                          </span>
                        )}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            </section>
          )}

          {!railShown && total > 0 && (
            <div>
              <Button size="sm" variant="outline" onClick={onShowRail}>
                Show the objects
              </Button>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
