"use client"

import { useMemo, useState } from "react"
import { Puzzle } from "@/components/icons"
import { del, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { ChoiceRow } from "@/components/flow"
import { FormFact, FormNote, FormSection } from "@/components/form"
import { SearchInput } from "@/components/page"
import { ProductLogo, hasProductLogo } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { CardsSkeleton, CouldNotRead } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { isDown } from "@/components/database/ops/performance-parts"
import { useDatabase } from "@/components/database/shell/database-context"

/** One extension of `GET /databases/{id}/server/extensions`. */
type DbExtension = {
  name: string
  version?: string
  availableVersion?: string
  installed: boolean
  /** The schema it lives in; for a plugin catalogue, the kind of plugin. */
  schema?: string
  comment?: string
}

type DbExtensions = {
  extensions: DbExtension[]
  supported: boolean
  /** Enabling and disabling work from here. */
  editable?: boolean
  reason?: string
}

/** A loaded module of a key–value server, from `GET /redis/server`. */
type ServerModules = { modules: { name: string; version: number }[] }

/** A run of cards two across where the column has the width. */
function Cards({ children }: { children: React.ReactNode }) {
  return (
    <ul data-slot="choice-list" className="grid min-w-0 gap-2 sm:grid-cols-2">
      {children}
    </ul>
  )
}

/** The extension drawn as itself where its artwork is bundled, else as a part that plugs in. */
function ExtensionMark({ name, dim }: { name: string; dim?: boolean }) {
  return (
    <span className={dim ? "flex opacity-45" : "flex"}>
      <ProductLogo id={hasProductLogo(name) ? name : undefined} size="sm" fallback={Puzzle} />
    </span>
  )
}

/**
 * What the server can be extended with: a SQL engine's extensions — each a
 * card, with the one command that enables or disables it where the engine
 * lets the dashboard do that — or a key–value server's loaded modules.
 *
 * Enabled ones are listed first, as what the database has; the catalogue of
 * what could be enabled is one press and a search away. A catalogue the
 * engine does not change from a connection (its plugins) is read and says so.
 */
export function ExtensionsSection({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { engine } = useDatabase()
  return engine.kind === "keyvalue" ? <Modules /> : <Extensions confirm={confirm} />
}

function Extensions({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { id, conn, engine, readOnly, status } = useDatabase()
  const { can } = useAuth()
  const down = isDown(status.state)
  const [show, setShow] = useState<"enabled" | "available">("enabled")
  const [search, setSearch] = useState("")
  const [busy, setBusy] = useState<string>()
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
  const enabled = useMemo(() => (data?.extensions ?? []).filter((one) => one.installed), [data])
  const available = useMemo(() => (data?.extensions ?? []).filter((one) => !one.installed), [data])
  const mayEnable = can("system.admin") && !readOnly && Boolean(data?.editable)
  const mayDisable = mayEnable && can("destructive")
  const words = search.trim().toLowerCase()
  const shown = (show === "enabled" ? enabled : available).filter(
    (one) =>
      !words ||
      one.name.toLowerCase().includes(words) ||
      one.comment?.toLowerCase().includes(words),
  )

  const enable = async (extension: DbExtension) => {
    setBusy(extension.name)
    try {
      await post(`/databases/${id}/server/extensions`, { name: extension.name })
      notify.success(`Enabled ${extension.name}`, {
        description: `CREATE EXTENSION ran in ${conn.database || conn.name}.`,
      })
      list.refresh()
    } catch (err) {
      notify.error(`Could not enable ${extension.name}`, err)
    } finally {
      setBusy(undefined)
    }
  }

  const disable = (extension: DbExtension) =>
    confirm({
      title: `Disable ${extension.name}`,
      confirmLabel: "Disable",
      subject: {
        mark: <ExtensionMark name={extension.name} />,
        name: <span className="font-mono">{extension.name}</span>,
        facts: (
          <>
            {extension.version && <FormFact label="Version">{extension.version}</FormFact>}
            {extension.schema && (
              <FormFact label="In" mono>
                {extension.schema}
              </FormFact>
            )}
          </>
        ),
      },
      description: (
        <>
          <p>
            Drops the extension from {conn.database || conn.name}, with the functions, types and
            operators it added. Statistics it was collecting are lost.
          </p>
          <p>
            The engine refuses, and changes nothing, while something in the database still depends
            on it.
          </p>
        </>
      ),
      action: async () => {
        await del(`/databases/${id}/server/extensions/${encodeURIComponent(extension.name)}`)
        notify.success(`Disabled ${extension.name}`)
        list.refresh()
        return "reported"
      },
    })

  return (
    <FormSection
      aside
      id="extensions"
      title="Extensions"
      hint={
        data?.supported ? (
          <span className="numeric">
            {enabled.length} enabled
            {available.length > 0 ? ` · ${available.length} more available` : ""}
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
          <CardsSkeleton count={4} className="grid gap-2 sm:grid-cols-2" />
        )
      ) : !data.supported ? (
        <Notice title={`${engine.label} lists no extensions here`}>
          {data.reason ?? "This server has no extension catalogue to read."}
        </Notice>
      ) : (
        <div className="animate-rise space-y-3">
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
            <ChipStrip role="group" aria-label="Which extensions are listed">
              <FilterChip selected={show === "enabled"} onClick={() => setShow("enabled")}>
                Enabled
                <ChipCount>{enabled.length}</ChipCount>
              </FilterChip>
              {available.length > 0 && (
                <FilterChip selected={show === "available"} onClick={() => setShow("available")}>
                  Available
                  <ChipCount>{available.length}</ChipCount>
                </FilterChip>
              )}
            </ChipStrip>
            {(show === "available" ? available : enabled).length > 8 && (
              <SearchInput
                dense
                aria-label="Filter the extensions"
                placeholder="Filter by name or what it does"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                containerClassName="sm:w-64"
              />
            )}
          </div>
          {!data.editable && (
            <FormNote>
              These are the server&rsquo;s plugins. They are loaded from its own configuration, not
              from a connection.
            </FormNote>
          )}
          {shown.length === 0 ? (
            <p className="py-2 text-body text-muted-foreground">
              {words
                ? "No extension matches."
                : show === "enabled"
                  ? "No extension is enabled in this database."
                  : "Nothing more is available on this server."}
            </p>
          ) : (
            <Cards>
              {shown.map((extension) => {
                const newer =
                  extension.installed &&
                  extension.availableVersion &&
                  extension.availableVersion !== extension.version
                    ? extension.availableVersion
                    : undefined
                return (
                  <ChoiceRow
                    // A plugin catalogue lists one name once per kind of plugin.
                    key={`${extension.schema ?? ""}:${extension.name}`}
                    disabled
                    verb={extension.name}
                    leading={<ExtensionMark name={extension.name} dim={!extension.installed} />}
                    title={<span className="font-mono text-xs">{extension.name}</span>}
                    description={
                      extension.comment || (extension.schema ? `in ${extension.schema}` : undefined)
                    }
                    trailing={
                      <>
                        {newer && <Tag tone="warning">{newer} available</Tag>}
                        {(extension.version ?? extension.availableVersion) && (
                          <Tag mono>{extension.version ?? extension.availableVersion}</Tag>
                        )}
                      </>
                    }
                    actions={
                      extension.installed ? (
                        mayDisable && (
                          <Button
                            size="xs"
                            variant="ghost"
                            aria-label={`Disable ${extension.name}`}
                            onClick={() => disable(extension)}
                          >
                            Disable
                          </Button>
                        )
                      ) : mayEnable ? (
                        <Button
                          size="xs"
                          variant="outline"
                          aria-label={`Enable ${extension.name}`}
                          pending={busy === extension.name}
                          disabled={busy !== undefined}
                          onClick={() => void enable(extension)}
                        >
                          Enable
                        </Button>
                      ) : undefined
                    }
                  />
                )
              })}
            </Cards>
          )}
        </div>
      )}
    </FormSection>
  )
}

/** A key–value server's loaded modules. They are loaded by its configuration, so this is a reading. */
function Modules() {
  const { id, conn, status } = useDatabase()
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
    <FormSection
      aside
      id="extensions"
      title="Modules"
      hint={
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
          <CardsSkeleton count={2} className="grid gap-2 sm:grid-cols-2" />
        )
      ) : modules.length === 0 ? (
        <p className="animate-rise text-body leading-relaxed text-muted-foreground">
          No module is loaded: the server has its built-in types only. A module — JSON, search, time
          series — is loaded with <span className="font-mono">loadmodule</span> in the
          server&rsquo;s configuration, or by running an image that carries it.
        </p>
      ) : (
        <div className="animate-rise space-y-3">
          <Cards>
            {modules.map((module) => (
              <ChoiceRow
                key={module.name}
                disabled
                verb={module.name}
                leading={<ExtensionMark name={module.name} />}
                title={<span className="font-mono text-xs">{module.name}</span>}
                trailing={<Tag mono>{module.version}</Tag>}
              />
            ))}
          </Cards>
          <FormNote>
            Modules are loaded by the server&rsquo;s configuration, not from here.
          </FormNote>
        </div>
      )}
    </FormSection>
  )
}
