"use client"

import { useState } from "react"
import { Cross, Plus } from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { TailscaleView } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { InfoTip } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { Segments } from "@/components/deploy/settings/segments"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  NO_OFFER_DRAFT,
  offerChanges,
  offerRequest,
  routeProblem,
  routesReading,
  type OfferDraft,
} from "./tunnel-reading"

type Offers = Pick<TailscaleView["prefs"], "advertiseRoutes" | "advertisingExitNode">

const offersOf = (prefs: TailscaleView["prefs"]): Offers => ({
  advertiseRoutes: prefs.advertiseRoutes,
  advertisingExitNode: prefs.advertisingExitNode,
})

/**
 * What this server does for the tailnet, as settings: whether it is offered
 * as an exit node, and the networks behind it the tailnet may reach through
 * it. They are the only things about Tailscale that change here. Using
 * another node as an exit, shields up and logging out are not offered,
 * because each can cut off the browser that is reading this page.
 *
 * They were a switch and a row of chips that each wrote the moment they were
 * pressed. They are one form on the page's save bar now: the choice and the
 * list are edited, the bar counts the edits, and Save (the bar's, Enter in
 * the field, or the + beside it) writes them in one request.
 *
 * A save is worked out against the preferences as they are at that moment,
 * read again first: a lost answer may have accepted the last attempt, and
 * the poll may not have caught up. A refusal keeps every edit and offers the
 * save again.
 *
 * Withdrawing a route or the exit-node offer is a destructive route, so an
 * account without that capability can add and cannot take away.
 */
export function TailscaleOffers({
  tailscale,
  onChanged,
}: {
  tailscale: TailscaleView
  onChanged: () => void
}) {
  const { can } = useAuth()
  const destructive = can("destructive")
  const [draft, setDraft] = useState<OfferDraft>(NO_OFFER_DRAFT)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  // What the last save wrote, shown until the page's own read moves on from
  // what it held then: without it the saved choice flipped back for a poll.
  const [applied, setApplied] = useState<{ from: string; offers: Offers }>()
  const read = offersOf(tailscale.prefs)
  const prefs = applied && applied.from === JSON.stringify(read) ? applied.offers : read

  const changes = offerChanges(prefs, draft)
  const problem = routeProblem(draft.route)
  const exit = draft.exitNode ?? prefs.advertisingExitNode
  const exitDirty = exit !== prefs.advertisingExitNode
  const routesDirty = changes - (exitDirty ? 1 : 0) > 0
  const reading = routesReading(prefs.advertiseRoutes, tailscale.approval)
  const routeState = (route: string) =>
    tailscale.approval?.routes.find((r) => r.route === route)?.state

  const save = async () => {
    setBusy(true)
    setError(undefined)
    const added = draft.route.trim()
    try {
      const latest = await get<{ tailscale: TailscaleView }>("/network/vpn")
      if (latest.tailscale.prefsReadable === false) {
        throw new Error(
          "Tailscale preferences could not be read. Refresh them before offering a network.",
        )
      }
      const current = offersOf(latest.tailscale.prefs)
      const body = offerRequest(current, draft)
      if (!body) {
        notify.success(added ? `${added} is already offered` : "Tailscale already offers this")
      } else {
        const result = await post<{ note: string }>("/network/vpn/tailscale", body)
        notify.success(savedWords(body, current, added), { description: result.note })
      }
      setApplied({
        from: JSON.stringify(read),
        offers: {
          advertisingExitNode: body?.advertiseExitNode ?? current.advertisingExitNode,
          advertiseRoutes: body?.advertiseRoutes ?? current.advertiseRoutes,
        },
      })
      // A network typed while the save was in flight is a new edit, and stays.
      setDraft((held) => ({
        ...NO_OFFER_DRAFT,
        route: held.route.trim() === added ? "" : held.route,
      }))
      onChanged()
      return true
    } catch (err) {
      const failure = err instanceof Error ? err : new Error(String(err))
      setError(failure)
      notify.error("Tailscale was not changed", failure)
      return false
    } finally {
      setBusy(false)
    }
  }

  return (
    <SettingForm
      name="Tailscale offers"
      onSave={save}
      dirty={changes > 0}
      changes={changes}
      saving={busy}
      invalid={Boolean(problem)}
      canEdit
      applies="immediately"
      onDiscard={() => {
        setDraft(NO_OFFER_DRAFT)
        setError(undefined)
      }}
      error={
        error && (
          <span id="tailscale-offers-error" className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <span>{error.message} Your edits have been kept.</span>
            <Button
              type="button"
              size="xs"
              variant="outline"
              disabled={busy || changes === 0}
              onClick={() => void save()}
            >
              Retry offer
            </Button>
          </span>
        )
      }
    >
      <SettingSection
        title="Exit node"
        status={settingStatus({ dirty: exitDirty, refused: Boolean(error) && exitDirty })}
        state={
          prefs.advertisingExitNode || tailscale.prefs.usingExitNode ? (
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
              {prefs.advertisingExitNode &&
                (tailscale.approval?.exitNode ? (
                  <Status
                    tone={tailscale.approval.exitNode === "serving" ? "running" : "warning"}
                    label={
                      tailscale.approval.exitNode === "serving"
                        ? "Offered to the tailnet"
                        : "Awaiting approval on the control server"
                    }
                  />
                ) : (
                  <span>Offered</span>
                ))}
              {tailscale.prefs.usingExitNode && (
                <span className="text-warning">
                  This server sends its own traffic through another exit node.
                </span>
              )}
            </span>
          ) : (
            "Not offered"
          )
        }
      >
        <div className="flex min-h-9 min-w-0 flex-wrap items-center justify-between gap-x-6 gap-y-2">
          <span className="flex items-center gap-1.5 text-sm font-medium">
            Offer as an exit node
            <InfoTip label="What offering an exit node does">
              Other devices on the tailnet can send their internet through this server.
            </InfoTip>
          </span>
          <Segments
            label="Offer as an exit node"
            value={exit ? "on" : "off"}
            disabled={busy || !tailscale.running}
            onChange={(next) =>
              setDraft((held) => ({
                ...held,
                exitNode: (next === "on") === prefs.advertisingExitNode ? undefined : next === "on",
              }))
            }
            options={[
              // Taking the offer back is the destructive half of the choice.
              { value: "off", label: "Off", disabled: prefs.advertisingExitNode && !destructive },
              { value: "on", label: "Offered" },
            ]}
          />
        </div>
      </SettingSection>

      <SettingSection
        title="Subnet routes"
        status={settingStatus({ dirty: routesDirty, refused: Boolean(error) && routesDirty })}
        state={
          <span className="flex items-center gap-1.5">
            {reading.words}
            {reading.waiting > 0 && (
              <InfoTip label="Why a subnet is not served">
                A subnet not served is awaiting approval on the control server, or another router of
                the same subnet is primary for it. Approval and the tailnet&rsquo;s policy are
                managed there.
              </InfoTip>
            )}
          </span>
        }
      >
        {prefs.advertiseRoutes.length > 0 && (
          <ul aria-label="Offered subnet routes" className="divide-y divide-hairline">
            {prefs.advertiseRoutes.map((route) => {
              const withdrawn = draft.withdraw.includes(route)
              const state = routeState(route)
              return (
                <li key={route} className="flex h-10 min-w-0 items-center gap-3">
                  <span
                    className={cn(
                      "min-w-0 flex-1 truncate font-mono text-body",
                      withdrawn && "text-muted-foreground line-through",
                    )}
                  >
                    {route}
                  </span>
                  {withdrawn ? (
                    <span className="text-xs text-warning">withdrawn on save</span>
                  ) : (
                    state && (
                      <Status
                        tone={state === "serving" ? "running" : "warning"}
                        label={state === "serving" ? "served" : "not served"}
                      />
                    )
                  )}
                  {withdrawn ? (
                    <Button
                      type="button"
                      size="xs"
                      variant="ghost"
                      disabled={busy}
                      aria-label={`Keep offering ${route}`}
                      onClick={() =>
                        setDraft((held) => ({
                          ...held,
                          withdraw: held.withdraw.filter((r) => r !== route),
                        }))
                      }
                    >
                      Keep
                    </Button>
                  ) : (
                    destructive && (
                      <IconAction
                        type="button"
                        label={`Stop offering ${route}`}
                        disabled={busy}
                        onClick={() =>
                          setDraft((held) => ({ ...held, withdraw: [...held.withdraw, route] }))
                        }
                      >
                        <Cross />
                      </IconAction>
                    )
                  )}
                </li>
              )
            })}
          </ul>
        )}
        <div className="space-y-1.5">
          <div className="flex min-w-0 items-center gap-2">
            <Input
              value={draft.route}
              onChange={(event) => {
                setDraft((held) => ({ ...held, route: event.target.value }))
                setError(undefined)
              }}
              aria-invalid={Boolean(problem)}
              aria-describedby={
                problem ? "tailscale-route-problem" : error ? "tailscale-offers-error" : undefined
              }
              placeholder="10.0.4.0/24"
              aria-label="A network to offer"
              className="h-9 min-w-0 flex-1 font-mono sm:max-w-xs"
            />
            <Button
              size="icon"
              variant="outline"
              type="submit"
              disabled={busy || !draft.route.trim() || Boolean(problem)}
              aria-label="Offer it"
            >
              <Plus aria-hidden />
            </Button>
          </div>
          {problem && (
            <p id="tailscale-route-problem" className="text-hint text-destructive">
              {problem}
            </p>
          )}
        </div>
      </SettingSection>
    </SettingForm>
  )
}

/** What a save did, for its toast. */
function savedWords(
  body: { advertiseExitNode?: boolean; advertiseRoutes?: string[] },
  before: Offers,
  added: string,
) {
  const words: string[] = []
  if (body.advertiseExitNode !== undefined)
    words.push(body.advertiseExitNode ? "Offered as an exit node" : "No longer an exit node")
  if (body.advertiseRoutes) {
    const gone = before.advertiseRoutes.filter((r) => !body.advertiseRoutes!.includes(r))
    if (added && body.advertiseRoutes.includes(added) && !before.advertiseRoutes.includes(added))
      words.push(`${added} offered`)
    for (const route of gone) words.push(`${route} withdrawn`)
  }
  return words.join(", ") || "Tailscale changed"
}
