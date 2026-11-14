"use client"

import { useEffect, useState } from "react"
import { Logout, Shield, ShieldOff, Slash, UserMinus, UserPlus } from "@/components/icons"
import { get, post } from "@/lib/api"
import { clockMinute, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useSessionState } from "@/lib/view-state"
import type { GamePlayers as GamePlayersResponse } from "@/lib/types"
import { useArrivals } from "@/hooks/use-arrivals"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact, InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbActions, type Verb } from "@/components/verbs"
import { EmptyStep, ProjectEmpty } from "@/components/deploy/project-empty"
import {
  GameIdentity,
  GameIdentitySkeleton,
  useGameOverview,
} from "@/components/deploy/game/identity"
import { PlayerFace } from "@/components/deploy/game/player-face"
import {
  observePlayers,
  sameRecord,
  type PlayerRecord,
  type PlayerSighting,
} from "@/components/deploy/game/players-model"

type PlayerAction = "op" | "deop" | "whitelist_add" | "whitelist_remove" | "kick" | "ban"

/** What each action does to a player, for the menu and the toast after it. */
const ACTIONS: Record<PlayerAction, { label: string; progressive: string; done: string }> = {
  op: {
    label: "Make operator",
    progressive: "Promoting",
    done: "is now an operator",
  },
  deop: {
    label: "Remove operator",
    progressive: "Demoting",
    done: "is no longer an operator",
  },
  whitelist_add: {
    label: "Add to whitelist",
    progressive: "Whitelisting",
    done: "was added to the whitelist",
  },
  whitelist_remove: {
    label: "Remove from whitelist",
    progressive: "Removing",
    done: "was removed from the whitelist",
  },
  kick: {
    label: "Kick",
    progressive: "Kicking",
    done: "was kicked",
  },
  ban: {
    label: "Ban",
    progressive: "Banning",
    done: "was banned",
  },
}

/**
 * Who is on the server, each drawn the way their game draws them — the head,
 * in the hue their name takes in the game console's replies — with how long
 * the page has seen them on in a column of its own, and the moderation verbs
 * declared once (§13): kicking is the one pressed daily and stays inline, the
 * rest go behind one menu, and a ban asks first. While an action is on its way
 * the row says so in the present tense, in the column the reading was in,
 * until the server's next answer lands.
 *
 * Player controls appear only when a tested adapter can actually report
 * identities. A game whose reply this dashboard cannot read gets the console
 * and nothing that pretends to be more.
 */
export function GamePlayers({ projectId }: { projectId: number }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  // One entry per player, so an answer for one never clears another's.
  // `sent` marks an action the server took, waiting for the list to show it.
  const [pending, setPending] = useState<Record<string, { action: PlayerAction; sent: boolean }>>(
    {},
  )
  const overview = useGameOverview(projectId)
  const players = usePoll(
    (signal) => get<GamePlayersResponse>(`/deploy/${projectId}/game/players`, undefined, signal),
    20000,
    [projectId],
  )
  // A list that landed after an action was taken is the one that shows it,
  // so that is when its row stops saying "Kicking…" — not when the request
  // returned, which left the row still for a moment and then jumped. Adjusted
  // during render, as `useArrivals` is: no effect and no second paint.
  const [answered, setAnswered] = useState(players.data)
  if (players.data !== answered) {
    setAnswered(players.data)
    if (Object.values(pending).some((entry) => entry.sent))
      setPending((previous) =>
        Object.fromEntries(Object.entries(previous).filter(([, entry]) => !entry.sent)),
      )
  }
  const names = players.data?.names ?? []
  const arrived = useArrivals(names)
  const address = overview.data?.address
  const blueprintId = overview.data?.blueprintId
  // Since when the page has had each player in an answer; see players-model.
  const [record, setRecord] = useSessionState<PlayerRecord | undefined>(
    `deploy.${projectId}.game.players.seen`,
    undefined,
  )
  const answer = players.data?.supported ? players.data : undefined
  useEffect(() => {
    if (!answer) return
    const next = observePlayers(record, answer.names, answer.observedAt)
    if (!sameRecord(record, next)) setRecord(next)
  }, [answer, record, setRecord])

  const act = async (action: PlayerAction, name: string) => {
    setPending((previous) => ({ ...previous, [name]: { action, sent: false } }))
    try {
      await post(`/deploy/${projectId}/game/players/${action}`, { name })
      notify.success(`${name} ${ACTIONS[action].done}`)
      setPending((previous) => ({ ...previous, [name]: { action, sent: true } }))
      players.refresh()
      overview.refresh()
    } catch (error) {
      notify.error(`${ACTIONS[action].label} did not reach ${name}`, error)
      setPending((previous) =>
        Object.fromEntries(Object.entries(previous).filter(([key]) => key !== name)),
      )
    }
  }

  const verbsFor = (name: string): Verb[] => {
    const verb = (action: PlayerAction, icon: Verb["icon"], extra: Partial<Verb> = {}): Verb => ({
      key: action,
      label: ACTIONS[action].label,
      progressive: ACTIONS[action].progressive,
      icon,
      disabled: name in pending,
      run: () => void act(action, name),
      ...extra,
    })
    return [
      verb("kick", Logout, { inline: true, danger: true }),
      verb("op", Shield, { group: "Operator" }),
      verb("deop", ShieldOff, { group: "Operator" }),
      verb("whitelist_add", UserPlus, { group: "Whitelist" }),
      verb("whitelist_remove", UserMinus, { group: "Whitelist" }),
      verb("ban", Slash, {
        danger: true,
        run: () =>
          confirm({
            title: "Ban this player",
            subject: {
              mark: <PlayerFace name={name} blueprintId={blueprintId} />,
              name,
              facts: address && (
                <FormFact label="Server" mono>
                  {address}
                </FormFact>
              ),
            },
            description:
              "Disconnects them and refuses them until they are pardoned from the console.",
            confirmLabel: "Ban",
            action: () => act("ban", name),
          }),
      }),
    ]
  }

  return (
    <div className="space-y-6">
      {overview.data ? (
        <GameIdentity overview={overview.data} projectId={projectId} onChanged={overview.refresh} />
      ) : (
        !overview.error && <GameIdentitySkeleton />
      )}
      <Panel plain>
        <PanelHeader
          title="Players"
          actions={
            players.data?.supported && (
              <span className="flex items-center gap-3 text-hint text-muted-foreground">
                {/* A list the game could only partly give says so here, in
                    the line that says when it was read. */}
                {players.data.reason && (
                  <span className="flex items-center gap-1.5">
                    <Status tone="warning" label="Partial list" />
                    <InfoTip label="Why the list is partial">{players.data.reason}</InfoTip>
                  </span>
                )}
                <span>observed {relativeTime(players.data.observedAt)}</span>
              </span>
            )
          }
        />
        <PanelBody>
          {players.error && !players.data ? (
            <ErrorState error={players.error} onRetry={players.refresh} />
          ) : !players.data ? (
            <LoadingRows rows={3} />
          ) : !players.data.supported ? (
            <ProjectEmpty
              title={
                players.data.status === "unavailable"
                  ? "No running server to ask who is on"
                  : "This game cannot report who is online"
              }
              reason={players.data.reason}
            >
              <EmptyStep href={`/deploy/${projectId}/console`}>Console</EmptyStep>
            </ProjectEmpty>
          ) : names.length === 0 ? (
            <ProjectEmpty
              title="Nobody is online"
              reason={
                <>
                  Up to {players.data.maximum} can join
                  {address && (
                    <>
                      {" "}
                      at <span className="font-mono text-foreground">{address}</span>
                    </>
                  )}
                  .
                </>
              }
            />
          ) : (
            <RowList aria-label="Online players">
              {names.map((name) => {
                const seen = <SeenFor sighting={record?.players[name]} />
                return (
                  <Row
                    key={name}
                    className={
                      arrived.has(name)
                        ? "group animate-rise transition-colors hover:bg-row-hover"
                        : "group transition-colors hover:bg-row-hover"
                    }
                    leading={<PlayerFace name={name} blueprintId={blueprintId} />}
                    title={name}
                    // On a phone the reading is the row's second line; from
                    // `sm` it is a column, so every player's reads down.
                    subtitle={<span className="sm:hidden">{seen}</span>}
                    trailing={
                      // What is happening to the player replaces the reading
                      // rather than joining it, so the row keeps its length.
                      pending[name] ? (
                        <Status
                          tone="warning"
                          label={`${ACTIONS[pending[name].action].progressive}…`}
                          className="justify-end sm:w-36"
                        />
                      ) : (
                        <span className="numeric w-36 text-right text-hint text-muted-foreground max-sm:hidden">
                          {seen}
                        </span>
                      )
                    }
                  >
                    {can("service.control") && (
                      <VerbActions dim verbs={verbsFor(name)} menuLabel={`Actions for ${name}`} />
                    )}
                  </Row>
                )
              })}
            </RowList>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </div>
  )
}

/**
 * How long the page has seen a player on: "joined 3m ago" for one it saw
 * arrive, "on before 14:02" for one who was already there when it first
 * asked — the game does not say when anybody joined.
 */
function SeenFor({ sighting }: { sighting?: PlayerSighting }) {
  if (!sighting) return null
  return (
    <time dateTime={sighting.at}>
      {sighting.arrived
        ? `joined ${relativeTime(sighting.at)}`
        : `on before ${clockMinute(sighting.at)}`}
    </time>
  )
}
