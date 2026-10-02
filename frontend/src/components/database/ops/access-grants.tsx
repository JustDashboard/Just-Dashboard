"use client"

import { useEffect, useMemo, useState } from "react"
import { errorMessage, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { FormNote, OptionList, OptionRow, Statement } from "@/components/form"
import { Modal } from "@/components/modal"
import { ChipCount, ChipStrip, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { nameHue } from "@/components/database/home/kinds"
import { read } from "@/components/database/home/read"
import {
  cellKey,
  heldLater,
  heldOn,
  levelPrivileges,
  pendingWords,
  plannedRequests,
  scopeKey,
  toggled,
  toggledAll,
  type Cell,
  type Held,
  type Membership,
  type Planned,
  type Wanted,
} from "@/components/database/ops/access-model"
import type {
  DbCatalog,
  DbPrivilegeLevel,
  DbPrivilegeResult,
  DbRoleDetail,
} from "@/components/database/ops/access-types"
import { useDatabase } from "@/components/database/shell/database-context"

type ObjectLevel = Exclude<DbPrivilegeLevel["level"], "role">

const LEVEL_WORD: Record<ObjectLevel, { tab: string; one: string; every: string }> = {
  database: { tab: "Databases", one: "database", every: "Every database" },
  schema: { tab: "Schemas", one: "schema", every: "Every schema" },
  table: { tab: "Tables", one: "table", every: "Every table" },
  sequence: { tab: "Sequences", one: "sequence", every: "Every sequence" },
}

/** The objects a level lists in one scope, by the catalogue's own groups. */
const CATALOG_GROUPS: Record<"table" | "sequence", string[]> = {
  table: ["tables", "views", "materializedViews"],
  sequence: ["sequences"],
}

/**
 * What one account holds, object by object, and the way to change it.
 *
 * A level at a time — databases, schemas, tables, sequences, as the engine
 * has them — every object is a row and every privilege of the level a chip on
 * it: pressed where it is held. Pressing one stages a change, drawn in the
 * hues a pending change has everywhere (added, removed), and nothing is sent
 * until the reader has read the statements the server will run for the whole
 * set. A privilege held by owning the object is shown held and cannot be
 * pressed: no revoke takes it away.
 *
 * Roles the account is a member of are staged the same way, on engines that
 * have memberships.
 */
export function GrantsMatrix({
  detail,
  levels,
  roles,
  editable,
  onApplied,
}: {
  detail: DbRoleDetail
  /** The engine's closed set of levels and privileges. */
  levels: DbPrivilegeLevel[]
  /** The other accounts of the server, for memberships. */
  roles: string[]
  /** The reader may grant and revoke, on a connection that is not protected. */
  editable: boolean
  onApplied: () => void
}) {
  const { id, conn, engine } = useDatabase()
  const objectLevels = useMemo(
    () =>
      levels.filter(
        (level): level is DbPrivilegeLevel & { level: ObjectLevel } => level.level !== "role",
      ),
    [levels],
  )
  const memberships = levels.some((level) => level.level === "role")
  // It opens on the level the account holds the most at: that is what the
  // reader came to see, and an empty first level reads as "holds nothing".
  const [view, setView] = useState<ObjectLevel>(() => {
    const count = (name: ObjectLevel) =>
      detail.grants.filter((grant) => grant.level === name && !grant.future && !grant.owner).length
    return [...objectLevels].sort((a, b) => count(b.level) - count(a.level))[0]?.level ?? "database"
  })
  const level = objectLevels.find((one) => one.level === view) ?? objectLevels[0]
  const [scope, setScope] = useState("")
  const [wanted, setWanted] = useState<Wanted>({})
  const [members, setMembers] = useState<Membership[]>([])
  const [reviewing, setReviewing] = useState(false)

  // Which container an object of this level is named inside: a schema of the
  // connection's database, or a database of the server.
  const scoped = level && (level.level === "table" || level.level === "sequence")
  const byDatabase = Boolean(scoped && level.needs.includes("database"))

  const databases = usePoll(
    (signal) =>
      read<{ name: string }[]>(
        `/databases/${id}/schemas`,
        (answer) => Array.isArray(answer),
        undefined,
        signal,
      ),
    0,
    [id],
  )
  // The catalogue of the scope on screen: its schemas, and that scope's objects.
  const catalog = usePoll(
    (signal) =>
      read<DbCatalog>(
        `/databases/${id}/catalog`,
        (answer) => Array.isArray(answer.schemas),
        scope ? { schema: scope } : undefined,
        signal,
      ),
    0,
    [id, scope],
    { enabled: engine.can("catalog") },
  )

  const schemas = useMemo(
    () => (catalog.data?.schemas ?? []).filter((one) => !one.system).map((one) => one.name),
    [catalog.data],
  )
  const scopes = byDatabase ? (databases.data ?? []).map((one) => one.name) : schemas
  const currentScope =
    scope || (byDatabase ? conn.database : (catalog.data?.schema ?? schemas[0] ?? ""))

  const listed: string[] = useMemo(() => {
    if (!level) return []
    if (level.level === "database") return (databases.data ?? []).map((one) => one.name)
    if (level.level === "schema") return schemas
    const groups = CATALOG_GROUPS[level.level]
    const held = catalog.data && catalog.data.schema === currentScope ? catalog.data.objects : {}
    return groups.flatMap((group) => held[group] ?? []).map((one) => one.name)
  }, [level, databases.data, schemas, catalog.data, currentScope])

  if (!level) return null

  const cellOf = (name: string): Cell =>
    level.level === "database"
      ? { level: "database", database: name }
      : level.level === "schema"
        ? { level: "schema", schema: name }
        : byDatabase
          ? { level: level.level, database: currentScope, table: name }
          : { level: level.level, schema: currentScope, table: name }
  const holds = (cell: Cell): Held => {
    const of = levels.find((one) => one.level === cell.level) ?? level
    // A grant on a schema, a table or a sequence is listed for the connection's
    // own database, and names it; the cell says only the schema.
    return heldOn(detail.grants, cell, of)
  }
  // What it holds something on comes first, then the connection's own
  // database, then the rest by name: a server has dozens of databases, and
  // the reader came for the ones this account can touch.
  const rank = (name: string) => {
    const held = holds(cellOf(name))
    if (held.privileges.length > 0 || held.other.length > 0) return 0
    return name === conn.database ? 1 : 2
  }
  const objects = [...listed].sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
  const domain = levelPrivileges(level)
  const hasAll = level.privileges.some((one) => one.toUpperCase() === "ALL")
  const loading =
    level.level === "database"
      ? !databases.data && !databases.error
      : !catalog.data && !catalog.error
  const failed = level.level === "database" ? databases.error : catalog.error

  // How many objects of each level it holds something on, for the strip.
  const heldCount = (name: ObjectLevel) =>
    new Set(
      detail.grants
        .filter((grant) => grant.level === name && !grant.future)
        .map((grant) => [grant.database, grant.schema, grant.table].join("\u0000")),
    ).size

  const press = (cell: Cell, privilege: string) =>
    setWanted((held) => toggled(held, cell, holds(cell), privilege))
  const pressAll = (cell: Cell) =>
    setWanted((held) =>
      toggledAll(held, cell, holds(cell), levels.find((one) => one.level === cell.level) ?? level),
    )
  // The whole scope at once: the privilege is wanted on every object, or on none.
  const pressEvery = (privilege: string) =>
    setWanted((held) => {
      const cells = objects.map(cellOf)
      const everywhere = cells.every((cell) =>
        (held[cellKey(cell)]?.privileges ?? holds(cell).privileges).includes(privilege),
      )
      return cells.reduce((next, cell) => {
        const now = next[cellKey(cell)]?.privileges ?? holds(cell).privileges
        return now.includes(privilege) === !everywhere
          ? next
          : toggled(next, cell, holds(cell), privilege)
      }, held)
    })

  const memberNow = (role: string) => {
    const staged = members.find((one) => one.role === role)
    return staged ? staged.member : Boolean(detail.memberOf?.includes(role))
  }
  const pressMember = (role: string) =>
    setMembers((held) => {
      const was = Boolean(detail.memberOf?.includes(role))
      const rest = held.filter((one) => one.role !== role)
      const next = !memberNow(role)
      return next === was ? rest : [...rest, { role, member: next }]
    })

  const pending = Object.keys(wanted).length + members.length
  const later =
    scoped && level.future
      ? heldLater(detail.grants, { level: level.level, schema: currentScope }, level)
      : []

  return (
    <div className="space-y-4" data-slot="grants-matrix">
      {memberships && (roles.length > 0 || (detail.memberOf?.length ?? 0) > 0) && (
        <div className="space-y-1.5">
          <p className="text-body font-medium">Member of</p>
          {editable ? (
            <ChipStrip role="group" aria-label="Roles it is a member of: pressed where it is one">
              {roles.map((role) => {
                const was = Boolean(detail.memberOf?.includes(role))
                const now = memberNow(role)
                return (
                  <FilterChip
                    key={role}
                    selected={now}
                    className={cn("font-mono", now !== was && !now && "line-through")}
                    style={
                      now !== was ? { color: `var(--git-${now ? "added" : "deleted"})` } : undefined
                    }
                    onClick={() => pressMember(role)}
                  >
                    <span style={now === was ? { color: nameHue(role) } : undefined}>{role}</span>
                  </FilterChip>
                )
              })}
            </ChipStrip>
          ) : detail.memberOf && detail.memberOf.length > 0 ? (
            <p className="flex flex-wrap gap-1.5">
              {detail.memberOf.map((role) => (
                <Tag key={role} mono>
                  {role}
                </Tag>
              ))}
            </p>
          ) : (
            <FormNote>It is a member of no other role.</FormNote>
          )}
          {detail.members.length > 0 && (
            <FormNote>
              Its own members: <span className="font-mono">{detail.members.join(", ")}</span>
            </FormNote>
          )}
        </div>
      )}

      <div className="space-y-3">
        <div
          role="group"
          aria-label="Grants by level"
          className="flex min-w-0 gap-4 overflow-x-auto border-b border-hairline"
        >
          {objectLevels.map((one) => (
            <button
              key={one.level}
              type="button"
              aria-pressed={one.level === level.level}
              onClick={() => {
                setView(one.level)
                setScope("")
              }}
              className={cn(tabClasses(one.level === level.level, "h-9"), "gap-1.5")}
            >
              {LEVEL_WORD[one.level].tab}
              <ChipCount>{heldCount(one.level)}</ChipCount>
            </button>
          ))}
        </div>

        {scoped && scopes.length > 1 && (
          <ChipStrip role="group" aria-label={byDatabase ? "Which database" : "Which schema"}>
            {scopes.map((name) => (
              <FilterChip
                key={name}
                selected={name === currentScope}
                className="font-mono"
                onClick={() => setScope(name)}
              >
                {name}
              </FilterChip>
            ))}
          </ChipStrip>
        )}
        {!byDatabase && level.level !== "database" && (
          <FormNote>
            {LEVEL_WORD[level.level].tab} of <span className="font-mono">{conn.database}</span>, the
            database this connection is on. Another database&rsquo;s are granted from a connection
            to it.
          </FormNote>
        )}

        {loading ? (
          <div className="space-y-2.5 py-1" aria-hidden>
            {[52, 40, 64].map((width) => (
              <Skeleton key={width} className="h-6" style={{ width: `${width}%` }} />
            ))}
          </div>
        ) : failed ? (
          <FormNote tone="danger" role="alert">
            The {LEVEL_WORD[level.level].tab.toLowerCase()} could not be listed:{" "}
            {errorMessage(failed)}
          </FormNote>
        ) : objects.length === 0 ? (
          <FormNote>
            There is no {LEVEL_WORD[level.level].one}
            {scoped ? ` in ${currentScope}` : ""}.
          </FormNote>
        ) : (
          <ul className="divide-y divide-hairline" aria-label={LEVEL_WORD[level.level].tab}>
            {editable && level.allObjects && objects.length > 1 && (
              <li className="flex min-w-0 flex-col gap-1.5 py-2 sm:flex-row sm:items-start sm:gap-3">
                <span className="w-40 shrink-0 pt-1 text-xs font-medium">
                  {LEVEL_WORD[level.level].every}
                </span>
                <span className="flex min-w-0 flex-1 flex-wrap gap-1">
                  {domain.map((privilege) => {
                    const everywhere = objects.every((name) => {
                      const cell = cellOf(name)
                      return (wanted[cellKey(cell)]?.privileges ?? holds(cell).privileges).includes(
                        privilege,
                      )
                    })
                    return (
                      <FilterChip
                        key={privilege}
                        selected={everywhere}
                        className="h-6 px-2 font-mono text-micro"
                        aria-label={`${privilege} on every ${LEVEL_WORD[level.level].one} in ${currentScope}`}
                        onClick={() => pressEvery(privilege)}
                      >
                        {privilege}
                      </FilterChip>
                    )
                  })}
                </span>
              </li>
            )}
            {objects.map((name) => {
              const cell = cellOf(name)
              const held = holds(cell)
              const want = wanted[cellKey(cell)]?.privileges
              const now = want ?? held.privileges
              if (!editable && held.privileges.length === 0 && held.other.length === 0) return null
              const full = domain.every((one) => now.includes(one))
              return (
                <li
                  key={name}
                  data-object={name}
                  className="flex min-w-0 flex-col gap-1.5 py-2 sm:flex-row sm:items-start sm:gap-3"
                >
                  <span className="w-40 shrink-0 truncate pt-1 font-mono text-xs" title={name}>
                    {name}
                  </span>
                  <span className="flex min-w-0 flex-1 flex-wrap items-center gap-1">
                    {editable ? (
                      <>
                        {hasAll && (
                          <FilterChip
                            selected={full}
                            className="h-6 px-2 font-mono text-micro"
                            aria-label={`ALL on ${name}`}
                            onClick={() => pressAll(cell)}
                          >
                            ALL
                          </FilterChip>
                        )}
                        {domain.map((privilege) => {
                          const was = held.privileges.includes(privilege)
                          const is = now.includes(privilege)
                          const owned = held.owned.includes(privilege)
                          return (
                            <FilterChip
                              key={privilege}
                              selected={is}
                              disabled={owned}
                              aria-label={`${privilege} on ${name}`}
                              className={cn(
                                "h-6 px-2 font-mono text-micro",
                                was && !is && "line-through",
                                owned && "opacity-60",
                              )}
                              style={
                                was !== is
                                  ? { color: `var(--git-${is ? "added" : "deleted"})` }
                                  : undefined
                              }
                              onClick={() => press(cell, privilege)}
                            >
                              {privilege}
                            </FilterChip>
                          )
                        })}
                      </>
                    ) : (
                      held.privileges.map((privilege) => (
                        <Tag key={privilege} mono>
                          {privilege}
                        </Tag>
                      ))
                    )}
                    {held.other.map((privilege) => (
                      <Tag
                        key={privilege}
                        mono
                        title="Listed by the server; changed in the console"
                      >
                        {privilege}
                      </Tag>
                    ))}
                    {held.owned.length > 0 && <Tag>owner</Tag>}
                    {held.grantable && <Tag>may grant it on</Tag>}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
        {!editable &&
          !loading &&
          !failed &&
          objects.length > 0 &&
          objects.every((name) => {
            const held = holds(cellOf(name))
            return held.privileges.length === 0 && held.other.length === 0
          }) && (
            <FormNote>
              It holds nothing on a {LEVEL_WORD[level.level].one}
              {scoped ? ` in ${currentScope}` : ""}.
            </FormNote>
          )}
        {later.length > 0 && (
          <FormNote>
            On {LEVEL_WORD[level.level].tab.toLowerCase()} created later in{" "}
            <span className="font-mono">{currentScope}</span> it gets{" "}
            <span className="font-mono">{later.join(", ")}</span>.
          </FormNote>
        )}
        {detail.grantsTruncated && (
          <FormNote tone="warning">
            The server listed the first thousand of its grants; an object further down may hold more
            than is shown.
          </FormNote>
        )}
      </div>

      {pending > 0 && (
        <div
          role="status"
          data-slot="grants-pending"
          className="sticky bottom-0 -mx-4 flex flex-wrap items-center justify-end gap-2 border-t border-hairline bg-popover px-4 py-3"
        >
          <span className="mr-auto text-body font-medium">{pendingWords(wanted, members)}</span>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setWanted({})
              setMembers([])
            }}
          >
            Discard
          </Button>
          <Button size="sm" onClick={() => setReviewing(true)}>
            Review…
          </Button>
        </div>
      )}

      {reviewing && (
        <GrantsReview
          role={detail}
          plan={(options) =>
            plannedRequests(
              wanted,
              holds,
              levels,
              scoped ? { [scopeKey(cellOf(""))]: objects } : {},
              members,
              { host: detail.host, ...options },
            )
          }
          offersGrantOption={Object.values(wanted).some(
            ({ cell }) => levels.find((one) => one.level === cell.level)?.grantOption,
          )}
          offersFuture={Boolean(scoped && level.future)}
          scopeName={currentScope}
          onClose={() => setReviewing(false)}
          onApplied={() => {
            setReviewing(false)
            setWanted({})
            setMembers([])
            onApplied()
          }}
        />
      )}
    </div>
  )
}

type Previewed = { planned: Planned; statements: string[]; notes: string[]; error?: string }

/**
 * The statements a set of grant changes will run, as the server writes them,
 * before any of them runs. Each request is previewed by its own route — none
 * of the SQL is written here — and the command is live once every one has
 * been planned. A set that takes something away says so in the command's
 * colour: a revoke can stop an application.
 */
function GrantsReview({
  role,
  plan,
  offersGrantOption,
  offersFuture,
  scopeName,
  onClose,
  onApplied,
}: {
  role: Pick<DbRoleDetail, "name" | "host">
  plan: (options: { grantOption: boolean; future: boolean }) => Planned[]
  offersGrantOption: boolean
  offersFuture: boolean
  scopeName: string
  onClose: () => void
  onApplied: () => void
}) {
  const { id } = useDatabase()
  const [grantOption, setGrantOption] = useState(false)
  const [future, setFuture] = useState(false)
  const [answered, setAnswered] = useState<{ key: string; previewed: Previewed[] }>()
  const [running, setRunning] = useState(false)
  const [failure, setFailure] = useState<string>()
  const planned = useMemo(
    () => plan({ grantOption, future }),
    // The plan is a function of the two options; the changes do not move while this is open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [grantOption, future],
  )
  const whole = planned.some((one) => one.body.table === "")
  const path = (one: Planned) =>
    `/databases/${id}/server/roles/${encodeURIComponent(role.name)}/privileges${one.action === "revoke" ? "/revoke" : ""}`

  // The answers belong to the plan they were asked for: a plan changed by an
  // option shows "reading" again until its own answers land.
  const planKey = JSON.stringify(planned.map((one) => [one.action, one.body]))
  const previewed = answered?.key === planKey ? answered.previewed : undefined

  useEffect(() => {
    let cancelled = false
    void Promise.all(
      planned.map(async (one): Promise<Previewed> => {
        try {
          const answer = await post<DbPrivilegeResult>(path(one), one.body, {
            query: { preview: 1 },
          })
          return {
            planned: one,
            statements: answer.statements,
            notes: [
              ...(answer.futureOwners && answer.futureOwners.length > 0
                ? [`Covers what ${answer.futureOwners.join(", ")} create later.`]
                : []),
              ...(answer.notes ?? []),
            ],
          }
        } catch (err) {
          return { planned: one, statements: [], notes: [], error: errorMessage(err) }
        }
      }),
    ).then((answers) => {
      if (!cancelled) setAnswered({ key: planKey, previewed: answers })
    })
    return () => {
      cancelled = true
    }
    // `path` is derived from the role and the connection, which do not change here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [planned])

  const revokes = planned.filter((one) => one.action === "revoke").length
  const ready = previewed !== undefined && previewed.every((one) => !one.error)

  const apply = async () => {
    setRunning(true)
    setFailure(undefined)
    let done = 0
    try {
      for (const one of planned) {
        await post<DbPrivilegeResult>(path(one), one.body)
        done += 1
      }
      notify.success(`Applied ${plural(done, "change")} to ${role.name}`)
      onApplied()
    } catch (err) {
      setFailure(
        `${done} of ${planned.length} ran. ${planned[done]?.about ?? "The next one"} was refused: ${errorMessage(err)}`,
      )
      setRunning(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && !running && onClose()}
      size="lg"
      title={`Change what ${role.name} holds`}
      description="The statements the server will run for these grant changes"
      initialFocus="body"
      footer={
        <>
          <p className="mr-auto min-w-0 text-hint text-muted-foreground">
            {plural(planned.length, "request")}
            {revokes > 0 ? `, ${revokes} of them taking something away` : ""}.
          </p>
          <Button variant="outline" onClick={onClose} disabled={running}>
            Cancel
          </Button>
          <Button
            variant={revokes > 0 ? "destructive" : "default"}
            disabled={!ready || planned.length === 0}
            pending={running}
            onClick={() => void apply()}
          >
            Apply
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {(offersGrantOption || (offersFuture && whole)) && (
          <OptionList>
            {offersFuture && whole && (
              <OptionRow
                title={`Also for what is created later in ${scopeName}`}
                checked={future}
                onCheckedChange={setFuture}
              />
            )}
            {offersGrantOption && (
              <OptionRow
                title="Let it grant these on to other accounts"
                checked={grantOption}
                onCheckedChange={setGrantOption}
              />
            )}
          </OptionList>
        )}
        {!previewed ? (
          <p className="flex text-body" role="status">
            <TextShimmer>Reading the statements from the server…</TextShimmer>
          </p>
        ) : (
          previewed.map((one, index) => (
            <div key={index} className="space-y-1.5">
              <Statement
                label={one.planned.about}
                sql={one.statements.join(";\n")}
                placeholder={
                  one.error
                    ? "No statement: the server could not plan this change."
                    : "The server runs no statement for it."
                }
              />
              {one.error && (
                <FormNote tone="danger" role="alert" className="break-words">
                  {one.error}
                </FormNote>
              )}
              {one.notes.map((note) => (
                <FormNote key={note}>{note}</FormNote>
              ))}
            </div>
          ))
        )}
        {failure && (
          <FormNote tone="danger" role="alert" className="break-words">
            {failure}
          </FormNote>
        )}
      </div>
    </Modal>
  )
}
