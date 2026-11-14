"use client"

import { useRef, useState } from "react"
import Link from "next/link"
import { Cross, Globe, Router, Shield, Warning } from "@/components/icons"
import { errorMessage, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type {
  DbAccess,
  DbAccessChange,
  DbAccessPreview,
  DbAccessRequest,
  DbHostAddress,
  DbPooler,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { CidrText, cidrKind } from "@/components/config/allowlist"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { Field, FormFact, FormNote, Statement } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read, record } from "@/components/database/home/read"
import { EngineGlyph, EngineMark } from "@/components/database/kit"
import {
  EXPOSURE,
  REACH_CHOICES,
  addressWords,
  firewallOutcome,
  gatewayOutcome,
  joinWords,
  reachDraft,
  reachRefusal,
  sourceProblem,
  wallWords,
  type ReachEdit,
} from "@/components/database/ops/settings-model"
import { useAsk } from "@/components/database/ops/settings-ask"
import { usePendingReport } from "@/components/database/ops/settings-pending"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Where the server can be reached from, and the four answers this page can
 * give it for a container the dashboard owns: this machine only, one of its
 * own addresses, a list of addresses, or anywhere.
 *
 * The answer is a field of the page: four segments under the picture, the
 * chosen one's value under them (the address, picked from the ones this
 * machine has, or the list), held as a draft and saved by the page's bar.
 * They were four cards with a paragraph each, and a press on one began the
 * change at once, on a page whose other fields wait for Save.
 *
 * None of them is a switch. A new binding recreates the container (every
 * session is dropped while it is replaced) and each answer writes or removes
 * firewall rules, so Save asks first, with what it does: the two plain
 * answers in the page's own words, the two that take a value in the server's,
 * rules and all, because the list is enforced by the gateway table the
 * network module keeps and not by a rule the reader could look for on the
 * Firewall page alone. Nothing is sent until that is confirmed, and a change
 * that is cancelled or refused leaves the draft as it was chosen.
 *
 * The picture carries what a list of facts and three notices said: the
 * machine's own addresses, the container and its binding, where the server
 * listens, what the firewall does about the port, what the gateway has
 * refused, and in amber the port that only a password guards.
 *
 * Where the binding is not the dashboard's to change, the page says whose it
 * is instead of offering a control the server would refuse. Any role reads
 * the summary's word. The addresses, the list and the firewall are an
 * administrator's read, and only an administrator asks for them.
 */
export function ReachabilitySection() {
  const { id, conn, engine, summary, status } = useDatabase()
  const { can } = useAuth()
  const admin = can("system.admin")
  const [changing, setChanging] = useState<DbAccess["exposure"]>()
  const [reviewing, setReviewing] = useState(false)
  const [edits, setEdits] = useState<ReachEdit>({})
  // Why the server turned the last change down, in its words.
  const [turnedDown, setTurnedDown] = useState<string>()
  const { ask, dialog, asking } = useAsk()
  const edit = edits
  const setEdit = (next: (held: ReachEdit) => ReachEdit) => {
    setEdits(next)
    setTurnedDown(undefined)
  }
  const access = usePoll(
    (signal) =>
      read<DbAccess>(
        `/databases/${id}/access`,
        (answer) => record(answer.firewall) && Array.isArray(answer.publicAddresses),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: admin },
  )

  // A connection pooler is published where the database is told to be, by this
  // same switch, so the confirmation says so.
  const pooler = usePoll(
    (signal) => get<DbPooler>(`/databases/${id}/pooler`, undefined, signal),
    0,
    [id],
    { enabled: admin && engine.can("pooler") },
  )
  const pooled = pooler.data?.enabled ? pooler.data : undefined

  const data = access.data
  const exposure = data?.exposure ?? summary?.exposure ?? "unknown"
  const words = EXPOSURE[exposure] ?? EXPOSURE.unknown
  const refusal = data ? reachRefusal(data) : undefined
  const mayChange = admin && can("destructive") && data !== undefined && !refusal
  const port = data?.port ?? (Number(conn.port) || undefined)
  const firewall = data?.firewall
  const rule = firewall?.backend ?? "the firewall"
  const container = data?.container ?? "its container"
  const draft = data ? reachDraft(data, edit) : undefined
  const dirty = Boolean(mayChange && draft?.dirty)
  const busy = Boolean(changing) || reviewing
  const subject = {
    mark: <EngineMark engine={engine} size="sm" />,
    name: conn.name,
    facts: (
      <>
        <FormFact label="Container" mono>
          {container}
        </FormFact>
        <FormFact label="Port" mono>
          {port ?? "—"}
        </FormFact>
      </>
    ),
  }

  /** Sends the change, and says how it ended. The recreate can take a minute: the bar says it is being saved. */
  const send = async (request: DbAccessRequest, done: string) => {
    setChanging(request.exposure)
    try {
      const answer = await put<DbAccessChange>(`/databases/${id}/access`, request)
      const said = [
        gatewayOutcome(answer.gateway, answer.access.port),
        firewallOutcome(answer.firewall, answer.access.port, answer.access.exposure),
      ]
        .filter(Boolean)
        .join(" ")
      if (answer.pooler?.error) {
        notify.warning(
          answer.pooler.removed
            ? `The connection pooler could not follow ${conn.name} and was removed`
            : `The connection pooler did not follow ${conn.name}`,
          { description: answer.pooler.error },
        )
      }
      if (answer.gatewayError || answer.firewallError) {
        notify.warning(`${conn.name} changed, and not everything followed`, {
          description: [answer.gatewayError, answer.firewallError].filter(Boolean).join(" "),
        })
      } else {
        notify.success(done, { description: said || undefined })
      }
      setEdits({})
      return true
    } catch (err) {
      // Under the form, not over it: the bar is still up with the answer to save again.
      setTurnedDown(errorMessage(err))
      return false
    } finally {
      setChanging(undefined)
      access.refresh()
      status.refresh()
    }
  }

  const plain = (to: "local" | "public", from: DbAccess) => {
    const data = from
    // A list on every interface is already the binding Anywhere needs.
    const recreates = to === "public" ? data.exposure !== "allowlist" : true
    const listed = data.exposure === "allowlist"
    return ask(
      {
        title: to === "public" ? `Publish ${conn.name} to anywhere` : `Restrict ${conn.name}`,
        confirmLabel: to === "public" ? "Publish" : "Restrict",
        subject,
        description:
          to === "public" ? (
            <>
              <p>
                {recreates
                  ? `Recreates ${container} with port ${port} bound to every interface of this machine${
                      firewall?.active && firewall.editable
                        ? `, and adds a rule to ${rule} that allows port ${port} from anywhere.`
                        : "."
                    }`
                  : `The gateway stops refusing the addresses that are not on the list${
                      firewall?.active && firewall.editable
                        ? `, and ${rule}'s rules for the listed addresses give way to one that allows port ${port} from anywhere.`
                        : "."
                    }`}{" "}
                Anything that can reach this machine can then try the password.
              </p>
              {pooled && (
                <p>
                  {pooled.container} (PgBouncer) is published with it, on port{" "}
                  {pooled.port ?? "its own"}
                  {firewall?.active && firewall.editable ? ", with a rule of its own" : ""}.
                </p>
              )}
              <p>
                {recreates
                  ? "The data is kept. Every open session is dropped while the container is replaced."
                  : "Nothing restarts, and open sessions carry on."}
              </p>
            </>
          ) : (
            <>
              <p>
                Recreates {container} with port {port} bound to this machine only, and removes the
                firewall rules this dashboard wrote for it
                {listed ? " and the gateway's list" : ""}. Anything that reaches it from another
                machine stops reaching it.
              </p>
              {pooled && pooled.exposure !== "local" && (
                <p>{pooled.container} (PgBouncer) is brought back to this machine with it.</p>
              )}
              <p>
                The data is kept. Every open session is dropped while the container is replaced.
              </p>
            </>
          ),
      },
      () =>
        send(
          { exposure: to },
          to === "public"
            ? `${conn.name} is reachable from anywhere`
            : `${conn.name} is reachable from this server only`,
        ),
    )
  }

  /**
   * Asks the server what the change would run, then confirms with that: the
   * gateway's rules, the recreate, and the firewall's plan in its own words.
   */
  const reviewed = async (
    request: Extract<DbAccessRequest, { exposure: "private" | "allowlist" }>,
  ) => {
    setReviewing(true)
    let preview: DbAccessPreview
    try {
      preview = await post<DbAccessPreview>(`/databases/${id}/access/preview`, request)
    } catch (err) {
      setTurnedDown(`Could not work out what the change would do. ${errorMessage(err)}`)
      return false
    } finally {
      setReviewing(false)
    }
    const listed = request.exposure === "allowlist"
    const rules = preview.steps.flatMap((step) => step.lines ?? [])
    return ask(
      {
        title: listed
          ? `Allow only listed addresses to reach ${conn.name}`
          : `Publish ${conn.name} on one address`,
        confirmLabel: listed ? "Allow these addresses" : "Publish on this address",
        subject,
        description: (
          <>
            <ol className="list-inside list-decimal space-y-1" aria-label="What runs">
              {preview.steps.map((step, index) => (
                <li key={`${step.kind}:${index}`}>{step.description}</li>
              ))}
            </ol>
            {rules.length > 0 && (
              <Statement label="The gateway's rules" sql={rules.join("\n")} placeholder="" />
            )}
            {preview.firewallError && (
              <Notice tone="warning" icon={Warning} title="The firewall will not follow">
                {preview.firewallError}
              </Notice>
            )}
            <p>
              {preview.recreate
                ? "The data is kept. Every open session is dropped while the container is replaced."
                : "Nothing restarts. Sessions already open from other addresses carry on until they close."}
            </p>
          </>
        ),
      },
      () =>
        send(
          request,
          listed
            ? `${conn.name} is reachable from the listed addresses`
            : `${conn.name} is reachable on ${request.address}`,
        ),
    )
  }

  /** The bar's Save: every answer is asked for before it is sent. */
  const save = async () => {
    if (!data || !draft?.dirty || busy || asking) return false
    setTurnedDown(undefined)
    if (draft.choice === "local" || draft.choice === "public") return plain(draft.choice, data)
    if (draft.choice === "private") {
      return draft.address ? reviewed({ exposure: "private", address: draft.address }) : false
    }
    return reviewed({ exposure: "allowlist", allow: draft.allow })
  }

  usePendingReport("reach", {
    edits: dirty ? [{ name: "Reachable from", state: "changed" }] : [],
  })

  // What each answer does, behind the label: the cards said it in a paragraph each.
  const what = (
    <ul className="space-y-1">
      <li>
        This server: port {port} on loopback, for programs here and the deployments linked to it.
      </li>
      <li>
        One address: port {port} on one address of this machine: a tailnet, a VPN, a local network.
      </li>
      <li>
        Allowed: port {port} on every interface, and the gateway refuses every address not on your
        list.
      </li>
      <li>Anywhere: port {port} on every interface. Only the password is in the way.</li>
      <li>Save asks first: a new binding replaces the container and drops its sessions.</li>
    </ul>
  )

  const section = (
    <SettingSection
      id="reachability"
      title="Where it can be reached from"
      state={
        <span className="flex">
          {exposure === "public" ? (
            <Status verdict="warning" label={words.word} />
          ) : (
            <Status tone="unknown" label={words.word} />
          )}
        </span>
      }
      status={
        changing ? (
          <Status
            key="changing"
            tone="running"
            label={<TextShimmer>Changing where it is reachable from…</TextShimmer>}
          />
        ) : (
          settingStatus({ dirty, refused: dirty && Boolean(turnedDown) })
        )
      }
    >
      <ReachPicture
        access={data}
        exposure={exposure}
        changing={Boolean(changing)}
        loading={admin && !data && !access.error}
      />

      {mayChange && data && draft && (
        <>
          <Field label="Reachable from" info={what}>
            <Segments
              fill
              label="Where it is reachable from"
              value={draft.choice}
              disabled={busy}
              onChange={(choice) => setEdit((held) => ({ ...held, choice }))}
              options={REACH_CHOICES.map((choice) => ({
                value: choice.value,
                label: choice.label,
                disabled:
                  (choice.value === "private" && data.addresses.length === 0) ||
                  (choice.value === "allowlist" && !data.gateway.available),
              }))}
            />
          </Field>
          {data.addresses.length === 0 && (
            <FormNote data-slot="reach-unavailable">
              One address: this machine has no tailnet, VPN or local-network address to publish on.
            </FormNote>
          )}
          {!data.gateway.available && (
            <FormNote data-slot="reach-unavailable">Allowed: {data.gateway.reason}</FormNote>
          )}

          {draft.choice === "private" && (
            <OneAddress
              access={data}
              picked={draft.address}
              busy={busy}
              onPick={(address) => setEdit((held) => ({ ...held, address }))}
            />
          )}
          {draft.choice === "allowlist" && (
            <AllowedAddresses
              access={data}
              list={draft.allow}
              busy={busy}
              onChange={(allow) => setEdit((held) => ({ ...held, allow }))}
            />
          )}
        </>
      )}
      {/* The place the answers take, kept while they are read. */}
      {admin && can("destructive") && !data && !access.error && (
        <div data-slot="reach-loading">
          <Field label="Reachable from" info={what}>
            <Skeleton className="h-8 w-full" />
          </Field>
        </div>
      )}

      {data && data.exposure !== "allowlist" && data.allow.length > 0 && (
        <Notice
          tone="warning"
          icon={Warning}
          title="A list from an unfinished change is still kept"
        >
          The gateway still refuses every address but {joinWords(data.allow)} on port {port}. Choose
          where it can be reached from again to clear it.
        </Notice>
      )}

      {admin && !data && access.error && (
        <CouldNotRead
          what="where it is reachable from"
          error={access.error}
          onRetry={access.refresh}
        />
      )}

      {admin && refusal && (
        <FormNote data-slot="reach-refusal">
          <span className="font-medium text-foreground">Not changed from here.</span> {refusal}
        </FormNote>
      )}
      {admin && data && !refusal && !can("destructive") && (
        <FormNote>Changing it recreates the container, which your role may not do.</FormNote>
      )}
      {!admin && (
        <FormNote>
          The addresses and the firewall are an administrator&rsquo;s to read and to change.
        </FormNote>
      )}
    </SettingSection>
  )

  return (
    <>
      {/* One form in every state, so the section is not drawn again when the
          binding lands and with it the right to change it. It is on the bar
          only for the role and the container that can be changed. */}
      <SettingForm
        name="Where it is reachable from"
        onSave={save}
        dirty={dirty}
        changes={dirty ? 1 : 0}
        saving={busy}
        invalid={dirty && Boolean(draft?.problem)}
        canEdit={mayChange}
        applies="immediately"
        onDiscard={() => setEdit(() => ({}))}
        error={turnedDown && `It was not changed. ${turnedDown}`}
      >
        {section}
      </SettingForm>
      {dialog}
    </>
  )
}

/**
 * Which of this machine's addresses the port is published on: a tailnet's,
 * a VPN's, a local network's, each said by who it faces.
 */
function OneAddress({
  access,
  picked,
  busy,
  onPick,
}: {
  access: DbAccess
  picked: string | undefined
  busy: boolean
  onPick: (address: string) => void
}) {
  const current = access.exposure === "private" ? access.address : undefined
  const chosen = access.addresses.find((one) => one.address === picked)
  return (
    <div className="animate-rise space-y-3" data-slot="reach-address">
      <ChoiceGrid columns={2} role="group" aria-label="This machine's addresses">
        {access.addresses.map((one) => (
          <AddressCard
            key={one.address}
            address={one}
            selected={one.address === picked}
            current={one.address === current}
            disabled={busy}
            onClick={() => onPick(one.address)}
          />
        ))}
      </ChoiceGrid>
      {chosen?.network === "uplink" ? (
        <FormNote tone="warning">
          A cloud provider often maps a public address onto this one, which would make it as
          reachable as Anywhere. Pick a tailnet or a VPN address if one is listed.
        </FormNote>
      ) : chosen?.network === "tailnet" || chosen?.network === "vpn" ? (
        <FormNote>
          {addressWords(chosen).hint} Docker publishes only on an address the machine holds: if{" "}
          {chosen.interface ?? "the tunnel"} is down when the container starts, it does not start
          until it is restarted.
        </FormNote>
      ) : (
        chosen && <FormNote>{addressWords(chosen).hint}</FormNote>
      )}
    </div>
  )
}

function AddressCard({
  address,
  selected,
  current,
  disabled,
  onClick,
}: {
  address: DbHostAddress
  selected: boolean
  current: boolean
  disabled: boolean
  onClick: () => void
}) {
  const words = addressWords(address)
  return (
    <ChoiceCard selected={selected} disabled={disabled} onClick={onClick} className="min-h-0">
      <span className="flex min-w-0 items-center gap-2">
        <ProductLogo
          id={address.network === "tailnet" ? "tailscale" : undefined}
          fallback={Router}
          size="sm"
        />
        <ChoiceCardTitle>
          <span className="font-mono">{address.address}</span>
        </ChoiceCardTitle>
      </span>
      <ChoiceCardHint>
        {words.label}
        {address.interface ? ` · ${address.interface}` : ""}
        {current ? " · published here now" : ""}
      </ChoiceCardHint>
    </ChoiceCard>
  )
}

/**
 * The list the gateway keeps the port to, one row per address or network,
 * drawn the way the dashboard's own allowlist is: where those addresses are,
 * the tailnet as Tailscale and the rest as what they are.
 *
 * It is a draft until the page is saved, so a list can be built up and taken
 * apart without the gateway being loaded at every step. The address this
 * request came from is offered by name, since it is the one most often meant
 * and the one most often mistyped.
 */
function AllowedAddresses({
  access,
  list,
  busy,
  onChange,
}: {
  access: DbAccess
  list: string[]
  busy: boolean
  onChange: (list: string[]) => void
}) {
  const [adding, setAdding] = useState("")
  const candidate = adding.trim()
  const problem = sourceProblem(candidate, list)
  const client = access.client
  const offerClient =
    client && !list.includes(client) && !sourceProblem(client, list) && client !== candidate
  const port = access.port

  const add = (entry: string) => {
    if (!entry || sourceProblem(entry, list)) return
    onChange([...list, entry])
    setAdding("")
  }

  return (
    <div className="animate-rise space-y-3" data-slot="reach-allowlist">
      {list.length > 0 ? (
        <RowList aria-label="Allowed addresses">
          {list.map((entry) => {
            const kind = cidrKind(entry)
            return (
              <Row
                key={entry}
                className="px-0 py-2"
                leading={<ProductLogo id={kind.product} fallback={kind.glyph} size="sm" />}
                title={<CidrText cidr={entry} />}
                subtitle={entry === client ? `${kind.label} · the address you are on` : kind.label}
                trailing={
                  <IconAction
                    label={`Remove ${entry}`}
                    disabled={busy}
                    onClick={() => onChange(list.filter((other) => other !== entry))}
                  >
                    <Cross />
                  </IconAction>
                }
              />
            )
          })}
        </RowList>
      ) : (
        <FormNote>
          {`No address is on the list yet. Add the ones that connect to ${port ? `port ${port}` : "it"}: an office, an application server, your own.`}
        </FormNote>
      )}
      {/* Not a form of its own: the page's form is around it, and Enter here adds the entry. */}
      <Field label="Add an address or a network" htmlFor="reach-allow-add" error={problem}>
        <div className="flex min-w-0 gap-2">
          <Input
            id="reach-allow-add"
            value={adding}
            placeholder="203.0.113.7 or 198.51.100.0/24"
            aria-invalid={Boolean(problem) || undefined}
            onChange={(event) => setAdding(event.target.value)}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return
              event.preventDefault()
              add(candidate)
            }}
            className="min-w-0 font-mono text-body"
          />
          <Button
            type="button"
            variant="outline"
            disabled={!candidate || Boolean(problem) || busy}
            onClick={() => add(candidate)}
          >
            Add
          </Button>
        </div>
      </Field>
      {access.gateway.alsoAdmitted.length > 0 && (
        <FormNote>
          Also let in, whatever the list says: {joinWords(access.gateway.alsoAdmitted)}, the
          networks this dashboard is reached from.
        </FormNote>
      )}
      {offerClient && (
        <FormNote>
          You are connected from <span className="font-mono text-foreground">{client}</span>.{" "}
          <button
            type="button"
            className="rounded-sm font-medium text-foreground underline focus-ring"
            onClick={() => add(client)}
            disabled={busy}
          >
            Add it
          </button>
        </FormNote>
      )}
    </div>
  )
}

/**
 * Who can reach the server's port, drawn as the way in: this machine's own
 * programs and the outside on the left, the container that publishes the
 * port in the middle (Docker's whale, its name, the binding) and the server
 * itself, as its product, at the end.
 *
 * The outside's line is the answer to the page's question. Dashed, with the
 * globe a ring not yet there, while the port is on loopback: nothing outside
 * gets in. Amber while it is published to anywhere, because it works and
 * should not be relied on (§3's amber, as a project's Runtime draws a port
 * that bypasses the proxy). Solid and plain for one network or a list, which
 * is the arrangement the page recommends, and a list draws everyone else
 * too, dashed, refused by the gateway, so the picture says both halves; amber
 * there when the gateway's table is not loaded and the list refuses nobody.
 * A server on another machine is one line from here to it, since where it
 * listens is that machine's to say.
 *
 * Its captions are the facts the section used to list under it: this
 * machine's addresses beside "This server", the firewall's word under
 * whoever it lets in or keeps out, what the gateway has refused, the
 * container as the way to its page, and where the server listens.
 *
 * It has its shape from the first paint: the container the summary names is
 * drawn while the binding is read, so the read landing fills in captions and
 * moves nothing.
 */
function ReachPicture({
  access,
  exposure,
  changing,
  loading,
}: {
  access?: DbAccess
  exposure: string
  changing: boolean
  /** The binding is being read: the line the firewall's word will take is kept for it. */
  loading: boolean
}) {
  const { conn, engine, summary, status } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const localMark = useRef<HTMLDivElement>(null)
  const outsideMark = useRef<HTMLDivElement>(null)
  const refusedMark = useRef<HTMLDivElement>(null)
  const boxMark = useRef<HTMLDivElement>(null)
  const serverMark = useRef<HTMLDivElement>(null)
  const remote = exposure === "remote"
  const listed = exposure === "allowlist"
  const one = exposure === "private"
  const anywhere = exposure === "public"
  const open = anywhere || listed || one
  const running = status.state === "running"
  const port = access?.port ?? (Number(conn.port) || undefined)
  const name = access ? access.container : summary?.container?.name
  const boxed = Boolean(name) && !remote
  const bind =
    anywhere || listed ? "0.0.0.0" : one ? (access?.address ?? "one address") : "127.0.0.1"
  const firewall = access?.firewall
  const wall = access && !remote ? wallWords(access) : undefined
  const network = access?.addresses.find((a) => a.address === access.address)
  const allow = access?.allow ?? []
  const unguarded = listed && access !== undefined && !access.gateway.enforced
  const machine = access && !remote && !anywhere ? access.publicAddresses : []
  const wallLine = wall ? (
    <span className="block">{wall}</span>
  ) : (
    loading && !remote && <span className="block">{"\u00a0"}</span>
  )

  const local = (
    <WireNode
      key="local"
      nodeRef={localMark}
      align="end"
      mark={<WireHost size="md" />}
      eyebrow={
        <>
          This server
          {machine.length > 0 && (
            <span className="font-mono leading-none tracking-normal"> · {machine.join(", ")}</span>
          )}
        </>
      }
      title="Programs and linked deployments"
      hint={remote ? "the dashboard dials it" : "over loopback"}
    />
  )
  const outside = remote ? undefined : listed ? (
    <WireNode
      key="outside"
      nodeRef={outsideMark}
      align="end"
      mark={
        <WireMark size="md" className="animate-rise">
          <Shield />
        </WireMark>
      }
      eyebrow="Allowed addresses"
      title={
        <span className="font-mono text-xs wrap-anywhere">
          {allow.slice(0, 2).join(", ")}
          {allow.length > 2 ? ` +${allow.length - 2}` : ""}
        </span>
      }
      hint={
        <>
          {allow.length} on the list
          {wallLine}
        </>
      }
    />
  ) : one ? (
    <WireNode
      key="outside"
      nodeRef={outsideMark}
      align="end"
      mark={
        network?.network === "tailnet" ? (
          <WireMark size="md" tone="logo" className="animate-rise">
            <ProductGlyph id="tailscale" />
          </WireMark>
        ) : (
          <WireMark size="md" className="animate-rise">
            <Router />
          </WireMark>
        )
      }
      eyebrow={network ? addressWords(network).label : "One network"}
      title={<span className="font-mono text-xs wrap-anywhere">{access?.address}</span>}
      hint={
        <>
          {network?.interface ? `published on ${network.interface}` : "one address of this machine"}
          {wallLine}
        </>
      }
    />
  ) : (
    <WireNode
      key="outside"
      nodeRef={outsideMark}
      align="end"
      mark={
        anywhere ? (
          <WireMark size="md" tone="warning" className="animate-rise">
            <Globe />
          </WireMark>
        ) : (
          <WirePlaceholder size="md" className="animate-rise">
            <Globe />
          </WirePlaceholder>
        )
      }
      eyebrow="Anywhere"
      title={
        anywhere ? (
          <span className="font-mono text-xs">
            {access?.publicAddresses.length ? access.publicAddresses.join(", ") : "every interface"}
          </span>
        ) : (
          "Other machines"
        )
      }
      hint={
        <>
          <span className={cn(anywhere && "text-warning")} data-slot="reach-warning">
            {!anywhere
              ? "cannot reach it"
              : firewall?.active && !firewall.open
                ? "only this machine's networks get through, for now"
                : "only the password is in the way"}
          </span>
          {wallLine}
        </>
      }
    />
  )
  const refused = listed && (
    <WireNode
      key="refused"
      nodeRef={refusedMark}
      align="end"
      mark={
        unguarded ? (
          <WireMark size="md" tone="warning" className="animate-rise">
            <Globe />
          </WireMark>
        ) : (
          <WirePlaceholder size="md" className="animate-rise">
            <Globe />
          </WirePlaceholder>
        )
      }
      eyebrow="Everyone else"
      title="Other addresses"
      hint={
        unguarded ? (
          <span className="text-warning" data-slot="reach-warning">
            not refused: the gateway&rsquo;s table is not loaded.{" "}
            <Link href="/network/protection" className="rounded-sm underline focus-ring">
              Protection
            </Link>{" "}
            says why
          </span>
        ) : (
          <>
            refused by the gateway
            {access && access.gateway.refused > 0 && (
              <span className="numeric block">
                {access.gateway.refused.toLocaleString()} attempts so far
              </span>
            )}
          </>
        )
      }
    />
  )
  const box = (
    <WireNode
      nodeRef={boxMark}
      align="center"
      mark={
        <WireMark size="md" tone="logo">
          <ProductGlyph id="docker" />
        </WireMark>
      }
      eyebrow="Container"
      title={
        name ? (
          <Link
            href={`/docker/containers/${encodeURIComponent(name)}`}
            className="rounded-sm font-mono text-xs wrap-anywhere underline focus-ring"
          >
            {name}
          </Link>
        ) : undefined
      }
      hint={
        <>
          <span className="font-mono">
            {bind}:{port ?? "?"}
          </span>
          {access?.composeProject && (
            <span className="block">compose project {access.composeProject}</span>
          )}
        </>
      }
    />
  )
  const server = (
    <WireNode
      nodeRef={serverMark}
      align="start"
      mark={
        <WireMark size="md" tone="logo">
          <EngineGlyph engine={engine} />
        </WireMark>
      }
      eyebrow={`${engine.label}${summary?.versionNumber ? ` ${summary.versionNumber}` : ""}`}
      title={
        <span className="font-mono text-xs wrap-anywhere">
          {conn.port ? `${conn.host}:${conn.port}` : `:${port ?? "?"}`}
        </span>
      }
      hint={remote ? "on its own machine" : "listens"}
    />
  )
  const inner = boxed ? boxMark : serverMark
  const start = [local, outside, refused].filter((node): node is React.JSX.Element => Boolean(node))
  return (
    <SettingPicture
      label="Who can reach it"
      containerRef={container}
      className="py-4"
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={localMark}
            toRef={inner}
            still={!running || changing}
          />
          {!remote && (
            <AnimatedBeam
              key={exposure}
              containerRef={container}
              fromRef={outsideMark}
              toRef={inner}
              still
              dashed={!open}
              tone={anywhere ? "warning" : "default"}
            />
          )}
          {listed && (
            <AnimatedBeam
              key={unguarded ? "open" : "refused"}
              containerRef={container}
              fromRef={refusedMark}
              toRef={inner}
              still
              dashed={!unguarded}
              tone={unguarded ? "warning" : "default"}
            />
          )}
          {boxed && (
            <AnimatedBeam
              containerRef={container}
              fromRef={boxMark}
              toRef={serverMark}
              still={!running || changing}
              delay={0.6}
            />
          )}
        </>
      }
      start={start}
      middle={boxed ? box : undefined}
      end={server}
    />
  )
}
