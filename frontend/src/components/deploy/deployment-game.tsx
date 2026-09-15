"use client"

import { useCallback, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { ArrowRight, Box, Terminal, Users, Warning } from "@/components/icons"
import { Group, Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get, post, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  BlueprintProperty,
  GameConsoleResult,
  GameOverview,
  GamePlayers,
  GameProperties,
} from "@/lib/types"

type ConsoleLine = { id: number; command: string; output: string; failed: boolean; at: string }

/**
 * The game console. It sends one command at a time through the container's own
 * console client and shows what the game said back — deliberately not a shell,
 * and deliberately not a persistent session: the server refuses anything that
 * is not a plain game command, and this field can only produce those.
 */
export function GameConsoleTab({ projectID }: { projectID: number }) {
  const { can } = useAuth()
  const [command, setCommand] = useState("")
  const [busy, setBusy] = useState(false)
  const [lines, setLines] = useState<ConsoleLine[]>([])
  const [history, setHistory] = useState<string[]>([])
  const [historyIndex, setHistoryIndex] = useState(-1)
  const nextID = useRef(0)
  const overview = usePoll(
    (signal) => get<GameOverview>(`/deploy/${projectID}/game`, undefined, signal),
    15000,
    [projectID],
  )

  const send = useCallback(async () => {
    const trimmed = command.trim()
    if (!trimmed || busy) return
    setBusy(true)
    try {
      const result = await post<GameConsoleResult>(`/deploy/${projectID}/game/console`, {
        command: trimmed,
      })
      setLines((previous) =>
        [
          ...previous,
          {
            id: nextID.current++,
            command: result.command,
            output: result.output || "(the server returned no output)",
            failed: result.exitCode !== 0,
            at: result.executedAt,
          },
        ].slice(-200),
      )
      setHistory((previous) =>
        [...previous.filter((entry) => entry !== trimmed), trimmed].slice(-50),
      )
      setHistoryIndex(-1)
      setCommand("")
    } catch (error) {
      notify.error("The console refused that command", error)
    } finally {
      setBusy(false)
    }
  }, [busy, command, projectID])

  const recallHistory = (direction: -1 | 1) => {
    if (history.length === 0) return
    const next =
      historyIndex === -1
        ? direction === -1
          ? history.length - 1
          : -1
        : Math.min(history.length - 1, Math.max(-1, historyIndex + direction))
    setHistoryIndex(next)
    setCommand(next === -1 ? "" : history[next])
  }

  const unavailable = overview.data && overview.data.status !== "available"
  return (
    <Panel>
      <PanelHeader
        title="Console"
        actions={
          overview.data?.containerId && (
            <Button variant="ghost" size="sm" asChild>
              <Link
                href={`/logs?${new URLSearchParams({ source: `docker:${overview.data.containerId}` })}`}
              >
                Open logs <ArrowRight className="size-3" />
              </Link>
            </Button>
          )
        }
      />
      <PanelBody className="space-y-3">
        {overview.error ? (
          <ErrorState error={overview.error} />
        ) : !overview.data ? (
          <LoadingRows rows={3} />
        ) : unavailable ? (
          <Notice title="Console unavailable" icon={Terminal}>
            {overview.data.reason ??
              "This game server has no running container to send commands to."}
          </Notice>
        ) : (
          <>
            <Group
              role="log"
              aria-label="Console transcript"
              aria-live="polite"
              className="max-h-[26rem] min-w-0 space-y-3 overflow-y-auto"
              tinted
            >
              {lines.length === 0 ? (
                <EmptyNote>
                  Nothing sent yet. Try <span className="font-mono">list</span> to see who is
                  online.
                </EmptyNote>
              ) : (
                lines.map((line) => (
                  <div key={line.id} className="min-w-0 space-y-1">
                    <p className="flex min-w-0 flex-wrap items-center gap-2 font-mono text-xs">
                      <span className="text-muted-foreground">&gt;</span>
                      <span className="min-w-0 font-medium break-all">{line.command}</span>
                      {line.failed && <Tag tone="warning">non-zero exit</Tag>}
                      <span className="text-hint text-muted-foreground">
                        {relativeTime(line.at)}
                      </span>
                    </p>
                    <ol className="font-mono text-xs leading-6">
                      {line.output
                        .replace(/\r\n?/g, "\n")
                        .split("\n")
                        .map((text, index) => (
                          <li
                            key={index}
                            className="flex min-w-0 gap-3 rounded-sm px-2 hover:bg-row-hover"
                          >
                            <span
                              aria-hidden="true"
                              className="w-7 shrink-0 text-right text-muted-foreground/60 select-none"
                            >
                              {index + 1}
                            </span>
                            <span className="min-w-0 break-words whitespace-pre-wrap text-muted-foreground">
                              {text || " "}
                            </span>
                          </li>
                        ))}
                    </ol>
                  </div>
                ))
              )}
            </Group>
            {can("service.control") ? (
              <form
                className="flex min-w-0 flex-wrap items-end gap-2"
                onSubmit={(event) => {
                  event.preventDefault()
                  void send()
                }}
              >
                <div className="min-w-0 flex-1 space-y-1.5">
                  <Label htmlFor="game-console-command">Command</Label>
                  <Input
                    id="game-console-command"
                    value={command}
                    autoComplete="off"
                    spellCheck={false}
                    className="font-mono"
                    placeholder="list"
                    onChange={(event) => setCommand(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "ArrowUp") {
                        event.preventDefault()
                        recallHistory(-1)
                      } else if (event.key === "ArrowDown") {
                        event.preventDefault()
                        recallHistory(1)
                      }
                    }}
                  />
                </div>
                <Button type="submit" size="sm" disabled={busy || !command.trim()}>
                  {busy ? "Sending…" : "Send"}
                </Button>
              </form>
            ) : (
              <EmptyNote>Sending commands needs the service control capability.</EmptyNote>
            )}
            <p className="text-hint text-muted-foreground">
              Only plain game commands are accepted. Anything carrying a shell character is refused
              before it reaches the container.
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

const PLAYER_ACTIONS: { action: string; label: string; destructive?: boolean }[] = [
  { action: "op", label: "Make operator" },
  { action: "deop", label: "Remove operator" },
  { action: "whitelist_add", label: "Add to whitelist" },
  { action: "whitelist_remove", label: "Remove from whitelist" },
  { action: "kick", label: "Kick", destructive: true },
  { action: "ban", label: "Ban", destructive: true },
]

/**
 * Player controls appear only when a tested adapter can actually report
 * identities. A game whose reply this dashboard cannot read gets the console
 * and nothing that pretends to be more.
 */
export function GamePlayersTab({ projectID }: { projectID: number }) {
  const { can } = useAuth()
  const [busy, setBusy] = useState("")
  const players = usePoll(
    (signal) => get<GamePlayers>(`/deploy/${projectID}/game/players`, undefined, signal),
    20000,
    [projectID],
  )

  const act = async (action: string, name: string) => {
    setBusy(`${action}:${name}`)
    try {
      await post(`/deploy/${projectID}/game/players/${action}`, { name })
      notify.success(`${name}: ${action.replace("_", " ")} sent`)
      players.refresh()
    } catch (error) {
      notify.error(`Could not ${action.replace("_", " ")} ${name}`, error)
    } finally {
      setBusy("")
    }
  }

  return (
    <Panel>
      <PanelHeader title="Players" />
      <PanelBody className="space-y-3">
        {players.error ? (
          <ErrorState error={players.error} />
        ) : !players.data ? (
          <LoadingRows rows={3} />
        ) : !players.data.supported ? (
          <Notice title="Player list not available" icon={Users}>
            {players.data.reason ??
              "No tested adapter can report player identities for this server. The console remains available."}
          </Notice>
        ) : (
          <>
            {players.data.reason && (
              <Notice title="Partial player list" tone="warning" icon={Warning}>
                {players.data.reason}
              </Notice>
            )}
            {players.data.names.length === 0 ? (
              <EmptyState
                icon={Users}
                title="Nobody is online"
                description={`The server reported ${players.data.online} players connected.`}
                className="border-0 py-6"
              />
            ) : (
              <ul aria-label="Online players" className="divide-y divide-hairline">
                {players.data.names.map((name) => (
                  <li
                    key={name}
                    className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0"
                  >
                    <span className="min-w-0 font-mono text-sm break-all">{name}</span>
                    {can("service.control") && (
                      <div className="flex min-w-0 flex-wrap gap-1.5">
                        {PLAYER_ACTIONS.map((entry) => (
                          <Button
                            key={entry.action}
                            size="xs"
                            variant={entry.destructive ? "outline" : "ghost"}
                            className={entry.destructive ? "text-destructive" : undefined}
                            disabled={busy === `${entry.action}:${name}`}
                            onClick={() => act(entry.action, name)}
                          >
                            {entry.label}
                          </Button>
                        ))}
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}
            <p className="text-hint text-muted-foreground">
              Observed {relativeTime(players.data.observedAt)} by asking the server directly.
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * The server's own settings file. Only the keys the blueprint declares get a
 * control; everything else stays in the raw preview and is never rewritten by
 * this editor.
 */
export function GameSettingsTab({ projectID }: { projectID: number }) {
  const { can } = useAuth()
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const properties = usePoll(
    (signal) => get<GameProperties>(`/deploy/${projectID}/game/properties`, undefined, signal),
    0,
    [projectID],
  )
  const values = useMemo(() => properties.data?.values ?? {}, [properties.data])
  const changed = useMemo(
    () => Object.entries(draft).filter(([key, value]) => (values[key] ?? "") !== value),
    [draft, values],
  )

  const save = async () => {
    if (changed.length === 0) return
    setSaving(true)
    try {
      const result = await put<{ applied: string[]; restartRequired: boolean }>(
        `/deploy/${projectID}/game/properties`,
        { changes: Object.fromEntries(changed) },
      )
      notify.success(
        result.applied.length === 1
          ? `${result.applied[0]} saved`
          : `${result.applied.length} settings saved`,
        result.restartRequired
          ? { description: "Restart the server for them to take effect." }
          : undefined,
      )
      setDraft({})
      properties.refresh()
    } catch (error) {
      notify.error("Could not save these settings", error)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-4">
      <Panel>
        <PanelHeader
          title="Server settings"
          actions={
            can("system.admin") && (
              <Button size="sm" disabled={changed.length === 0 || saving} onClick={save}>
                {saving ? "Saving…" : `Save ${changed.length || ""}`.trim()}
              </Button>
            )
          }
        />
        <PanelBody className="space-y-4">
          {properties.error ? (
            <ErrorState error={properties.error} />
          ) : !properties.data ? (
            <LoadingRows rows={5} />
          ) : properties.data.status !== "available" ? (
            <Notice title="Settings unavailable" icon={Box}>
              {properties.data.reason ?? "This file could not be read from the running server."}
            </Notice>
          ) : properties.data.known.length === 0 ? (
            <EmptyNote>This blueprint declares no settings this dashboard edits.</EmptyNote>
          ) : (
            <>
              {properties.data.restartRequired && changed.length > 0 && (
                <Notice title="A restart is needed" tone="warning" icon={Warning}>
                  The server reads this file on start. Saving writes the change; restarting applies
                  it.
                </Notice>
              )}
              <div className="grid min-w-0 gap-4 sm:grid-cols-2">
                {properties.data.known.map((property) => (
                  <PropertyField
                    key={property.key}
                    property={property}
                    value={draft[property.key] ?? values[property.key] ?? property.default ?? ""}
                    disabled={!can("system.admin")}
                    onChange={(value) =>
                      setDraft((previous) => ({ ...previous, [property.key]: value }))
                    }
                  />
                ))}
              </div>
            </>
          )}
        </PanelBody>
      </Panel>

      {properties.data?.raw && (
        <details className="min-w-0 rounded-lg border border-hairline">
          <summary className="cursor-pointer rounded-lg p-4 text-sm font-medium focus-ring">
            View the file on disk
          </summary>
          <div className="min-w-0 border-t border-hairline">
            <pre className="max-h-[24rem] overflow-auto p-4 font-mono text-hint leading-relaxed whitespace-pre">
              {properties.data.raw}
            </pre>
          </div>
        </details>
      )}
    </div>
  )
}

function PropertyField({
  property,
  value,
  disabled,
  onChange,
}: {
  property: BlueprintProperty
  value: string
  disabled: boolean
  onChange: (value: string) => void
}) {
  const id = `game-property-${property.key}`
  return (
    <div className="min-w-0 space-y-1.5">
      <Label htmlFor={id}>{property.label}</Label>
      {property.kind === "boolean" ? (
        <div className="flex min-h-9 items-center gap-2">
          <Switch
            id={id}
            checked={value === "true"}
            disabled={disabled}
            onCheckedChange={(checked) => onChange(checked ? "true" : "false")}
          />
          <span className="text-xs text-muted-foreground">{value === "true" ? "On" : "Off"}</span>
        </div>
      ) : property.kind === "choice" ? (
        <Select value={value} disabled={disabled} onValueChange={onChange}>
          <SelectTrigger id={id}>
            <SelectValue placeholder="Choose" />
          </SelectTrigger>
          <SelectContent>
            {(property.choices ?? []).map((choice) => (
              <SelectItem key={choice.value} value={choice.value}>
                {choice.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        <Input
          id={id}
          value={value}
          disabled={disabled}
          inputMode={property.kind === "number" ? "numeric" : undefined}
          min={property.minimum}
          max={property.maximum}
          type={property.kind === "number" ? "number" : "text"}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {property.description && (
        <p className="text-hint text-muted-foreground">{property.description}</p>
      )}
      <p className="font-mono text-hint text-muted-foreground">{property.key}</p>
    </div>
  )
}
