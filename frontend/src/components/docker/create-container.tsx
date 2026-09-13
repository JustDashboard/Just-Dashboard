"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  Box,
  Clipboard,
  CloudUpload,
  Code,
  Copy,
  Globe,
  Layers,
  Plus,
  Servers,
  SettingsSliders,
  ShieldOff,
  Sparkles,
  Trash,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { get, post } from "@/lib/api"
import { parseDockerRun, suggestName, type ParsedRun } from "@/lib/docker-run"
import { usePoll } from "@/hooks/use-poll"
import type {
  ContainerSpec,
  DockerTemplate,
  CreateResult,
  DockerNetwork,
  DockerVolume,
  MountSpec,
  PortMapping,
  SpecPreview,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { SidePanel } from "@/components/side-panel"
import { Notice } from "@/components/state"
import { ExplainIcon, Field, Hint, Term } from "@/components/docker/explain"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Well } from "@/components/panel"
import { Tag } from "@/components/tag"
import { FilterChip } from "@/components/tabs"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { copyText } from "@/lib/clipboard"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle } from "@/components/choice-card"

/**
 * Running something new — the thing this dashboard could not do at all.
 *
 * Portainer's equivalent is twelve accordions of Engine fields, which is a
 * faithful rendering of the API and unusable by anyone who does not already
 * know it. The bet here is different: three ways in that match how people
 * actually arrive at wanting a container, one page of decisions with the
 * reasoning written next to each, and the resulting `docker run` shown back so
 * the form is never a black box.
 *
 * The three entry points matter more than the form does.
 *
 *   - **A starting point** covers "I want to run Postgres" without knowing
 *     that it needs a volume at /var/lib/postgresql/data and refuses to boot
 *     without a password variable.
 *   - **Paste a command** covers the overwhelmingly common case: a README
 *     with a `docker run` line in it and a reader with nowhere to put it.
 *   - **From scratch** is for people who know what they want.
 */

type Mode = "choose" | "form"

const TEMPLATE_CATEGORY_LABEL: Record<string, string> = {
  http: "Web servers",
  database: "Databases",
  tool: "Tools",
  automation: "Automation",
}

export function CreateContainerPanel({
  open,
  onOpenChange,
  onCreated,
  initialSpec,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated?: (result: CreateResult) => void
  /** Pre-filled from "duplicate this container". */
  initialSpec?: ContainerSpec
}) {
  return (
    // Keyed so each opening starts clean rather than showing the last
    // attempt's half-filled form.
    <CreateContainerBody
      key={open ? "open" : "closed"}
      open={open}
      onOpenChange={onOpenChange}
      onCreated={onCreated}
      initialSpec={initialSpec}
    />
  )
}

const blankSpec = (): ContainerSpec => ({
  name: "",
  image: "",
  env: [],
  ports: [],
  mounts: [],
  labels: [],
  networks: [],
  limits: {},
  // The default a server wants, chosen rather than inherited: Docker's own
  // default is "no", which means a reboot silently leaves the service down.
  restartPolicy: "unless-stopped",
  start: true,
  pull: "missing",
})

function CreateContainerBody({
  open,
  onOpenChange,
  onCreated,
  initialSpec,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated?: (result: CreateResult) => void
  initialSpec?: ContainerSpec
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const [mode, setMode] = useState<Mode>(initialSpec ? "form" : "choose")
  const [spec, setSpec] = useState<ContainerSpec>(initialSpec ?? blankSpec())
  const [parseWarnings, setParseWarnings] = useState<string[]>([])
  // Kept apart from the warnings: an unsupported flag is not a parse failure,
  // it is a statement that the container about to be created is not the one
  // the pasted command describes. And the command itself is kept so the
  // operator can go back to it — the form is a reading of it, not a
  // replacement for it.
  const [unsupported, setUnsupported] = useState<string[]>([])
  const [original, setOriginal] = useState("")
  const [busy, setBusy] = useState(false)
  const [tab, setTab] = useState("setup")

  const patch = useCallback(
    (next: Partial<ContainerSpec>) => setSpec((s) => ({ ...s, ...next })),
    [],
  )

  const start = (
    next: ContainerSpec,
    parsed?: Pick<ParsedRun, "warnings" | "unsupported" | "original">,
  ) => {
    setSpec(next)
    setParseWarnings(parsed?.warnings ?? [])
    setUnsupported(parsed?.unsupported ?? [])
    setOriginal(parsed?.original ?? "")
    setMode("form")
    setTab("setup")
  }

  const create = async () => {
    if (!spec.image.trim()) {
      notify.error("An image is required")
      setTab("setup")
      return
    }
    setBusy(true)
    try {
      const result = await post<CreateResult>("/docker/containers/", spec)
      if (result.warnings.length > 0) {
        notify.warning(
          `${result.name} created, with ${result.warnings.length} thing(s) worth knowing`,
          {
            description: result.warnings[0],
            duration: 10000,
          },
        )
      } else {
        notify.success(`${result.name} is ${result.started ? "running" : "created"}`)
      }
      onCreated?.(result)
      onOpenChange(false)
    } catch (err) {
      notify.error("Could not create the container", err, { duration: 12000 })
    } finally {
      setBusy(false)
    }
  }

  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      width="xl"
      title={mode === "choose" ? "Run something new" : spec.name || "New container"}
      description={
        mode === "choose"
          ? "Three ways to start. All of them end at the same form, which you can edit before anything runs."
          : spec.image || "Pick an image to run"
      }
      bodyClassName="flex min-h-0 flex-1 flex-col p-4"
      footer={
        mode === "form" && (
          <>
            <Button variant="ghost" size="sm" onClick={() => setMode("choose")}>
              Start over
            </Button>
            <div className="ml-auto flex items-center gap-2">
              <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
                <Switch
                  checked={spec.start}
                  onCheckedChange={(v) => patch({ start: v })}
                  aria-label="Start it after creating"
                />
                Start it now
              </label>
              <Button
                size="sm"
                onClick={create}
                disabled={busy || !spec.image.trim()}
                pending={busy}
              >
                <CloudUpload className="size-4" />
                {spec.start ? "Create and start" : "Create"}
              </Button>
            </div>
          </>
        )
      }
    >
      {mode === "choose" ? (
        <ChooseStart onPick={start} />
      ) : (
        <div className="flex min-h-0 flex-1 flex-col gap-3">
          {/*
            An unsupported flag is the one thing here that must never be
            silent. `--gpus all` dropped without a word produces a container
            that starts and then has no GPU, and the operator finds out from
            the application failing rather than from the form.
          */}
          {unsupported.length > 0 && (
            <Notice title="These flags are not in the visual editor" icon={Warning} tone="warning">
              <ul className="ml-4 list-disc space-y-1 font-mono text-hint">
                {unsupported.map((flag, i) => (
                  <li key={i}>{flag}</li>
                ))}
              </ul>
              <p className="mt-2">
                They were understood and left out, so the container this form creates is not the one
                the command describes. Run the original command in a shell if any of them matters.
              </p>
            </Notice>
          )}
          {parseWarnings.length > 0 && (
            <Notice title="Read before creating" icon={Warning} tone="warning">
              <ul className="ml-4 list-disc space-y-1">
                {parseWarnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </Notice>
          )}
          {original && (
            <details className="text-xs">
              <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
                The command you pasted
              </summary>
              <pre className="mt-1 overflow-x-auto rounded-sm border border-hairline bg-surface-header/40 p-2 font-mono text-hint whitespace-pre">
                {original}
              </pre>
            </details>
          )}
          <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-3">
            <TabsList className="w-fit shrink-0">
              <TabsTrigger value="setup">Setup</TabsTrigger>
              <TabsTrigger value="advanced">Advanced</TabsTrigger>
              <TabsTrigger value="command">Command</TabsTrigger>
            </TabsList>
            <TabsContent value="setup" className="min-h-0 flex-1 space-y-5 overflow-y-auto pr-1">
              <SetupFields spec={spec} patch={patch} />
            </TabsContent>
            <TabsContent value="advanced" className="min-h-0 flex-1 space-y-5 overflow-y-auto pr-1">
              <AdvancedFields spec={spec} patch={patch} admin={admin} />
            </TabsContent>
            <TabsContent value="command" className="min-h-0 flex-1 overflow-y-auto pr-1">
              <CommandPreview spec={spec} />
            </TabsContent>
          </Tabs>
        </div>
      )}
    </SidePanel>
  )
}

/* ------------------------------------------------------------------ start -- */

/**
 * The two routes that have something to show, plus the one that does not.
 *
 * This screen used to be all three at once, stacked: three icon headings, three
 * paragraphs of explanation, a category strip, nine template cards each
 * carrying a four-line warning, a textarea, and two buttons — about nine
 * hundred pixels of chrome around one decision. The decision is the first
 * thing now, as three cards on one line, and only the route you picked spends
 * any height. "From scratch" has nothing to configure, so it does not select:
 * it opens the empty form directly.
 */
type StartRoute = "template" | "paste"

const START_ROUTES: {
  key: StartRoute
  icon: React.ComponentType<{ className?: string }>
  title: string
  hint: string
}[] = [
  {
    key: "template",
    icon: Sparkles,
    title: "Something common",
    hint: "Postgres, Redis, Nginx — filled in with the ports and storage each needs.",
  },
  {
    key: "paste",
    icon: Clipboard,
    title: "A command you found",
    hint: "Turn a docker run line from a README into a form you can read.",
  },
]

function ChooseStart({
  onPick,
}: {
  onPick: (
    spec: ContainerSpec,
    parsed?: Pick<ParsedRun, "warnings" | "unsupported" | "original">,
  ) => void
}) {
  const [route, setRoute] = useState<StartRoute>("template")
  const [pasted, setPasted] = useState("")
  // The starting points come from the server's reviewed blueprint catalogue,
  // so the container form and Deployments offer the same images, ports and
  // storage rather than two lists that drift apart.
  const templates = usePoll(
    (signal) => get<DockerTemplate[]>("/docker/templates", undefined, signal),
    0,
    [],
  )

  const convert = () => {
    const parsed = parseDockerRun(pasted)
    if (!parsed.spec.image) {
      notify.error(
        "That does not look like a docker run command",
        parsed.warnings[0] ?? "It should end with an image name.",
      )
      return
    }
    // A pasted command usually has no restart policy because it was written
    // for a one-off run. On a server, defaulting to "no" means the service
    // does not come back after a reboot, which is never what was meant.
    onPick({ ...parsed.spec, restartPolicy: parsed.spec.restartPolicy || "unless-stopped" }, parsed)
  }

  return (
    <div className="min-w-0 space-y-4">
      <div className="grid gap-2 sm:grid-cols-3 [&>*]:min-w-0">
        {START_ROUTES.map((item) => (
          <ChoiceCard
            key={item.key}
            selected={route === item.key}
            onClick={() => setRoute(item.key)}
            className="gap-1"
          >
            <span className="flex w-full min-w-0 items-center gap-2">
              <item.icon className="size-3.5 shrink-0 text-brand" />
              <ChoiceCardTitle className="truncate">{item.title}</ChoiceCardTitle>
            </span>
            <ChoiceCardHint>{item.hint}</ChoiceCardHint>
          </ChoiceCard>
        ))}
        <ChoiceCard onClick={() => onPick(blankSpec())} className="gap-1">
          <span className="flex w-full min-w-0 items-center gap-2">
            <Box className="size-3.5 shrink-0 text-brand" />
            <ChoiceCardTitle className="truncate">From scratch</ChoiceCardTitle>
          </span>
          <ChoiceCardHint>An empty form, with every field explained beside it.</ChoiceCardHint>
        </ChoiceCard>
      </div>

      {route === "template" ? (
        <TemplatePicker templates={templates.data ?? []} onPick={onPick} />
      ) : (
        <PasteRun value={pasted} onChange={setPasted} onConvert={convert} />
      )}
    </div>
  )
}

/**
 * The catalogue, as a grid of one-glance cards.
 *
 * What a card had to stop carrying is the `requires` paragraph. "Set
 * MARIADB_PASSWORD, MARIADB_ROOT_PASSWORD before starting it. Deploying this
 * through Deployments generates the secrets for you." is four lines of
 * instruction attached to a thing you have not chosen yet, repeated on every
 * card that has one — three of them side by side turned the row into a wall of
 * orange text. It is now one word on the card and a full notice at the top of
 * the form the moment the template is picked, which is both shorter and the
 * place the instruction can actually be followed.
 */
function TemplatePicker({
  templates,
  onPick,
}: {
  templates: DockerTemplate[]
  onPick: (
    spec: ContainerSpec,
    parsed?: Pick<ParsedRun, "warnings" | "unsupported" | "original">,
  ) => void
}) {
  const categories = useMemo(() => {
    const seen: DockerTemplate["category"][] = []
    for (const template of templates) {
      if (!seen.includes(template.category)) seen.push(template.category)
    }
    return seen
  }, [templates])
  const [category, setCategory] = useState<DockerTemplate["category"] | null>(null)
  const active = category ?? categories[0] ?? "http"

  if (templates.length === 0) {
    return <Hint>The reviewed catalogue is empty on this install.</Hint>
  }

  return (
    <section className="min-w-0 space-y-2.5">
      <div className="flex min-w-0 flex-wrap items-center gap-1">
        {categories.map((id) => (
          <FilterChip key={id} selected={active === id} onClick={() => setCategory(id)}>
            {TEMPLATE_CATEGORY_LABEL[id] ?? id}
          </FilterChip>
        ))}
        <span className="ml-auto flex items-center gap-1.5 text-hint text-muted-foreground">
          Bound to this server only
          <ExplainIcon name="templateBinding" />
        </span>
      </div>

      <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3 [&>*]:min-w-0">
        {templates
          .filter((template) => template.category === active)
          .map((template) => (
            <ChoiceCard
              key={template.id}
              className="gap-1"
              onClick={() =>
                onPick(template.spec, {
                  warnings: template.requires ? [template.requires] : [],
                  unsupported: [],
                  original: "",
                })
              }
            >
              <span className="flex w-full min-w-0 items-center gap-2">
                <ChoiceCardTitle className="truncate">{template.name}</ChoiceCardTitle>
                {template.requires && (
                  <Tag tone="warning" icon={Warning} className="shrink-0">
                    setup
                  </Tag>
                )}
                <Tag mono className="ml-auto max-w-[45%] truncate">
                  {template.spec.image}
                </Tag>
              </span>
              <ChoiceCardHint className="line-clamp-2">{template.blurb}</ChoiceCardHint>
            </ChoiceCard>
          ))}
      </div>
    </section>
  )
}

function PasteRun({
  value,
  onChange,
  onConvert,
}: {
  value: string
  onChange: (next: string) => void
  onConvert: () => void
}) {
  return (
    <section className="min-w-0 space-y-2.5">
      <Textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        spellCheck={false}
        rows={5}
        className="font-mono text-xs"
        placeholder={
          "docker run -d \\\n  --name uptime-kuma \\\n  -p 3001:3001 \\\n  -v uptime-kuma:/app/data \\\n  louislam/uptime-kuma:1"
        }
      />
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" onClick={onConvert} disabled={!value.trim()}>
          <Sparkles className="size-4" />
          Read this command
        </Button>
        <span className="flex items-center gap-1.5 text-hint text-muted-foreground">
          Nothing runs yet
          <ExplainIcon name="pastedCommand" />
        </span>
      </div>
    </section>
  )
}

/* ------------------------------------------------------------------ setup -- */

type PatchFn = (next: Partial<ContainerSpec>) => void

function SetupFields({ spec, patch }: { spec: ContainerSpec; patch: PatchFn }) {
  const volumes = useDockerList<DockerVolume>("/docker/volumes/")

  return (
    <>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label="Image"
          term="image"
          required
          htmlFor="spec-image"
          // The one hint in this form that survives as prose, because it is
          // about what you are typing *right now* and it changes as you type:
          // leaving the tag off is the mistake, and saying so after the fact
          // is saying it too late.
          hint={
            spec.image.trim() && !spec.image.includes(":") ? (
              <span className="text-warning">
                No version given, so this means <span className="font-mono">latest</span> — whatever
                the publisher last pushed. <Term name="tag">Why that matters</Term>
              </span>
            ) : undefined
          }
        >
          <Input
            id="spec-image"
            value={spec.image}
            spellCheck={false}
            placeholder="nginx:alpine"
            onChange={(e) => {
              const image = e.target.value
              // Name follows the image until the operator types their own,
              // which saves the commonest keystroke in this form.
              const shouldTrack = !spec.name || spec.name === suggestName(spec.image)
              patch(shouldTrack ? { image, name: suggestName(image) } : { image })
            }}
          />
        </Field>
        <Field label="Name" term="containerName" htmlFor="spec-name">
          <Input
            id="spec-name"
            value={spec.name}
            spellCheck={false}
            placeholder="my-app"
            onChange={(e) => patch({ name: e.target.value })}
          />
        </Field>
      </div>

      <Field
        label="When it stops"
        term="restart"
        hint={
          spec.restartPolicy === "no" || !spec.restartPolicy
            ? "This container will not come back when the server reboots."
            : undefined
        }
      >
        <Select
          value={spec.restartPolicy || "no"}
          onValueChange={(v) => patch({ restartPolicy: v })}
        >
          <SelectTrigger className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="unless-stopped">
              Start it again — unless I stopped it myself
            </SelectItem>
            <SelectItem value="always">Always start it again, even if I stopped it</SelectItem>
            <SelectItem value="on-failure">Only start it again if it crashed</SelectItem>
            <SelectItem value="no">Leave it stopped</SelectItem>
          </SelectContent>
        </Select>
      </Field>

      <PortEditor spec={spec} patch={patch} />
      <MountEditor spec={spec} patch={patch} volumes={volumes} />
      <EnvEditor spec={spec} patch={patch} />
    </>
  )
}

/**
 * A section's name, and the paragraph behind it.
 *
 * It used to carry the paragraph itself. Six sections × three lines of
 * explanation is eighteen lines of prose in a form with about twelve controls
 * in it — an expert scrolls past all of it every single time, and a newcomer
 * reads it once and then scrolls past it too. The `?` is the same gesture the
 * rest of the product uses for exactly this, and it costs nothing at rest.
 */
function SectionHeading({
  icon: Icon,
  title,
  term,
  action,
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  term?: string
  action?: React.ReactNode
}) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-2">
      <h3 className="flex min-w-0 items-center gap-2 text-body font-medium">
        <Icon className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate">{title}</span>
        {term && <ExplainIcon name={term} />}
      </h3>
      {action}
    </div>
  )
}

/**
 * Where a published port should be reachable from, offered as a choice rather
 * than as an address.
 *
 * "Bind address" is a question about netmasks; "who should be able to reach
 * this" is a question about intent, and they have the same answer. The default
 * is the safe one — a service that needs to be public almost always wants to
 * be public *through the reverse proxy*, which reaches it on loopback like
 * anything else on this server.
 *
 * The LAN option is populated from the machine's actual addresses rather than
 * offered as an abstraction: "Local network" with nothing behind it would be
 * the dashboard guessing which interface somebody meant.
 */
function PortEditor({ spec, patch }: { spec: ContainerSpec; patch: PatchFn }) {
  const ports = spec.ports ?? []
  const update = (i: number, next: Partial<PortMapping>) =>
    patch({ ports: ports.map((p, idx) => (idx === i ? { ...p, ...next } : p)) })

  // The host's own addresses, so "one interface" names a real one rather than
  // an abstraction the dashboard would have to guess at. Read once, like the
  // form's other pickers; a failure here costs the LAN options and nothing
  // else.
  const net = useHostAddresses()

  const bindable = (net?.interfaces ?? [])
    .filter((iface) => iface.up && !iface.loopback)
    .flatMap((iface) =>
      iface.addresses
        .map((cidr) => cidr.split("/")[0])
        .filter((address) => address.includes(".") && !address.startsWith("169.254"))
        .map((address) => ({ address, name: iface.name })),
    )

  return (
    <section className="space-y-2.5">
      <SectionHeading
        icon={Globe}
        title="Ports"
        term="port"
        action={
          <Button
            size="xs"
            variant="outline"
            onClick={() =>
              patch({
                ports: [
                  ...ports,
                  { hostIp: "127.0.0.1", hostPort: 8080, containerPort: 80, protocol: "tcp" },
                ],
              })
            }
          >
            <Plus className="size-3" />
            Add
          </Button>
        }
      />

      {ports.length === 0 && (
        <Hint>Nothing published. Containers on the same network still reach it by name.</Hint>
      )}
      {ports.map((port, i) => (
        <div key={i} className="flex flex-wrap items-end gap-2">
          <div className="min-w-32 flex-1">
            <Label className="text-micro text-muted-foreground">Reachable from</Label>
            <Select
              value={port.hostIp || "any"}
              onValueChange={(v) => update(i, { hostIp: v === "any" ? "" : v })}
            >
              <SelectTrigger className="h-8 w-full text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="127.0.0.1">This server only</SelectItem>
                {bindable.map((iface) => (
                  <SelectItem key={iface.address} value={iface.address}>
                    {iface.address} ({iface.name}) — whatever can reach that address
                  </SelectItem>
                ))}
                <SelectItem value="any">Every interface</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="w-24">
            <Label className="text-micro text-muted-foreground">Server port</Label>
            <Input
              type="number"
              className="h-8 text-xs"
              value={port.hostPort || ""}
              placeholder="none"
              onChange={(e) => update(i, { hostPort: Number(e.target.value) || 0 })}
            />
          </div>
          <span className="pb-2 text-xs text-muted-foreground">→</span>
          <div className="w-24">
            <Label className="text-micro text-muted-foreground">Container port</Label>
            <Input
              type="number"
              className="h-8 text-xs"
              value={port.containerPort || ""}
              onChange={(e) => update(i, { containerPort: Number(e.target.value) || 0 })}
            />
          </div>
          <div className="w-20">
            <Label className="text-micro text-muted-foreground">Protocol</Label>
            <Select
              value={port.protocol || "tcp"}
              onValueChange={(v) => update(i, { protocol: v })}
            >
              <SelectTrigger className="h-8 w-full text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="tcp">TCP</SelectItem>
                <SelectItem value="udp">UDP</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Remove this port"
            className="text-destructive"
            onClick={() => patch({ ports: ports.filter((_, idx) => idx !== i) })}
          >
            <Trash className="size-3.5" />
          </Button>
        </div>
      ))}
      {ports.some((p) => p.hostPort > 0 && !p.hostIp) && (
        <Notice title="Published on every interface" icon={ShieldOff} tone="warning">
          <p>
            Docker publishes ports with NAT rules that are consulted before the firewall&apos;s own,
            so this will be reachable from anywhere that can route to this server — even if the
            firewall appears to deny it. Closing it later means changing the binding, not adding a
            firewall rule.
          </p>
          <p className="mt-1">
            If the internet needs to reach this, the usual answer is still 127.0.0.1 with a reverse
            proxy site in front: the proxy reaches it like anything else on this server, and its
            TLS, logging and access rules then apply.
          </p>
        </Notice>
      )}
    </section>
  )
}

function MountEditor({
  spec,
  patch,
  volumes,
}: {
  spec: ContainerSpec
  patch: PatchFn
  volumes: DockerVolume[]
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const mounts = spec.mounts ?? []
  const update = (i: number, next: Partial<MountSpec>) =>
    patch({ mounts: mounts.map((m, idx) => (idx === i ? { ...m, ...next } : m)) })

  return (
    <section className="space-y-2.5">
      <SectionHeading
        icon={Servers}
        title="Storage"
        term="containerStorage"
        action={
          <Button
            size="xs"
            variant="outline"
            onClick={() =>
              patch({ mounts: [...mounts, { type: "volume", source: "", target: "" }] })
            }
          >
            <Plus className="size-3" />
            Add
          </Button>
        }
      />

      {mounts.length === 0 && (
        <Hint>Nothing attached. Fine for something stateless; not for a database.</Hint>
      )}
      {mounts.map((mount, i) => (
        <div key={i} className="space-y-1.5 rounded-lg border border-hairline p-2.5">
          <div className="flex flex-wrap items-end gap-2">
            <div className="w-44">
              {/* The `?` follows the selection: whichever kind is chosen, the
                  hover card explains that one. It replaces a help paragraph
                  that was printed under every mount row in the form. */}
              <Label className="flex items-center gap-1 text-micro text-muted-foreground">
                Kind
                <ExplainIcon name={mount.type === "volume" ? "volume" : mount.type} />
              </Label>
              <Select
                value={mount.type}
                onValueChange={(v) => update(i, { type: v as MountSpec["type"] })}
              >
                <SelectTrigger className="h-8 w-full text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="volume">Managed volume</SelectItem>
                  <SelectItem value="bind" disabled={!admin}>
                    Folder on this server{admin ? "" : " (admin only)"}
                  </SelectItem>
                  <SelectItem value="tmpfs">Temporary memory</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {mount.type !== "tmpfs" && (
              <div className="min-w-40 flex-1">
                <Label className="text-micro text-muted-foreground">
                  {mount.type === "bind" ? "Folder on the server" : "Volume name"}
                </Label>
                <Input
                  className="h-8 font-mono text-xs"
                  spellCheck={false}
                  list={mount.type === "volume" ? "docker-volume-names" : undefined}
                  value={mount.source ?? ""}
                  placeholder={mount.type === "bind" ? "/srv/app/config" : "my-app-data"}
                  onChange={(e) => update(i, { source: e.target.value })}
                />
              </div>
            )}
            <div className="min-w-40 flex-1">
              <Label className="text-micro text-muted-foreground">Path inside the container</Label>
              <Input
                className="h-8 font-mono text-xs"
                spellCheck={false}
                value={mount.target}
                placeholder="/data"
                onChange={(e) => update(i, { target: e.target.value })}
              />
            </div>
            <label className="flex h-8 cursor-pointer items-center gap-1.5 text-hint text-muted-foreground">
              <Switch
                checked={mount.readOnly ?? false}
                onCheckedChange={(v) => update(i, { readOnly: v })}
                aria-label="Read only"
              />
              read-only
            </label>
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="Remove this mount"
              className="text-destructive"
              onClick={() => patch({ mounts: mounts.filter((_, idx) => idx !== i) })}
            >
              <Trash className="size-3.5" />
            </Button>
          </div>
          {mount.type === "volume" && !mount.source && (
            <Hint className="text-warning">
              An unnamed volume gets a random hash for a name. It survives, but you will not
              recognise it in the volumes list later.
            </Hint>
          )}
        </div>
      ))}
      <datalist id="docker-volume-names">
        {volumes.map((v) => (
          <option key={v.name} value={v.name} />
        ))}
      </datalist>
    </section>
  )
}

function EnvEditor({ spec, patch }: { spec: ContainerSpec; patch: PatchFn }) {
  const env = spec.env ?? []
  const [bulk, setBulk] = useState(false)
  const [text, setText] = useState("")

  const openBulk = () => {
    setText(env.map((e) => `${e.name}=${e.value}`).join("\n"))
    setBulk(true)
  }
  const applyBulk = () => {
    const next = text
      .split("\n")
      .map((line) => line.trim())
      .filter((line) => line && !line.startsWith("#"))
      .map((line) => {
        const eq = line.indexOf("=")
        return eq === -1
          ? { name: line, value: "" }
          : { name: line.slice(0, eq).trim(), value: line.slice(eq + 1) }
      })
    patch({ env: next })
    setBulk(false)
  }

  return (
    <section className="space-y-2.5">
      <SectionHeading
        icon={SettingsSliders}
        title="Settings"
        term="env"
        action={
          <div className="flex gap-1.5">
            <Button size="xs" variant="ghost" onClick={bulk ? applyBulk : openBulk}>
              {bulk ? "Done" : "Paste .env"}
            </Button>
            {!bulk && (
              <Button
                size="xs"
                variant="outline"
                onClick={() => patch({ env: [...env, { name: "", value: "" }] })}
              >
                <Plus className="size-3" />
                Add
              </Button>
            )}
          </div>
        }
      />

      {bulk ? (
        <Textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={8}
          spellCheck={false}
          className="font-mono text-xs"
          placeholder={"KEY=value\nANOTHER_KEY=value"}
        />
      ) : (
        <>
          {env.length === 0 && <Hint className="italic">No settings passed.</Hint>}
          {env.map((row, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                className="h-8 w-2/5 font-mono text-xs"
                spellCheck={false}
                value={row.name}
                placeholder="NAME"
                onChange={(e) =>
                  patch({
                    env: env.map((x, idx) => (idx === i ? { ...x, name: e.target.value } : x)),
                  })
                }
              />
              <Input
                className="h-8 flex-1 font-mono text-xs"
                spellCheck={false}
                value={row.value}
                placeholder="value"
                onChange={(e) =>
                  patch({
                    env: env.map((x, idx) => (idx === i ? { ...x, value: e.target.value } : x)),
                  })
                }
              />
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="Remove this setting"
                className="text-destructive"
                onClick={() => patch({ env: env.filter((_, idx) => idx !== i) })}
              >
                <Trash className="size-3.5" />
              </Button>
            </div>
          ))}
        </>
      )}
    </section>
  )
}

/* --------------------------------------------------------------- advanced -- */

function AdvancedFields({
  spec,
  patch,
  admin,
}: {
  spec: ContainerSpec
  patch: PatchFn
  admin: boolean
}) {
  const networks = useDockerList<DockerNetwork>("/docker/networks/")
  const attachable = networks.filter((n) => !["host", "none"].includes(n.name))

  return (
    <>
      <section className="space-y-2.5">
        <SectionHeading icon={Layers} title="Networks" term="network" />
        <div className="flex flex-wrap gap-1.5">
          {attachable.map((net) => {
            const on = (spec.networks ?? []).includes(net.name)
            return (
              <Button
                key={net.id}
                size="xs"
                variant={on ? "secondary" : "outline"}
                onClick={() =>
                  patch({
                    networks: on
                      ? (spec.networks ?? []).filter((n) => n !== net.name)
                      : [...(spec.networks ?? []), net.name],
                    networkMode: "",
                  })
                }
              >
                {net.name}
              </Button>
            )
          })}
        </div>
        {admin && (
          <ToggleRow
            label="Share the server's network directly"
            term="hostNetwork"
            hint={
              spec.networkMode === "host"
                ? "Published ports are ignored, and it can reach anything bound to 127.0.0.1 on this server."
                : undefined
            }
            checked={spec.networkMode === "host"}
            onChange={(v) => patch({ networkMode: v ? "host" : "", networks: [] })}
          />
        )}
      </section>

      <section className="grid gap-4 sm:grid-cols-2">
        <Field
          label="Memory limit"
          term="memoryLimit"
          hint={spec.limits.memoryMb ? undefined : "No limit — a leak here takes the server down."}
        >
          <div className="flex items-center gap-2">
            <Input
              type="number"
              value={spec.limits.memoryMb ?? ""}
              placeholder="unlimited"
              onChange={(e) =>
                patch({ limits: { ...spec.limits, memoryMb: Number(e.target.value) || undefined } })
              }
            />
            <span className="text-xs text-muted-foreground">MB</span>
          </div>
        </Field>
        <Field label="CPU limit" term="cpuLimit">
          <Input
            type="number"
            step="0.5"
            value={spec.limits.cpus ?? ""}
            placeholder="unlimited"
            onChange={(e) =>
              patch({ limits: { ...spec.limits, cpus: Number(e.target.value) || undefined } })
            }
          />
        </Field>
      </section>

      <section className="grid gap-4 sm:grid-cols-2">
        <Field label="Run as user" term="containerUser">
          <Input
            value={spec.user ?? ""}
            spellCheck={false}
            placeholder="image default"
            onChange={(e) => patch({ user: e.target.value })}
          />
        </Field>
        <Field label="Working directory" term="workingDir">
          <Input
            value={spec.workingDir ?? ""}
            spellCheck={false}
            placeholder="image default"
            onChange={(e) => patch({ workingDir: e.target.value })}
          />
        </Field>
      </section>

      <Field label="Command" term="command">
        <Input
          value={(spec.command ?? []).join(" ")}
          spellCheck={false}
          className="font-mono text-xs"
          placeholder="image default"
          onChange={(e) =>
            patch({ command: e.target.value.trim() ? e.target.value.split(/\s+/) : undefined })
          }
        />
      </Field>

      <section className="space-y-2">
        <SectionHeading icon={ShieldOff} title="Behaviour" />
        <ToggleRow
          label="Run an init process"
          term="initProcess"
          checked={spec.init ?? false}
          onChange={(v) => patch({ init: v })}
        />
        <ToggleRow
          label="Read-only filesystem"
          term="readOnlyRootfs"
          checked={spec.readOnlyRootfs ?? false}
          onChange={(v) => patch({ readOnlyRootfs: v })}
        />
        <ToggleRow
          label="Pull a fresh image first"
          term="pullPolicy"
          checked={spec.pull === "always"}
          onChange={(v) => patch({ pull: v ? "always" : "missing" })}
        />
        {admin && (
          <ToggleRow
            label="Privileged"
            danger
            hint="Removes almost every restriction between the container and this server. Anything that breaks into it has the machine."
            checked={spec.privileged ?? false}
            onChange={(v) => patch({ privileged: v })}
          />
        )}
      </section>
    </>
  )
}

/**
 * One switch, one line.
 *
 * These were a switch above two lines of justification each, so four of them
 * filled a screen with text nobody reads twice. The justification is behind the
 * `?`; what stays on the row is the thing being turned on — except for
 * `privileged`, which keeps its sentence because it is the one control in this
 * form that can hand the server away, and a reader should not have to hover to
 * find that out.
 */
function ToggleRow({
  label,
  term,
  hint,
  checked,
  onChange,
  danger,
}: {
  label: string
  term?: string
  hint?: string
  checked: boolean
  onChange: (v: boolean) => void
  danger?: boolean
}) {
  return (
    <label className="flex cursor-pointer items-center gap-3 rounded-lg border border-hairline px-2.5 py-2">
      <Switch checked={checked} onCheckedChange={onChange} aria-label={label} />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          <span
            className={cn("truncate text-xs font-medium", danger && checked && "text-destructive")}
          >
            {label}
          </span>
          {term && <ExplainIcon name={term} />}
        </span>
        {hint && <Hint>{hint}</Hint>}
      </span>
    </label>
  )
}

/* ---------------------------------------------------------------- preview -- */

/**
 * The command the form would run, and the compose file it would become.
 *
 * Rendered by the server rather than in the browser so there is exactly one
 * implementation of "what does this spec mean" — a second one here would drift,
 * and the version that mattered would be the one nobody was reading.
 */
function CommandPreview({ spec }: { spec: ContainerSpec }) {
  const [preview, setPreview] = useState<SpecPreview>()
  const [error, setError] = useState<string>()
  const signature = JSON.stringify(spec)
  const latest = useRef(0)

  useEffect(() => {
    const seq = ++latest.current
    const controller = new AbortController()
    post<SpecPreview>("/docker/containers/preview", spec, { signal: controller.signal })
      .then((res) => {
        if (seq === latest.current) {
          setPreview(res)
          setError(undefined)
        }
      })
      .catch((err) => {
        if (!controller.signal.aborted && seq === latest.current) setError(String(err))
      })
    return () => controller.abort()
    // The spec is compared by value: this re-renders on every keystroke and
    // the request is cheap, but firing one per referentially-new object would
    // mean one per render instead.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature])

  const copy = (text: string, what: string) => void copyText(text, `${what} copied`)

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <SectionHeading icon={Code} title="The equivalent command" term="equivalentCommand" />
        <div className="relative">
          <Well className="max-h-64 whitespace-pre-wrap">{preview?.run ?? "…"}</Well>
          {preview && (
            <Button
              size="xs"
              variant="outline"
              className="absolute top-2 right-2"
              onClick={() => copy(preview.run, "Command")}
            >
              <Copy className="size-3" />
              Copy
            </Button>
          )}
        </div>
      </div>

      <div className="space-y-2">
        <SectionHeading icon={Layers} title="The same thing as compose" term="composeExport" />
        <div className="relative">
          <Well className="max-h-80 whitespace-pre-wrap">{preview?.compose ?? "…"}</Well>
          {preview && (
            <Button
              size="xs"
              variant="outline"
              className="absolute top-2 right-2"
              onClick={() => copy(preview.compose, "Compose file")}
            >
              <Copy className="size-3" />
              Copy
            </Button>
          )}
        </div>
      </div>
      {error && <Hint className="text-destructive">{error}</Hint>}
    </div>
  )
}

/* ----------------------------------------------------------------- shared -- */

/**
 * A one-shot list fetch for the pickers in this form.
 *
 * Not usePoll: these choices are read once while a dialog is open, and a
 * volume appearing under the cursor mid-edit would be worse than a list that
 * is thirty seconds stale.
 */
type HostNetwork = {
  interfaces: { name: string; addresses: string[]; loopback: boolean; up: boolean }[]
}

/**
 * The machine's own addresses, for the port editor's bind choices.
 *
 * Best effort: a principal without the security capability gets nothing, which
 * costs the named-interface options and leaves loopback and every-interface —
 * the two that matter — working exactly as before.
 */
function useHostAddresses(): HostNetwork | undefined {
  const [net, setNet] = useState<HostNetwork>()
  useEffect(() => {
    const controller = new AbortController()
    get<HostNetwork>("/security/network", undefined, controller.signal)
      .then(setNet)
      .catch(() => undefined)
    return () => controller.abort()
  }, [])
  return net
}

function useDockerList<T>(path: string): T[] {
  const [items, setItems] = useState<T[]>([])
  useEffect(() => {
    const controller = new AbortController()
    get<T[]>(path, undefined, controller.signal)
      .then(setItems)
      .catch(() => undefined)
    return () => controller.abort()
  }, [path])
  return items
}

/**
 * Pulls an image over the progress socket before a create that needs it.
 *
 * Exported for the images tab, which offers the same thing on its own: this is
 * the only place in the product that shows layer progress, and a pull without
 * it is a spinner that lasts four minutes.
 */
export function usePullProgress() {
  const [ref, setRef] = useState<string>()
  const [lines, setLines] = useState<string[]>([])
  const [done, setDone] = useState(false)
  const resolveRef = useRef<(ok: boolean) => void>(undefined)

  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "progress") {
      const msg = envelope.data as { id?: string; status: string; progress?: string }
      setLines((prev) => {
        const text = [msg.id, msg.status, msg.progress].filter(Boolean).join(" ")
        return [...prev.slice(-200), text]
      })
    } else if (envelope.type === "done") {
      setDone(true)
      resolveRef.current?.(true)
    } else if (envelope.type === "error") {
      setLines((prev) => [...prev, envelope.error ?? "pull failed"])
      setDone(true)
      resolveRef.current?.(false)
    }
  }, [])

  const query = useMemo(() => ({ ref: ref ?? "" }), [ref])
  useSocket("/docker/images/pull", { onMessage, enabled: Boolean(ref), query })

  const pull = (image: string) => {
    setLines([])
    setDone(false)
    setRef(image)
    return new Promise<boolean>((resolve) => {
      resolveRef.current = resolve
    })
  }
  const reset = () => {
    setRef(undefined)
    setLines([])
    setDone(false)
  }
  return { pull, reset, lines, done, active: Boolean(ref) && !done }
}
