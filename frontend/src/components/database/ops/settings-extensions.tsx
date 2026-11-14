"use client"

import { useMemo, useState } from "react"
import { Plus, Puzzle, Trash } from "@/components/icons"
import { plural } from "@/lib/format"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { GroupRule } from "@/components/flow"
import { FormNote } from "@/components/form"
import { SearchInput } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { LoadingRows, Notice } from "@/components/state"
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
import { VerbMenu } from "@/components/verbs"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
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
  libraryState,
  needsLoading,
  OTHER,
  shownExtensions,
  waitingLibraries,
  type DbExtension,
  type DbExtensions,
  type ExtensionShow,
} from "@/components/database/ops/extensions-model"
import { isDown } from "@/components/database/ops/performance-parts"
import { usePendingReport } from "@/components/database/ops/settings-pending"
import { useDatabase } from "@/components/database/shell/database-context"

/** A loaded module of a key–value server, from `GET /redis/server`. */
type ServerModules = { modules: { name: string; version: number }[] }

/** How many of a long catalogue are listed before the rest wait behind a press. */
const LISTED = 12

/** The value the category filter carries for every category, which Radix will not take as "". */
const EVERY_CATEGORY = "__every_category__"

/**
 * One extension or module on one line, at one height whatever is true of it:
 * led by what it is (its project's mark where one is bundled, the engine's
 * own for its plugins and modules, and the glyph of what it does for the
 * rest), then its name as the engine spells it with its version, over one
 * line that says what it needs to work, where it lives and what it does. Its
 * commands are at the row's end: the one it is waiting for as a button, and
 * the one that removes it behind the menu.
 *
 * A row used to be two lines or three, by whether it had a library to load.
 */
function ExtensionRow({
  name,
  mark,
  version,
  newer,
  where,
  about,
  state,
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
  /** What it needs before it works, or why it cannot be had here, in a few words. */
  state?: { text: string; tone?: "warning" }
  action?: React.ReactNode
}) {
  const said = about ? about.charAt(0).toUpperCase() + about.slice(1) : undefined
  return (
    <li data-extension={name} className="flex h-14 min-w-0 items-center gap-3.5">
      {mark}
      <div className="min-w-0 flex-1 space-y-0.5">
        <p className="flex min-w-0 items-center gap-x-2">
          <span className="min-w-0 truncate font-mono text-body font-medium">{name}</span>
          {version !== undefined && version !== "" && (
            <Tag mono className="shrink-0">
              {version}
            </Tag>
          )}
          {newer && (
            <Tag tone="warning" className="shrink-0">
              {newer} available
            </Tag>
          )}
        </p>
        <p
          className="truncate text-xs text-muted-foreground"
          title={[state?.text, where && `in ${where}`, said].filter(Boolean).join(" · ")}
        >
          {state && (
            <>
              <span
                data-slot="extension-facts"
                className={state.tone === "warning" ? "text-warning" : undefined}
              >
                {state.text}
              </span>
              {(where || said) && " · "}
            </>
          )}
          {where && `in ${where}`}
          {where && said && " · "}
          {said}
        </p>
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
 *
 * What the server loads at its next start, where that differs from what it
 * loaded at this one, is on the strip at the head of the page with Restart
 * beside it (`settings-pending.tsx`): each library named, as added or removed.
 */
export function ExtensionsSection() {
  const { engine } = useDatabase()
  return engine.kind === "keyvalue" ? <Modules /> : <Extensions />
}

type Opened = { kind: "enable" | "update" | "disable" | "preload"; extension: DbExtension }

function Extensions() {
  const { id, conn, engine, readOnly, status } = useDatabase()
  const { can } = useAuth()
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

  // The libraries behind the one parameter that waits: named, not the parameter.
  const libraries = waiting
    ? [
        ...waiting.adding.map((name) => ({ name, mono: true, state: "added" })),
        ...waiting.removing.map((name) => ({ name, mono: true, state: "removed" })),
      ]
    : []
  usePendingReport("extensions", {
    waiting: libraries,
    covers: libraries.length > 0 ? ["shared_preload_libraries"] : [],
    refresh: list.refresh,
  })

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
            className="rounded-full px-3"
            aria-label={`Update ${extension.name}`}
            onClick={() => setOpened({ kind: "update", extension })}
          >
            Update
          </Button>
        )}
        {mayDisable && (
          <VerbMenu
            label={`More for ${extension.name}`}
            verbs={[
              {
                key: "disable",
                label: "Disable",
                icon: Trash,
                danger: true,
                run: () => setOpened({ kind: "disable", extension }),
              },
            ]}
          />
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
                          state={libraryState(extension, preload)}
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
