"use client"

import { useEffect, useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { Linked, NetworkDevice, Slash, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import type { Container, DockerNetwork, NetworkDetail, NetworkMember } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { EmptyState, ErrorState, LoadingPanel, LoadingRows } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Group, Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { Detail, DetailList, SearchInput } from "@/components/page"
import { ChipCount, FilterChip } from "@/components/tabs"
import type { ConfirmFn } from "@/components/docker/shared"
import { Hint } from "@/components/docker/explain"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
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
  confirm,
  creating: externalCreating,
  onCreatingChange,
  actions,
}: {
  confirm: ConfirmFn
  creating?: boolean
  onCreatingChange?: (open: boolean) => void
  actions?: React.ReactNode
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

  const { data, error, loading, refresh } = usePoll(
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
  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />

  // The three the Engine owns are never removable, so they are not "unused"
  // in any sense the prune button should count.
  const unused = networks.filter((n) => n.usedBy.length === 0 && !SYSTEM_NETWORKS.includes(n.name))

  return (
    <div className="space-y-4">
      {/* Plain: the list is the page, and each network is a card with its own
          edge — a frame around framed cards is the nesting §12 refuses. */}
      <Panel plain className="animate-rise">
        <PanelHeader
          title="Networks"
          actions={
            <>
              {actions}
              {/* POST /docker/networks/prune has existed since this tab did and
                  nothing ever called it. A network left behind by a removed
                  stack is invisible clutter that also holds a subnet out of the
                  pool, which is what makes a later `compose up` fail to find
                  one. */}
              {can("destructive") && unused.length > 0 && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    confirm({
                      title: "Remove unused networks",
                      confirmLabel: "Remove",
                      description: (
                        <p>
                          Removes the {unused.length} network
                          {unused.length === 1 ? "" : "s"} nothing is attached to:{" "}
                          <b>{unused.map((n) => n.name).join(", ")}</b>. Docker recreates a compose
                          network the next time its stack comes up.
                        </p>
                      ),
                      action: async () => {
                        const rep = await post<{ items: string[] }>("/docker/networks/prune")
                        notify.success(
                          rep.items.length
                            ? `Removed ${rep.items.length} network${rep.items.length === 1 ? "" : "s"}`
                            : "Nothing to remove",
                        )
                        refresh()
                      },
                    })
                  }
                >
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
                  confirm={confirm}
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
      <NewNetworkDialog open={creating} onOpenChange={setCreating} onCreated={refresh} />
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
  confirm,
  onOpen,
  onChanged,
}: {
  network: DockerNetwork
  confirm: ConfirmFn
  onOpen: () => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const system = SYSTEM_NETWORKS.includes(network.name)
  const removable = can("destructive") && !system && network.usedBy.length === 0
  const attached = `${network.usedBy.length} container${network.usedBy.length === 1 ? "" : "s"}`

  return (
    <ChoiceRow
      verb={network.name}
      onSelect={onOpen}
      leading={<ProductLogo fallback={NetworkDevice} size="sm" />}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{network.name}</span>
          {system && <Tag className="shrink-0">Docker system</Tag>}
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
        can("destructive") &&
        (removable ? (
          <IconAction
            label="Remove"
            className="text-destructive"
            onClick={() =>
              confirm({
                title: "Delete network",
                confirmLabel: "Delete",
                description: (
                  <p>
                    Removes <b>{network.name}</b>. Nothing is attached to it, so nothing loses a
                    route. A compose project recreates its own network on the next deploy.
                  </p>
                ),
                action: async (c) => {
                  await del(`/docker/networks/${network.id}`, { confirm: c })
                  onChanged()
                },
              })
            }
          >
            <Trash />
          </IconAction>
        ) : (
          <IconAction
            label={
              system
                ? "Docker's own network — cannot be removed"
                : `In use by ${attached} — cannot be removed while attached`
            }
            className="text-muted-foreground opacity-40"
            onClick={onOpen}
          >
            <Trash />
          </IconAction>
        ))
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
 * faster, and shows aliases and addresses where the ASCII version could not.
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
                {member.ipv4 || "no address"}
              </span>
              {member.aliases
                .filter((a) => a !== member.name && !member.id.startsWith(a))
                .slice(0, 2)
                .map((a) => (
                  <Tag key={a} mono>
                    {a}
                  </Tag>
                ))}
            </li>
          ))}
        </ul>
      </Group>
    </section>
  )
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

  const { data, error, loading, refresh } = usePoll<NetworkDetail>(
    (signal) =>
      get<NetworkDetail>(`/docker/networks/${encodeURIComponent(id ?? "")}`, undefined, signal),
    0,
    [id],
    { enabled: id !== null },
  )

  const disconnect = async (containerId: string, containerName: string) => {
    try {
      await post(`/docker/networks/${id}/disconnect`, { container: containerId })
      notify.success(`${containerName} left ${data?.name}`)
      refresh()
      onChanged()
    } catch (err) {
      notify.error("Could not detach it", err)
    }
  }

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
        (data.attachable ? (
          <Button size="sm" variant="outline" onClick={() => setAttaching(true)}>
            <Linked className="size-3.5" />
            Attach a container
          </Button>
        ) : (
          <Hint className="max-w-64">
            This network only accepts members at creation — attach new containers through the
            compose file, then deploy.
          </Hint>
        ))
      }
    >
      {error && <ErrorState error={error} />}
      {loading && !data && <LoadingRows />}
      {data && (
        <div className="space-y-6">
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
                Attachable decides whether a running container can join this
                network afterwards. Compose creates non-attachable networks by
                default — those only accept members listed in the compose file
                at creation, which is why attaching sometimes fails on a network
                that looks perfectly ordinary.
              */}
              <Detail label="Accepts new members" className="text-body leading-relaxed">
                {data.attachable ? "yes, while running" : "only via the compose file at deploy"}
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
                {data.members.map((m) => (
                  <Row
                    key={m.id}
                    className="group px-0 py-2"
                    title={m.name}
                    subtitle={m.ipv4 || "no address"}
                    mono
                    trailing={
                      <>
                        {m.aliases
                          .filter((a) => a !== m.name && !m.id.startsWith(a))
                          .map((a) => (
                            <Tag key={a} mono>
                              {a}
                            </Tag>
                          ))}
                        {can("service.control") && !data.system && (
                          <IconAction
                            reveal
                            label={`Detach ${m.name}`}
                            onClick={() => disconnect(m.id, m.name)}
                          >
                            <Slash />
                          </IconAction>
                        )}
                      </>
                    }
                  />
                ))}
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
    </SidePanel>
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
  const [containers, setContainers] = useState<Container[]>([])
  const [picked, setPicked] = useState("")
  const [alias, setAlias] = useState("")
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    get<Container[]>("/docker/containers/", undefined, controller.signal)
      .then(setContainers)
      .catch(() => undefined)
    return () => controller.abort()
  }, [open])

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
    } finally {
      setBusy(false)
    }
  }

  const available = containers.filter((c) => !attached.has(c.id))

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
          <Button onClick={attach} disabled={busy || !picked} pending={busy}>
            Attach
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label className="text-xs">Container</Label>
          <Select value={picked} onValueChange={setPicked}>
            <SelectTrigger className="w-full">
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
          {available.length === 0 && <Hint>Every container is already on this network.</Hint>}
        </div>
        <div className="space-y-1.5">
          <Label className="text-xs">Extra name (optional)</Label>
          <Input
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
      </div>
    </Modal>
  )
}

function NewNetworkDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const [name, setName] = useState("")
  const [internal, setInternal] = useState(false)
  const [subnet, setSubnet] = useState("")
  const [busy, setBusy] = useState(false)

  const create = async () => {
    setBusy(true)
    try {
      await post("/docker/networks/", { name, internal, subnet: subnet.trim() || undefined })
      notify.success(`${name} created`)
      onCreated()
      onOpenChange(false)
      setName("")
      setSubnet("")
      setInternal(false)
    } catch (err) {
      notify.error("Could not create the network", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="sm"
      title="Create network"
      description="A private network for containers that need to reach each other. On it, a
            container's name is its hostname."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={create} disabled={busy || !name.trim()} pending={busy}>
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="network-name" className="text-xs">
            Name
          </Label>
          <Input
            id="network-name"
            value={name}
            spellCheck={false}
            className="font-mono"
            placeholder="app-internal"
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <Group className="flex items-start gap-3">
          <Switch
            id="network-internal"
            checked={internal}
            onCheckedChange={setInternal}
            className="mt-0.5"
          />
          <label htmlFor="network-internal" className="cursor-pointer">
            <span className="block text-xs font-medium">Cut it off from the internet</span>
            <Hint>
              Containers on this network can reach each other and nothing else. The right choice for
              a database that only needs to talk to the application in front of it.
            </Hint>
          </label>
        </Group>
        <div className="space-y-1.5">
          <Label htmlFor="network-subnet" className="text-xs">
            Subnet (optional)
          </Label>
          <Input
            id="network-subnet"
            value={subnet}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder="Docker picks one"
            onChange={(e) => setSubnet(e.target.value)}
          />
          <Hint>
            Only worth setting if it has to avoid a range already used on your own network — a VPN
            or an office LAN.
          </Hint>
        </div>
      </div>
    </Modal>
  )
}
