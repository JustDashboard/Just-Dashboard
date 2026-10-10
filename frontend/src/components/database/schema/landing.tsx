"use client"

import { BarList } from "@/components/bar-list"
import { compactCount, grouped } from "@/components/database/data/view"
import { EngineMark } from "@/components/database/kit"
import { objectParams, tableParams } from "@/components/database/schema/address"
import { GroupGlyph, groupSpec } from "@/components/database/schema/kinds"
import { schemaFigures } from "@/components/database/schema/landing-figures"
import { SchemaMark } from "@/components/database/schema/rail"
import { Composition } from "@/components/database/schema/statistics"
import type { DbCatalog, DbTableStats, SchemaObject } from "@/components/database/schema/types"
import { useDatabase } from "@/components/database/shell/database-context"
import { IconAction } from "@/components/icon-action"
import { SidebarLeftOpen } from "@/components/icons"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { VerbMenu, type Verb } from "@/components/verbs"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { bytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import Link from "next/link"
import { useEffect, useMemo, useRef } from "react"

/** How many labels of an enum are printed before the rest are counted. */
const LABELS = 8
/** How many tables are listed by name where nothing ranks them. */
const LISTED = 12
/** How many tables are asked about: the server's ceiling. */
const MOST = 1000

/**
 * What stands where an object would, before one is chosen: the schema itself,
 * in the figures only this page has.
 *
 * The tree beside it already says what the schema holds, kind by kind, so
 * this does not say it again. It says what the tree cannot: how many rows
 * there are in all, how much of the disk they take and how much of that is
 * indexes — read from the engine's own table statistics where it keeps them —
 * then the tables that weigh the most, a press from open, and beside them the
 * two things most often looked up and least often remembered: an enum type's
 * labels, and what the views are called.
 *
 * The commands that make something new are in its head, beside the schema
 * they act on, with the one that drops it.
 */
export function SchemaLanding({
  catalog,
  railShown,
  onShowRail,
  creations,
  verbs,
  note,
  asked,
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
  /** Counts the times the reader asked for the schema to be read again. */
  asked: number
}) {
  const { id, engine, href, goto } = useDatabase()
  const measures = engine.can("tableStats")
  const stats = usePoll(
    (signal) =>
      get<DbTableStats>(
        `/databases/${id}/tablestats`,
        { schema: catalog.schema || undefined, limit: MOST },
        signal,
      ),
    0,
    [id, catalog.schema],
    { enabled: measures },
  )
  const reread = stats.refresh
  const answered = useRef(asked)
  useEffect(() => {
    if (answered.current === asked) return
    answered.current = asked
    reread()
  }, [asked, reread])

  const measured = stats.data?.supported ? stats.data.tables : undefined
  const figures = useMemo(() => schemaFigures(catalog, measured), [catalog, measured])
  // The engine is still being asked: a figure the catalogue lacks is on its way, not absent.
  const measuring = measures && !stats.data && !stats.error

  const info = catalog.schemas.find((schema) => schema.name === catalog.schema)
  const name = catalog.schema || engine.label
  const total = Object.values(catalog.objects).reduce((sum, held) => sum + (held?.length ?? 0), 0)
  const tables = (catalog.objects.tables ?? []) as SchemaObject[]
  const enums = ((catalog.objects.types ?? []) as SchemaObject[]).filter(
    (type) => type.kind === "enum" && type.values && type.values.length > 0,
  )
  const views = [
    ...((catalog.objects.views ?? []) as SchemaObject[]).map((object) => ({
      object,
      group: "views" as const,
    })),
    ...((catalog.objects.materializedViews ?? []) as SchemaObject[]).map((object) => ({
      object,
      group: "materializedViews" as const,
    })),
  ]
  const [first, ...rest] = creations
  const facts = [
    info?.comment,
    info?.owner && `Owned by ${info.owner}`,
    info?.detail,
    info?.default && "names resolve here by default",
  ].filter(Boolean)
  const beside = enums.length > 0 || views.length > 0 || figures.reads !== null

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
      <div className="@container min-h-0 flex-1 overflow-y-auto">
        <div
          className={cn(
            "flex animate-rise flex-col gap-6 px-5 py-5",
            // An empty schema is a name and a command: they stand in the
            // middle of the pane, not at the top of an empty one.
            total === 0 && "mx-auto min-h-full max-w-xl justify-center",
          )}
        >
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            <EngineMark engine={engine} />
            <div className="min-w-0 flex-1">
              <h2 className="flex min-w-0 items-center gap-2 font-mono text-title font-medium">
                {catalog.schema && <SchemaMark name={catalog.schema} />}
                <span className="truncate">{name}</span>
              </h2>
              <p className="truncate text-body text-muted-foreground">
                {total === 0
                  ? `Nothing in this ${engine.nouns.container} yet.`
                  : facts.length > 0
                    ? facts.join(" · ")
                    : `A ${engine.nouns.container} of ${engine.label}`}
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
            <div
              className={cn(
                "grid items-start gap-x-10 gap-y-6",
                beside && "@4xl:grid-cols-[minmax(0,1fr)_minmax(0,24rem)]",
              )}
            >
              {figures.largest.length > 0 ? (
                <section aria-label={`Largest in ${name}`} className="min-w-0">
                  <h3 className="border-b border-hairline pb-2 text-title font-medium">
                    Largest in {name}
                  </h3>
                  <BarList
                    className="-mx-2 pt-1.5"
                    items={figures.largest.map(({ object, group, share, rows, bytes: size }) => ({
                      key: `${group}:${object.name}`,
                      label: object.name,
                      mark: <GroupGlyph group={group} />,
                      // What tells it from its neighbours besides its weight:
                      // how many rows, and the storage engine where one is named.
                      hint:
                        [
                          figures.rankedBy === "size" && rows !== null
                            ? `~${compactCount(rows)} rows`
                            : null,
                          object.detail,
                        ]
                          .filter(Boolean)
                          .join(" · ") || undefined,
                      value:
                        figures.rankedBy === "size" ? bytes(size) : `~${compactCount(rows ?? 0)}`,
                      share,
                      title: `Open ${object.name}`,
                      onClick: () => goto("schema", tableParams(object.schema, object.name)),
                    }))}
                  />
                </section>
              ) : tables.length > 0 && !measuring ? (
                // Nothing ranks them: the tables by name, as the engine lists them.
                <section aria-label={`Tables of ${name}`} className="min-w-0">
                  <h3 className="border-b border-hairline pb-2 text-title font-medium">
                    {groupSpec("tables").plural}
                  </h3>
                  <ul className="divide-y divide-hairline">
                    {tables.slice(0, LISTED).map((table) => (
                      <ObjectRow
                        key={table.name}
                        href={href("schema", tableParams(table.schema, table.name))}
                        group="tables"
                        name={table.name}
                        detail={table.comment ?? table.detail}
                      />
                    ))}
                  </ul>
                  {tables.length > LISTED && (
                    <p className="pt-2 text-hint text-muted-foreground">
                      and {grouped(tables.length - LISTED)} more, in the tree.
                    </p>
                  )}
                </section>
              ) : measuring ? (
                <div aria-hidden className="space-y-4 pt-1">
                  {["w-40", "w-28", "w-32", "w-24"].map((width) => (
                    <div key={width} className="space-y-1.5">
                      <Skeleton className={cn("h-3", width)} />
                      <Skeleton className="h-1 w-full" />
                    </div>
                  ))}
                </div>
              ) : null}

              {beside && (
                <div className="min-w-0 space-y-6">
                  {figures.reads && (
                    <section aria-label="How it is read" className="min-w-0">
                      <h3 className="border-b border-hairline pb-2 text-title font-medium">
                        How it is read
                      </h3>
                      <div className="pt-3">
                        <Composition
                          label={`${grouped(figures.reads.byIndex + figures.reads.byScan)} reads`}
                          total="since the counters were last reset"
                          parts={[
                            {
                              key: "index",
                              label: "By an index",
                              value: figures.reads.byIndex,
                              figure: grouped(figures.reads.byIndex),
                              color: "var(--chart-5)",
                            },
                            {
                              key: "scan",
                              label: "By scanning a table whole",
                              value: figures.reads.byScan,
                              figure: grouped(figures.reads.byScan),
                              color: "var(--chart-3)",
                            },
                          ]}
                        />
                      </div>
                    </section>
                  )}
                  {enums.length > 0 && (
                    <section aria-label="Enum types" className="min-w-0">
                      <h3 className="border-b border-hairline pb-2 text-title font-medium">
                        Enum types
                      </h3>
                      <ul className="divide-y divide-hairline">
                        {enums.map((type) => (
                          <li key={type.name}>
                            <Link
                              href={href("schema", objectParams(type))}
                              className="-mx-2 flex flex-col gap-1.5 rounded-md px-2 py-2 focus-ring-inset transition-colors hover:bg-row-hover"
                            >
                              <span className="flex min-w-0 items-center gap-1.5">
                                <GroupGlyph group="types" />
                                <span className="truncate font-mono text-xs font-medium">
                                  {type.name}
                                </span>
                              </span>
                              <span className="flex min-w-0 flex-wrap items-center gap-1.5 pl-5">
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
                  {views.length > 0 && (
                    <section aria-label="Views" className="min-w-0">
                      <h3 className="border-b border-hairline pb-2 text-title font-medium">
                        {groupSpec("views").plural}
                      </h3>
                      <ul className="divide-y divide-hairline">
                        {views.slice(0, LISTED).map(({ object, group }) => (
                          <ObjectRow
                            key={`${group}:${object.name}`}
                            href={href("schema", tableParams(object.schema, object.name))}
                            group={group}
                            name={object.name}
                            detail={object.comment ?? object.detail}
                          />
                        ))}
                      </ul>
                      {views.length > LISTED && (
                        <p className="pt-2 text-hint text-muted-foreground">
                          and {grouped(views.length - LISTED)} more, in the tree.
                        </p>
                      )}
                    </section>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/** One object as a row that opens it: its kind's glyph, its name, and a word about it. */
function ObjectRow({
  href,
  group,
  name,
  detail,
}: {
  href: string
  group: Parameters<typeof GroupGlyph>[0]["group"]
  name: string
  detail?: string
}) {
  return (
    <li>
      <Link
        href={href}
        className="-mx-2 flex min-h-8 min-w-0 items-center gap-1.5 rounded-md px-2 py-1.5 focus-ring-inset transition-colors hover:bg-row-hover"
      >
        <GroupGlyph group={group} />
        <span className="max-w-full shrink-0 truncate font-mono text-xs font-medium">{name}</span>
        {detail && (
          <span className="min-w-0 truncate pl-1 text-hint text-muted-foreground">{detail}</span>
        )}
      </Link>
    </li>
  )
}
