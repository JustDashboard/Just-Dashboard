"use client"

import { useEffect, useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Linked, LockClosed, NetworkDevice, Slash, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, errorMessage, get, post } from "@/lib/api"
import type { Container, DockerNetwork, NetworkDetail, NetworkMember } from "@/lib/types"
import {
  isChangePreview,
  isPrunePreview,
  membersKnown,
  ownerConsequence,
  ownerWords,
  prunePlan,
  type NetworkChangePreview,
  type NetworkPruneResult,
} from "@/lib/docker-networks"
import { ConflictList, NetworkChangeDialog } from "./network-conflicts"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { EmptyState, ErrorState, LoadingPanel, LoadingRows, Notice } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Group, Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { Detail, DetailList, SearchInput } from "@/components/page"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Hint } from "@/components/docker/explain"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { NewNetworkDialog } from "./network-create"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * Networks, and — the part that was missing — who is on them.
 *
 * "These two containers cannot see each other" is the most common Docker
 * problem there is, and its answer is almost always that they are on different
 * networks. The Engine will tell you, and no panel in this class puts it on
 * screen next to the name each container answers to. Attaching one is two
 * clicks here rather than a shell.
 */
export function NetworksTab({
  creating: externalCreating,
  onCreatingChange,
  actions,
  initialReservationId,
}: {
  creating?: boolean
  onCreatingChange?: (open: boolean) => void
  actions?: React.ReactNode
  initialReservationId?: string
}) {
  const { can } = useAuth()
  const [selected, setSelected] = useSessionState<string | null>("docker.networks.selected", null)
  const [internalCreating, setInternalCreating] = useState(false)
  const creating = externalCreating ?? internalCreating
  const setCreating = onCreatingChange ?? setInternalCreating
  const [filter, setFilter] = useSessionState("docker.networks.query", "")
  const [state, setState] = useSessionState<"all" | "custom" | "system">(
    "docker.networks.state",
    "all",
  )
  const [pruning, setPruning] = useState(false)

  const { data, error, refresh, lastSuccess } = usePoll(
    (signal) => get<DockerNetwork[]>("/docker/networks/", undefined, signal),
    30000,
  )
  const networks = useMemo(() => data ?? [], [data])
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return networks.filter((n) => {
      const system = SYSTEM_NETWORKS.includes(n.name)
      if (state === "custom" && system) return false
      if (state === "system" && !system) return false
      if (!needle) return true
      return (
        n.name.toLowerCase().includes(needle) ||
        n.driver.toLowerCase().includes(needle) ||
        n.subnets.some((s) => s.toLowerCase().includes(needle))
      )
    })
  }, [networks, filter, state])
  const counts = useMemo(
    () => ({
      all: networks.length,
      custom: networks.filter((n) => !SYSTEM_NETWORKS.includes(n.name)).length,
      system: networks.filter((n) => SYSTEM_NETWORKS.includes(n.name)).length,
    }),
    [networks],
  )
  if (!data) {
    if (error) return <ErrorState error={error} onRetry={refresh} />
    return <LoadingPanel />
  }

  // The three the Engine owns are never removable, so they are not "unused"
  // in any sense the prune button should count. A network whose members were
  // not read is not unused either; the prune dialog reads its own set afresh.
  const unused = networks.filter(
    (n) => membersKnown(n) && n.usedBy.length === 0 && !SYSTEM_NETWORKS.includes(n.name),
  )

  return (
    <div className="space-y-4">
      <NetworkReadWarning
        error={error}
        refresh={refresh}
        lastSuccess={lastSuccess}
        reading="Docker networks"
      />
      {networks.some((n) => !membersKnown(n)) && (
        <Notice title="Which containers use each network could not be read" tone="warning">
          Docker listed the networks but not the containers on them, so no network is shown as
          unused and none can be removed or pruned until a refresh reads them.
        </Notice>
      )}
      {/* Plain: the list is the page, and each network is a card with its own
          edge — a frame around framed cards is the nesting §12 refuses. */}
      <Panel plain className="animate-rise">
        <PanelHeader
          title="Networks"
          actions={
            <>
              {actions}
              {/* A network left behind by a removed stack is invisible clutter
                  that also holds a subnet out of the pool, which is what makes
                  a later `compose up` fail to find one. The Engine's own prune
                  also takes a network a stopped container still names, which
                  then cannot start; the dialog reviews the exact set first. */}
              {can("destructive") && unused.length > 0 && (
                <Button size="sm" variant="outline" onClick={() => setPruning(true)}>
                  <Trash className="size-4" />
                  Prune
                </Button>
              )}
            </>
          }
        />
        {networks.length > 0 && (
          <PanelToolbar>
            <SearchInput
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Search networks"
            />
            <div className="flex min-w-0 flex-wrap gap-1">
              <FilterChip selected={state === "all"} onClick={() => setState("all")}>
                All
                <ChipCount>{counts.all}</ChipCount>
              </FilterChip>
              {counts.custom > 0 && (
                <FilterChip selected={state === "custom"} onClick={() => setState("custom")}>
                  User-created
                  <ChipCount>{counts.custom}</ChipCount>
                </FilterChip>
              )}
              <FilterChip selected={state === "system"} onClick={() => setState("system")}>
                Docker system
                <ChipCount>{counts.system}</ChipCount>
              </FilterChip>
            </div>
          </PanelToolbar>
        )}
        <PanelBody flush>
          {networks.length === 0 ? (
            <EmptyState icon={NetworkDevice} title="No networks" />
          ) : visible.length === 0 ? (
            <EmptyState
              icon={NetworkDevice}
              title="Nothing matches those filters"
              description="Clear the search, or look under a different state."
              action={
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setFilter("")
                    setState("all")
                  }}
                >
                  Clear filters
                </Button>
              }
            />
          ) : (
            // One card per network at every width: each opens who is on it,
            // so each is a choice (§16).
            <ChoiceList aria-label="Networks" className="animate-rise">
              {visible.map((network) => (
                <NetworkCard
                  key={network.id}
                  network={network}
                  onOpen={() => setSelected(network.id)}
                  onChanged={refresh}
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <NetworkDetailPanel
        id={selected}
        onOpenChange={(o) => !o && setSelected(null)}
        onChanged={refresh}
      />
      <PruneDialog open={pruning} onOpenChange={setPruning} onPruned={refresh} />
      <NewNetworkDialog
        open={creating}
        onOpenChange={setCreating}
        onCreated={refresh}
        networks={networks}
        inventoryError={error}
        refreshInventory={refresh}
        initialReservationId={initialReservationId}
      />
    </div>
  )
}

/** The three Docker creates and will not let you remove. */
const SYSTEM_NETWORKS = ["bridge", "host", "none"]

/**
 * One network, as a card that opens who is on it.
 *
 * Docker's own three say so beside the name; the subnet, the driver and how
 * many containers are attached are the second line, which a column of cards is
 * scanned down for the one nothing is using. The removal
 * control is drawn at rest, with the reason in place of the button where Docker
 * would refuse, which is the part of this row that actually teaches.
 */
function NetworkCard({
  network,
  onOpen,
  onChanged,
}: {
  network: DockerNetwork
  onOpen: () => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const [removing, setRemoving] = useState(false)
  const system = SYSTEM_NETWORKS.includes(network.name)
  const known = membersKnown(network)
  const removable = can("destructive") && !system && known && network.usedBy.length === 0
  const attached = known
    ? `${network.usedBy.length} container${network.usedBy.length === 1 ? "" : "s"}`
    : "members unread"
  const owner = network.owner?.kind === "system" ? undefined : ownerWords(network.owner)

  return (
    <ChoiceRow
      verb={network.name}
      onSelect={onOpen}
      leading={<ProductLogo fallback={NetworkDevice} size="sm" />}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{network.name}</span>
          {system && <Tag className="shrink-0">Docker system</Tag>}
          {owner && <Tag className="max-w-48 shrink truncate">{owner}</Tag>}
          {network.internal && <Tag className="shrink-0">no internet</Tag>}
        </span>
      }
      // One line a phone can hold whole: the subnet first, because it is the
      // fact two networks are told apart by, then what drives it and who is on
      // it. The attached names are one hover away.
      description={
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="shrink-0 font-mono">{network.subnets.join(", ") || "no subnet"}</span>
          <span aria-hidden>·</span>
          <span className="shrink-0">{network.driver}</span>
          <span aria-hidden>·</span>
          {network.usedBy.length > 0 ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="numeric shrink-0 cursor-default">{attached}</span>
              </TooltipTrigger>
              <TooltipContent>{network.usedBy.join(", ")}</TooltipContent>
            </Tooltip>
          ) : (
            <span className="numeric shrink-0">{attached}</span>
          )}
        </span>
      }
      actions={
        can("destructive") && (
          <>
            {removable ? (
              <IconAction
                label="Remove"
                className="text-destructive"
                onClick={() => setRemoving(true)}
              >
                <Trash />
              </IconAction>
            ) : (
              <IconAction
                label={
                  system
                    ? "Docker's own network — cannot be removed"
                    : !known
                      ? "Who uses it could not be read — cannot judge a removal"
                      : `In use by ${attached} — cannot be removed while attached`
                }
                className="text-muted-foreground opacity-40"
                onClick={onOpen}
              >
                <Trash />
              </IconAction>
            )}
            <NetworkChangeDialog
              open={removing}
              onOpenChange={setRemoving}
              target={network.id}
              title={`Delete ${network.name}`}
              description={`What removing the network ${network.name} disturbs, read before it is removed.`}
              confirmLabel="Delete"
              emptyLabel="Nothing names this network: no container, stopped or running, and no deployment."
              load={() =>
                get<NetworkChangePreview>(
                  `/docker/networks/${encodeURIComponent(network.id)}/removal`,
                )
              }
              onConfirm={async () => {
                await del(`/docker/networks/${encodeURIComponent(network.id)}`)
                notify.success(`${network.name} removed`)
                onChanged()
              }}
            />
          </>
        )
      }
    />
  )
}

/**
 * Who is on this network, as a shape rather than a list.
 *
 * "These two containers cannot see each other" is the most common Docker
 * problem there is, and its answer is almost always that they are on different
 * networks. A list of names answers that; a small node diagram answers it
 * faster, and shows aliases, both families' addresses and which members also
 * sit on another network — the containers that join this network to the rest.
 */
function NetworkShape({ name, members }: { name: string; members: NetworkMember[] }) {
  if (members.length === 0) return null
  return (
    <section className="space-y-2">
      <p className="eyebrow">Shape</p>
      {/* The one fence in the panel: a diagram is a region, and its edge is
          what says the tree inside is one picture rather than a list. */}
      <Group tinted className="space-y-1.5">
        <div className="flex items-center gap-2">
          <span className="size-2 shrink-0 rounded-full bg-success" aria-hidden />
          <span className="truncate font-mono text-body font-medium">{name}</span>
          <span className="text-micro text-muted-foreground">network</span>
        </div>
        <ul className="space-y-1 border-l border-hairline pl-3">
          {members.map((member) => (
            <li
              key={member.id}
              className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 leading-5"
            >
              <span className="size-1.5 shrink-0 rounded-full bg-muted-foreground" aria-hidden />
              <span className="truncate font-mono text-hint">{member.name}</span>
              <span className="font-mono text-micro text-muted-foreground">
                {[member.ipv4, member.ipv6].filter(Boolean).join(" · ") || "no address"}
              </span>
              {visibleAliases(member)
                .slice(0, 2)
                .map((a) => (
                  <Tag key={a} mono>
                    {a}
                  </Tag>
                ))}
              {(member.networks ?? []).length > 0 && (
                <span className="text-micro text-muted-foreground">
                  also on <span className="font-mono">{member.networks?.join(", ")}</span>
                </span>
              )}
            </li>
          ))}
        </ul>
      </Group>
    </section>
  )
}

/** The names a member answers to beyond its own, its short ID left out. */
function visibleAliases(member: NetworkMember) {
  return member.aliases.filter((a) => a !== member.name && !member.id.startsWith(a))
}

/** Why a member offers no detach control: its owner put it there. */
function guardedMember(member: NetworkMember) {
  if (member.dashboard) return "Part of the dashboard itself — it is never detached here"
  if (member.ingress)
    return "The shared public Caddy — deployment routes reach their containers through it"
  return undefined
}

function NetworkDetailPanel({
  id,
  onOpenChange,
  onChanged,
}: {
  id: string | null
  onOpenChange: (open: boolean) => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const [attaching, setAttaching] = useState(false)
  const [detaching, setDetaching] = useState<NetworkMember | null>(null)

  const { data, error, loading, refresh } = usePoll<NetworkDetail>(
    (signal) =>
      get<NetworkDetail>(`/docker/networks/${encodeURIComponent(id ?? "")}`, undefined, signal),
    0,
    [id],
    { enabled: id !== null },
  )

  // Docker's --attachable gates only swarm-scoped networks; a local bridge
  // takes a running container whatever the flag says, Compose's included.
  const joinable = data && !data.system && (data.scope !== "swarm" || data.attachable)
  const ownNetwork = data?.owner?.kind === "dashboard"

  return (
    <SidePanel
      open={id !== null}
      onOpenChange={onOpenChange}
      title={data?.name ?? "Network"}
      description={data?.subnets.join(", ")}
      actions={
        can("service.control") &&
        data &&
        !data.system &&
        (ownNetwork ? (
          <Hint className="max-w-64">
            The dashboard&apos;s own private network takes no other containers.
          </Hint>
        ) : joinable ? (
          <Button size="sm" variant="outline" onClick={() => setAttaching(true)}>
            <Linked className="size-3.5" />
            Attach a container
          </Button>
        ) : (
          <Hint className="max-w-64">
            A swarm network created without --attachable takes only swarm services; add them through
            the stack file, then deploy.
          </Hint>
        ))
      }
    >
      {error && <ErrorState error={error} />}
      {loading && !data && <LoadingRows />}
      {data && (
        <div className="space-y-6">
          {data.membersError && (
            <Notice title="The members' details could not all be read" tone="warning">
              Docker listed who is attached but not the containers themselves, so their states,
              stacks and other networks are unknown here: {data.membersError}
            </Notice>
          )}
          <section className="space-y-2">
            <p className="eyebrow">Basics</p>
            <DetailList className="gap-y-2">
              <Detail label="Driver" className="text-body font-medium">
                {data.driver}
              </Detail>
              <Detail label="Scope" className="text-body">
                {data.scope || "local"}
              </Detail>
              <Detail label="Subnet" className="font-mono text-body">
                {data.subnets.join(", ") || "assigned by Docker"}
              </Detail>
              <Detail label="Gateway" className="font-mono text-body">
                {data.gateway || "—"}
              </Detail>
              {data.owner && (
                <Detail label="Created by" className="text-body leading-relaxed">
                  <span className="block">{ownerWords(data.owner)}</span>
                  <span className="block text-hint text-muted-foreground">
                    {ownerConsequence(data.owner)}
                  </span>
                </Detail>
              )}
            </DetailList>
          </section>

          <section className="space-y-2">
            <p className="eyebrow">Access</p>
            <DetailList className="gap-y-2">
              <Detail label="Reaches the internet" className="text-body">
                {data.internal ? "no" : "yes"}
              </Detail>
              <Detail label="IPv6" className="text-body">
                {data.ipv6 ? "on" : "off"}
              </Detail>
              {/*
                Attachable decides only whether a swarm network takes standalone
                containers. A local bridge — Compose's included — accepts a
                running container either way, so the old wording, which sent the
                operator to a compose file for an ordinary bridge, was wrong.
              */}
              <Detail label="Accepts new members" className="text-body leading-relaxed">
                {data.system
                  ? "chosen at creation as a network mode"
                  : ownNetwork
                    ? "no — the dashboard's own"
                    : joinable
                      ? "yes, while running"
                      : "only swarm services"}
              </Detail>
            </DetailList>
          </section>

          {data.members.length > 0 && <NetworkShape name={data.name} members={data.members} />}

          {Object.keys(data.labels ?? {}).length > 0 && (
            <section className="space-y-2">
              <p className="eyebrow">Labels</p>
              <ul className="space-y-1 font-mono text-hint leading-relaxed text-muted-foreground">
                {Object.entries(data.labels).map(([key, value]) => (
                  <li key={key} className="truncate">
                    {key}={value}
                  </li>
                ))}
              </ul>
            </section>
          )}

          <section className="space-y-2">
            <p className="eyebrow">On this network</p>
            <Hint className="leading-relaxed">
              Each of these can reach the others at the names listed beside it. A connection string
              on this network uses one of those names, not an IP address —{" "}
              <span className="font-mono">postgres:5432</span> rather than a number that changes
              when the container restarts.
            </Hint>
            {data.members.length === 0 ? (
              <Hint className="italic">Nothing is attached.</Hint>
            ) : (
              /* Rows with a hairline between them, not a bordered card per
                 member. The row carries `group` so the detach control can
                 appear under the pointer the way every other row's does. */
              <RowList>
                {data.members.map((m) => {
                  const guard = guardedMember(m)
                  return (
                    <Row
                      key={m.id}
                      className="group px-0 py-2"
                      title={m.name}
                      subtitle={
                        [m.ipv4, m.ipv6].filter(Boolean).join(" · ") +
                          ((m.networks ?? []).length
                            ? ` · also on ${m.networks?.join(", ")}`
                            : "") || "no address"
                      }
                      mono
                      trailing={
                        <>
                          {m.dashboard && <Tag>This dashboard</Tag>}
                          {m.ingress && <Tag>Shared ingress</Tag>}
                          {m.unread && <Tag>aliases unread</Tag>}
                          {visibleAliases(m).map((a) => (
                            <Tag key={a} mono>
                              {a}
                            </Tag>
                          ))}
                          {can("service.control") &&
                            !data.system &&
                            (guard ? (
                              <IconAction
                                reveal
                                label={guard}
                                className="text-muted-foreground opacity-40"
                                onClick={() => undefined}
                              >
                                <LockClosed />
                              </IconAction>
                            ) : (
                              <IconAction
                                reveal
                                label={`Detach ${m.name}`}
                                onClick={() => setDetaching(m)}
                              >
                                <Slash />
                              </IconAction>
                            ))}
                        </>
                      }
                    />
                  )
                })}
              </RowList>
            )}
          </section>
        </div>
      )}

      <AttachDialog
        open={attaching}
        networkId={id}
        networkName={data?.name}
        onOpenChange={setAttaching}
        onAttached={() => {
          refresh()
          onChanged()
        }}
        attached={new Set((data?.members ?? []).map((m) => m.id))}
      />
      <NetworkChangeDialog
        open={detaching !== null}
        onOpenChange={(open) => !open && setDetaching(null)}
        target={`${id}:${detaching?.id ?? ""}`}
        title={`Detach ${detaching?.name ?? ""}`}
        description={`What detaching ${detaching?.name ?? "the container"} from ${data?.name ?? "the network"} disturbs, read before it happens.`}
        intro={
          <p className="text-body">
            <b>{detaching?.name}</b> leaves <b>{data?.name}</b> immediately. Reattaching puts it
            back; connections open over this network are cut.
          </p>
        }
        confirmLabel="Detach"
        emptyLabel="Nothing else on this network reaches it, and it keeps its other networks."
        load={(signal) =>
          get(
            `/docker/networks/${encodeURIComponent(id ?? "")}/disconnect`,
            { container: detaching?.id ?? "" },
            signal,
          )
        }
        onConfirm={async () => {
          await post(`/docker/networks/${encodeURIComponent(id ?? "")}/disconnect`, {
            container: detaching?.id,
          })
          notify.success(`${detaching?.name} left ${data?.name}`)
          refresh()
          onChanged()
        }}
      />
    </SidePanel>
  )
}

/**
 * The reviewed prune: the networks the Engine's own prune would take, split
 * into the ones nothing names — removed — and the ones kept, each with why.
 * Only the removable ones' IDs are sent, and the backend rechecks each.
 */
function PruneDialog({
  open,
  onOpenChange,
  onPruned,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onPruned: () => void
}) {
  const reading = usePoll<unknown>(
    (signal) => get("/docker/networks/prune", undefined, signal),
    0,
    [open],
    { enabled: open },
  )
  const [busy, setBusy] = useState(false)
  const preview = isPrunePreview(reading.data) ? reading.data : undefined
  const error =
    reading.error ??
    (reading.data !== undefined && !preview
      ? new Error("The prune preview came back in a shape this page cannot read.")
      : undefined)
  const plan = preview ? prunePlan(preview.candidates) : undefined
  const prune = async () => {
    if (!plan || plan.removed.length === 0) return
    setBusy(true)
    try {
      const result = await post<NetworkPruneResult>("/docker/networks/prune", {
        ids: plan.removed.map((c) => c.id),
      })
      const removed = result.items.length
      notify.success(
        removed
          ? `Removed ${removed} network${removed === 1 ? "" : "s"}${result.skipped.length ? `, kept ${result.skipped.length} that changed since` : ""}`
          : "Nothing was removed",
      )
      onPruned()
      onOpenChange(false)
    } catch (err) {
      notify.error("Could not prune networks", err)
      reading.refresh()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      size="sm"
      title="Remove unused networks"
      description="The networks a prune would remove, and the ones it keeps because something still names them."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={prune}
            disabled={busy || Boolean(error) || !plan || plan.removed.length === 0}
            pending={busy}
          >
            {plan && plan.removed.length > 0 ? `Remove ${plan.removed.length}` : "Remove"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {reading.loading && <LoadingRows rows={2} />}
        {error && (
          <div role="alert" className="space-y-2 text-hint">
            <p className="text-destructive">{errorMessage(error)}</p>
            <p className="text-muted-foreground">Nothing was removed.</p>
            <Button size="xs" variant="outline" onClick={reading.refresh}>
              Try again
            </Button>
          </div>
        )}
        {plan && (
          <>
            <section className="space-y-1.5">
              <p className="eyebrow">Removed</p>
              {plan.removed.length === 0 ? (
                <Hint>No network is unused by every container and every deployment.</Hint>
              ) : (
                <ul aria-label="Networks removed" className="space-y-1">
                  {plan.removed.map((c) => (
                    <li key={c.id} className="text-hint">
                      <span className="font-mono">{c.name}</span>
                      <span className="text-muted-foreground"> · {ownerWords(c.owner)}</span>
                      {c.conflicts.length > 0 && (
                        <span className="block text-muted-foreground">
                          {c.conflicts.map((conflict) => conflict.message).join(" ")}
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </section>
            {plan.kept.length > 0 && (
              <section className="space-y-1.5">
                <p className="eyebrow">Kept</p>
                <ul aria-label="Networks kept" className="space-y-1.5">
                  {plan.kept.map((c) => (
                    <li key={c.id} className="text-hint leading-relaxed">
                      <span className="font-mono">{c.name}</span>
                      <span className="block text-muted-foreground">{c.reason}</span>
                    </li>
                  ))}
                </ul>
                <Hint>
                  Docker&apos;s own prune would remove these too. Each can still be removed on its
                  own after its preview.
                </Hint>
              </section>
            )}
          </>
        )}
      </div>
    </Modal>
  )
}

function AttachDialog({
  open,
  networkId,
  networkName,
  onOpenChange,
  onAttached,
  attached,
}: {
  open: boolean
  networkId: string | null
  networkName?: string
  onOpenChange: (open: boolean) => void
  onAttached: () => void
  attached: Set<string>
}) {
  const candidates = usePoll<Container[]>(
    (signal) => get("/docker/containers/", undefined, signal),
    30_000,
    [networkId, open],
    { enabled: open && Boolean(networkId) },
  )
  const [picked, setPicked] = useState("")
  const [alias, setAlias] = useState("")
  const [busy, setBusy] = useState(false)
  const [settledAlias, setSettledAlias] = useState("")

  // The preview follows the draft, a moment after typing stops.
  useEffect(() => {
    const timer = setTimeout(() => setSettledAlias(alias.trim()), 300)
    return () => clearTimeout(timer)
  }, [alias])
  const preview = usePoll<NetworkChangePreview>(
    (signal) =>
      get<NetworkChangePreview>(
        `/docker/networks/${encodeURIComponent(networkId ?? "")}/connect`,
        settledAlias ? { container: picked, alias: settledAlias } : { container: picked },
        signal,
      ),
    0,
    [networkId, picked, settledAlias],
    { enabled: open && Boolean(networkId) && Boolean(picked) },
  )
  const previewReady =
    !preview.loading &&
    !preview.error &&
    isChangePreview(preview.data) &&
    preview.data.container === picked
  const previewCurrent = previewReady && settledAlias === alias.trim()

  const attach = async () => {
    setBusy(true)
    try {
      await post(`/docker/networks/${networkId}/connect`, {
        container: picked,
        aliases: alias.trim() ? [alias.trim()] : undefined,
      })
      notify.success(`Attached to ${networkName}`)
      onAttached()
      onOpenChange(false)
      setPicked("")
      setAlias("")
    } catch (err) {
      notify.error("Could not attach it", err)
      preview.refresh()
    } finally {
      setBusy(false)
    }
  }

  const available = candidates.data?.filter((c) => !attached.has(c.id)) ?? []

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="sm"
      title="Attach a container"
      description={
        <>
          It joins <b>{networkName}</b> immediately, without restarting, and can reach everything
          else on it by name.
        </>
      }
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={attach}
            disabled={
              busy ||
              !picked ||
              Boolean(candidates.error) ||
              !available.some((c) => c.id === picked) ||
              !previewCurrent ||
              Boolean(preview.data?.blocked)
            }
            pending={busy}
          >
            Attach
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {candidates.data && (
          <NetworkReadWarning
            error={candidates.error}
            refresh={candidates.refresh}
            lastSuccess={candidates.lastSuccess}
            reading="container candidates"
          />
        )}
        {!candidates.data && candidates.error && (
          <div className="space-y-2">
            <ErrorState error={candidates.error} />
            <Button size="xs" variant="outline" onClick={candidates.refresh}>
              Try again
            </Button>
          </div>
        )}
        {candidates.loading && <LoadingRows rows={2} />}
        <div className="space-y-1.5">
          <Label className="text-xs" htmlFor="network-attach-container">
            Container
          </Label>
          <Select
            value={picked}
            onValueChange={setPicked}
            disabled={candidates.loading || Boolean(candidates.error)}
          >
            <SelectTrigger id="network-attach-container" className="w-full">
              <SelectValue placeholder="Pick one" />
            </SelectTrigger>
            <SelectContent>
              {available.map((c) => (
                <SelectItem key={c.id} value={c.id}>
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {candidates.data && !candidates.error && available.length === 0 && (
            <Hint>Every container is already on this network.</Hint>
          )}
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs" htmlFor="network-attach-alias">
            Extra name (optional)
          </Label>
          <Input
            id="network-attach-alias"
            value={alias}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder="db"
            onChange={(e) => setAlias(e.target.value)}
          />
          <Hint>
            An additional hostname the others can use. Useful when an application&apos;s config
            expects a name that is not the container&apos;s.
          </Hint>
        </div>
        {picked && (
          <section aria-label="Before attaching" className="space-y-1.5">
            <p className="eyebrow">Before attaching</p>
            {preview.loading && !previewReady && <LoadingRows rows={1} />}
            {preview.error && (
              <div role="alert" className="space-y-1.5 text-hint">
                <p className="text-destructive">
                  What attaching would run into could not be read: {errorMessage(preview.error)}
                </p>
                <Button size="xs" variant="outline" onClick={preview.refresh}>
                  Check again
                </Button>
              </div>
            )}
            {previewReady && preview.data && (
              <ConflictList
                conflicts={preview.data.conflicts}
                emptyLabel="No name, address or ownership conflict on this network."
              />
            )}
          </section>
        )}
      </div>
    </Modal>
  )
}
