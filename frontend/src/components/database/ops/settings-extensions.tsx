"use client"

import { useMemo, useState } from "react"
import { Plus, Puzzle } from "@/components/icons"
import { plural } from "@/lib/format"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { GroupRule } from "@/components/flow"
import { FormNote } from "@/components/form"
import { SearchInput } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { CouldNotRead } from "@/components/database/home/blocks"
import { powerConfirmation } from "@/components/database/home/power-subject"
import { read } from "@/components/database/home/read"
import { usePower } from "@/components/database/home/verbs"
import {
  CategoryGlyph,
  DisableExtension,
  EnableExtension,
  ExtensionMark,
  UpdateExtension,
} from "@/components/database/ops/extension-dialogs"
import { PreloadExtension } from "@/components/database/ops/extension-preload"
import {
  extensionCategories,
  extensionCounts,
  groupExtensions,
  hasUpdate,
  listWords,
  needsLoading,
  OTHER,
  shownExtensions,
  waitingLibraries,
  type DbExtension,
  type DbExtensions,
  type ExtensionShow,
  type PreloadState,
} from "@/components/database/ops/extensions-model"
import { isDown } from "@/components/database/ops/performance-parts"
import { useDatabase } from "@/components/database/shell/database-context"

/** A loaded module of a key–value server, from `GET /redis/server`. */
type ServerModules = { modules: { name: string; version: number }[] }

/** How many of a long catalogue are listed before the rest wait behind a press. */
const LISTED = 12

/** The value the category filter carries for every category, which Radix will not take as "". */
const EVERY_CATEGORY = "__every_category__"

/**
 * One extension or module as a row you read: led by what it is — its
 * project's mark where one is bundled, the engine's own for its plugins and
 * modules, and the glyph of what it does for the rest — then its name as the
 * engine spells it, its version, the sentence that says what it does, what it
 * needs to work, and its commands at the row's edge.
 */
function ExtensionRow({
  name,
  mark,
  version,
  newer,
  where,
  about,
  facts,
  action,
}: {
  name: string
  mark: React.ReactNode
  version?: string | number
  /** A later version the server could update it to. */
  newer?: string
  /** The schema it lives in. */
  where?: string
  about?: string
  /** What it needs before it works, or why it cannot be had here, as a line of hint. */
  facts?: { text: string; tone?: "warning" }
  action?: React.ReactNode
}) {
  return (
    <li data-extension={name} className="flex min-w-0 items-center gap-3.5 py-3">
      {mark}
      <div className="min-w-0 flex-1 space-y-1">
        <p className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-0.5">
          <span className="min-w-0 font-mono text-body font-medium wrap-anywhere">{name}</span>
          {version !== undefined && version !== "" && <Tag mono>{version}</Tag>}
          {newer && <Tag tone="warning">{newer} available</Tag>}
          {where && <span className="text-hint text-muted-foreground">in {where}</span>}
        </p>
        {about && (
          <p className="line-clamp-2 text-xs leading-relaxed text-muted-foreground first-letter:uppercase">
            {about}
          </p>
        )}
        {facts && (
          <FormNote tone={facts.tone} data-slot="extension-facts">
            {facts.text}
          </FormNote>
        )}
      </div>
      {action && <div className="flex shrink-0 items-center gap-1">{action}</div>}
    </li>
  )
}

/**
 * What the server can be extended with: a SQL engine's extensions — each a
 * row, with the commands that enable, update or disable it where the engine
 * lets the dashboard do that — or a key–value server's loaded modules.
 *
 * Enabled ones are listed first, as what the database has; what could be
 * enabled, what has a newer version and what the server's image does not
 * ship are each a chip away, grouped by what they are for, and searchable. A
 * catalogue the engine does not change from a connection (its plugins) is
 * read and says so.
 *
 * They are rows, not cards: nothing here is opened or taken, each is read and
 * has its commands (§15 pass 3).
 */
export function ExtensionsSection({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { engine } = useDatabase()
  return engine.kind === "keyvalue" ? <Modules /> : <Extensions confirm={confirm} />
}

type Opened = { kind: "enable" | "update" | "disable" | "preload"; extension: DbExtension }

/** What an extension needs before it works, read off what the server loads. */
function libraryFacts(
  extension: DbExtension,
  preload: PreloadState | undefined,
): { text: string; tone?: "warning" } | undefined {
  if (extension.unavailable) return { text: "Not in this server's image." }
  const library = extension.library
  if (!library || !preload) return undefined
  if (preload.loaded.includes(library)) {
    return {
      text: extension.module
        ? "Loaded when the server starts."
        : `The server loads ${library} when it starts.`,
    }
  }
  if (preload.configured.includes(library)) {
    return { text: `${library} is loaded once the server restarts.`, tone: "warning" }
  }
  if (extension.installed) {
    return {
      text: `Created, and idle: the server does not load ${library} when it starts.`,
      tone: "warning",
    }
  }
  return { text: `Needs ${library} loaded when the server starts, and a restart.` }
}

function Extensions({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { id, conn, engine, readOnly, status, summary } = useDatabase()
  const { can } = useAuth()
  const power = usePower()
  const down = isDown(status.state)
  const [show, setShow] = useState<ExtensionShow>("enabled")
  const [search, setSearch] = useState("")
  const [category, setCategory] = useState("")
  const [all, setAll] = useState(false)
  const [opened, setOpened] = useState<Opened>()
  const list = usePoll(
    (signal) =>
      read<DbExtensions>(
        `/databases/${id}/server/extensions`,
        (answer) => Array.isArray(answer.extensions),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: !down },
  )

  const data = list.data
  const preload = data?.preload
  const extensions = useMemo(() => data?.extensions ?? [], [data])
  const counts = useMemo(() => extensionCounts(extensions), [extensions])
  const inView = useMemo(() => shownExtensions(extensions, show, "", ""), [extensions, show])
  const categories = extensionCategories(inView)
  // Whether the search and the category filter are drawn is the whole list's
  // to say, not the chip's: the controls stay where they are from one chip
  // to the next.
  const searchable = extensions.length > 8
  const categorised = useMemo(() => extensionCategories(extensions).length > 1, [extensions])
  const shown = shownExtensions(extensions, show, search, category)
  const narrowed = search.trim() !== "" || category !== ""
  const listed = all || narrowed ? shown : shown.slice(0, LISTED)
  const mayEnable = can("system.admin") && !readOnly && Boolean(data?.editable)
  const mayDisable = mayEnable && can("destructive")
  const waiting = preload?.pendingRestart ? waitingLibraries(preload) : undefined

  const choose = (next: ExtensionShow) => {
    setShow(next)
    setAll(false)
    setCategory("")
  }

  const restartNow = () => {
    if (!summary) return
    confirm({
      ...powerConfirmation("restart", engine, summary),
      action: async () => {
        await power.run("restart")
        list.refresh()
        return "reported"
      },
    })
  }

  const actions = (extension: DbExtension) => {
    if (extension.unavailable) return undefined
    if (!extension.installed) {
      if (!mayEnable || (extension.library && !preload)) return undefined
      return (
        <Button
          size="xs"
          variant="outline"
          aria-label={`Enable ${extension.name}`}
          onClick={() =>
            setOpened({
              kind: needsLoading(extension, preload) ? "preload" : "enable",
              extension,
            })
          }
        >
          <Plus />
          Enable
        </Button>
      )
    }
    if (extension.module || !(mayDisable || (mayEnable && hasUpdate(extension)))) return undefined
    return (
      <>
        {mayEnable && hasUpdate(extension) && (
          <Button
            size="xs"
            variant="outline"
            aria-label={`Update ${extension.name}`}
            onClick={() => setOpened({ kind: "update", extension })}
          >
            Update
          </Button>
        )}
        {mayDisable && (
          <Button
            size="xs"
            variant="ghost"
            aria-label={`Disable ${extension.name}`}
            onClick={() => setOpened({ kind: "disable", extension })}
          >
            Disable
          </Button>
        )}
      </>
    )
  }

  const close = () => setOpened(undefined)
  const changed = () => list.refresh()

  return (
    <SettingSection
      id="extensions"
      title="Extensions"
      state={
        data?.supported ? (
          <span className="numeric">
            {counts.enabled} enabled
            {counts.available > 0 ? ` · ${counts.available} more available` : ""}
            {counts.updates > 0 ? ` · ${plural(counts.updates, "update")} available` : ""}
          </span>
        ) : undefined
      }
      status={waiting && <Status verdict="warning" label="Waiting for a restart" />}
    >
      {down ? (
        <FormNote>
          Extensions are read from the running server; they are listed once {conn.name} answers
          again.
        </FormNote>
      ) : !data ? (
        list.error ? (
          <CouldNotRead what="the extensions" error={list.error} onRetry={list.refresh} />
        ) : (
          <LoadingRows rows={4} />
        )
      ) : !data.supported ? (
        <Notice title={`${engine.label} lists no extensions here`}>
          {data.reason ?? "This server has no extension catalogue to read."}
        </Notice>
      ) : (
        <div className="animate-rise space-y-3">
          {waiting && (
            <Notice
              tone="warning"
              title={
                waiting.adding.length > 0
                  ? `${listWords(waiting.adding)} ${waiting.adding.length === 1 ? "is" : "are"} loaded once ${conn.name} restarts`
                  : `What ${conn.name} loads at start changes when it restarts`
              }
            >
              <p>
                {waiting.removing.length > 0 &&
                  `It stops loading ${listWords(waiting.removing)} then. `}
                The server reads shared_preload_libraries only when it starts.
                {!power.can.restart &&
                  (summary?.power.restart
                    ? " Restarting it is for a role that may interrupt it."
                    : ` ${summary?.power.reason ?? "Nothing here can restart it."}`)}
              </p>
              {power.can.restart && (
                <Button
                  size="xs"
                  variant="outline"
                  className="mt-2"
                  disabled={Boolean(power.change)}
                  onClick={restartNow}
                >
                  Restart {conn.name}
                </Button>
              )}
            </Notice>
          )}
          <div className="space-y-2">
            <ChipStrip role="group" aria-label="Which extensions are listed">
              <FilterChip selected={show === "enabled"} onClick={() => choose("enabled")}>
                Enabled
                <ChipCount>{counts.enabled}</ChipCount>
              </FilterChip>
              {counts.available > 0 && (
                <FilterChip selected={show === "available"} onClick={() => choose("available")}>
                  Available
                  <ChipCount>{counts.available}</ChipCount>
                </FilterChip>
              )}
              {counts.updates > 0 && (
                <FilterChip selected={show === "updates"} onClick={() => choose("updates")}>
                  Update available
                  <ChipCount>{counts.updates}</ChipCount>
                </FilterChip>
              )}
              {counts.missing > 0 && (
                <FilterChip selected={show === "missing"} onClick={() => choose("missing")}>
                  Not in this image
                  <ChipCount>{counts.missing}</ChipCount>
                </FilterChip>
              )}
            </ChipStrip>
            {(searchable || categorised) && (
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                {searchable && (
                  <SearchInput
                    aria-label="Filter the extensions"
                    placeholder="Filter by name or what it does"
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                  />
                )}
                {categorised && (
                  <Select
                    value={category || EVERY_CATEGORY}
                    onValueChange={(next) => {
                      setCategory(next === EVERY_CATEGORY ? "" : next)
                      setAll(false)
                    }}
                  >
                    <SelectTrigger
                      size="sm"
                      aria-label="Filter by category"
                      className="w-auto min-w-44"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={EVERY_CATEGORY}>Every category</SelectItem>
                      {categories.map((one) => (
                        <SelectItem key={one} value={one}>
                          {one}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
              </div>
            )}
          </div>
          {!data.editable && (
            <FormNote>
              These are the server&rsquo;s plugins. They are loaded from its own configuration, not
              from a connection.
            </FormNote>
          )}
          {show === "missing" && (
            <FormNote>
              The dashboard knows these and this server&rsquo;s image does not ship them. One is
              enabled here once the server runs an image that carries it.
            </FormNote>
          )}
          {shown.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              {narrowed
                ? "No extension matches."
                : show === "enabled"
                  ? "No extension is enabled in this database."
                  : show === "updates"
                    ? "Every extension is at the server's default version."
                    : "Nothing more is available on this server."}
            </p>
          ) : (
            <>
              <div className="space-y-4">
                {groupExtensions(listed).map((group, _, groups) => (
                  <div key={group.category}>
                    {groups.length > 1 && (
                      <GroupRule
                        leading={
                          <CategoryGlyph
                            category={group.category === OTHER ? undefined : group.category}
                            className="size-3.5 text-muted-foreground"
                          />
                        }
                        label={group.category}
                        count={
                          shown.filter((one) => (one.category ?? OTHER) === group.category).length
                        }
                      />
                    )}
                    <ul
                      className="divide-y divide-hairline"
                      aria-label={groups.length > 1 ? group.category : "Extensions"}
                    >
                      {group.extensions.map((extension) => (
                        <ExtensionRow
                          // A plugin catalogue lists one name once per kind of plugin.
                          key={`${extension.schema ?? ""}:${extension.name}`}
                          name={extension.name}
                          mark={
                            data.editable ? (
                              <ExtensionMark extension={extension} size="md" />
                            ) : (
                              <ProductLogo id={engine.logo} size="md" fallback={Puzzle} />
                            )
                          }
                          version={extension.version ?? extension.availableVersion}
                          newer={hasUpdate(extension) ? extension.availableVersion : undefined}
                          where={extension.installed ? extension.schema : undefined}
                          about={extension.about ?? extension.comment}
                          facts={libraryFacts(extension, preload)}
                          action={actions(extension)}
                        />
                      ))}
                    </ul>
                  </div>
                ))}
              </div>
              {!narrowed && shown.length > LISTED && (
                <Button
                  size="xs"
                  variant="ghost"
                  className="-ml-2"
                  aria-expanded={all}
                  onClick={() => setAll(!all)}
                >
                  {all ? "Show fewer" : `Show all ${shown.length}`}
                </Button>
              )}
            </>
          )}
        </div>
      )}
      {opened?.kind === "enable" && (
        <EnableExtension extension={opened.extension} onClose={close} onDone={changed} />
      )}
      {opened?.kind === "update" && (
        <UpdateExtension extension={opened.extension} onClose={close} onDone={changed} />
      )}
      {opened?.kind === "disable" && (
        <DisableExtension extension={opened.extension} onClose={close} onDone={changed} />
      )}
      {opened?.kind === "preload" && preload && (
        <PreloadExtension
          // The row as the server last read it: a module is enabled once loaded.
          extension={
            extensions.find((one) => one.name === opened.extension.name) ?? opened.extension
          }
          preload={preload}
          onClose={close}
          onChanged={changed}
        />
      )}
    </SettingSection>
  )
}

/** A key–value server's loaded modules. They are loaded by its configuration, so this is a reading. */
function Modules() {
  const { id, conn, engine, status } = useDatabase()
  const down = isDown(status.state)
  const server = usePoll(
    (signal) =>
      read<ServerModules>(
        `/databases/${id}/redis/server`,
        (answer) => Array.isArray(answer.modules),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: !down },
  )
  const modules = server.data?.modules
  return (
    <SettingSection
      id="extensions"
      title="Modules"
      state={
        modules ? (
          <span className="numeric">{plural(modules.length, "module")} loaded</span>
        ) : undefined
      }
    >
      {down ? (
        <FormNote>
          Modules are read from the running server; they are listed once {conn.name} answers again.
        </FormNote>
      ) : !modules ? (
        server.error ? (
          <CouldNotRead what="the modules" error={server.error} onRetry={server.refresh} />
        ) : (
          <LoadingRows rows={2} />
        )
      ) : modules.length === 0 ? (
        <p className="animate-rise text-body leading-relaxed text-muted-foreground">
          No module is loaded: the server has its built-in types only. A module — JSON, search, time
          series — is loaded with <span className="font-mono">loadmodule</span> in the
          server&rsquo;s configuration, or by running an image that carries it.
        </p>
      ) : (
        <div className="animate-rise space-y-3">
          <ul className="divide-y divide-hairline" aria-label="Modules">
            {modules.map((module) => (
              <ExtensionRow
                key={module.name}
                name={module.name}
                mark={<ProductLogo id={engine.logo} size="md" fallback={Puzzle} />}
                version={module.version}
              />
            ))}
          </ul>
          <FormNote>
            Modules are loaded by the server&rsquo;s configuration, not from here.
          </FormNote>
        </div>
      )}
    </SettingSection>
  )
}
