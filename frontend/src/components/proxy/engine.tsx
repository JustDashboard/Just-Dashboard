"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import {
  CheckCircle,
  Globe,
  ListOrdered,
  Logs,
  Play,
  RefreshClockwise,
  Stop,
} from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { duration, plural } from "@/lib/format"
import type {
  EngineAction,
  Job,
  ProxyReloadResult,
  ProxyValidation,
  SystemdUnit,
  UpdatePackage,
  UpdateReport,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { Status, type DotTone } from "@/components/status-dot"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { engineProduct } from "@/components/proxy/marks"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { engineKind, engineUnit, type ProxyStatus } from "@/components/proxy/proxy-context"
import type { EngineControl } from "@/components/proxy/engine-control"
import {
  PARTICIPLE,
  bootState,
  engineRun,
  isMasked,
  refusalOf,
  stoppedLabel,
} from "@/components/proxy/engine-lifecycle"
import { warningCount } from "@/components/proxy/config-test"
import { loadProofText, provenLive } from "@/components/proxy/load-proof"

/**
 * The engine itself: whether it is running, and the three things done to it.
 *
 * Every other page in this section writes files the engine reads; none of
 * them said whether it was up, and a reload that failed because nginx was
 * not running at all reported "invalid PID" and left the operator to guess.
 * The service unit is read through the same route the Services page uses,
 * so the two never disagree about what "running" means.
 */
export function useEngineUnit(status: ProxyStatus | undefined) {
  const name = engineUnit(status)
  const poll = usePoll(
    async (signal) => {
      const r = await get<{ unit: SystemdUnit }>(`/systemd/${name}`, undefined, signal)
      // Read once per poll rather than during render, which has to stay
      // pure: the uptime shown is as of the last fetch, which is the truth.
      return { unit: r.unit, fetchedAt: Date.now() }
    },
    30_000,
    [name],
    { enabled: Boolean(name) },
  )
  // A read that failed leaves the last answer in the poll; drawn, it said
  // "running for 2h" about a unit nobody could read.
  const current = poll.error ? undefined : poll.data
  const unit = current?.unit
  // systemd answers `not-found` for a unit it has never seen rather than
  // failing, so a host that runs nginx from a container or a nohup has no
  // unit and gets no controls, instead of controls that cannot work.
  const known = Boolean(unit && unit.loadState === "loaded")
  return {
    name,
    unit: known ? unit : undefined,
    fetchedAt: current?.fetchedAt,
    /** Why the unit could not be read, when its last read failed. */
    error: poll.error,
    refresh: poll.refresh,
  }
}

/** The one-line reading beside the engine's name. */
export function EngineStatus({
  unit,
  fetchedAt,
  pending,
}: {
  unit: SystemdUnit | undefined
  /** When the unit was last read, so the uptime is computed without a clock read during render. */
  fetchedAt?: number
  /** A start, restart or stop under way, said as it happens until the unit is read again. */
  pending?: string
}) {
  if (pending) return <Status state="activating" label={pending} />
  if (!unit) return null
  const running = engineRun(unit) === "running"
  const since = unit.activeSince && fetchedAt ? Math.max(0, fetchedAt / 1000 - unit.activeSince) : 0
  return (
    <Status
      state={unit.activeState}
      label={running ? `running${since > 60 ? ` for ${duration(since)}` : ""}` : stoppedLabel(unit)}
    />
  )
}

/**
 * What this host's proxy is, as the line the host Overview opens on: the
 * engine drawn as itself on the tile, its name and version, and after it the
 * facts a person opens the page to check — whether it is running, the
 * directory it reads, the ingress it serves through, whether certbot is here
 * and who renews — then what it serves. The verdict on its routes and the
 * service commands sit at the line's right end, where the Overview keeps its
 * verdict.
 *
 * It was a row of grey words. The engine is the one product this section is
 * about, and a page about nginx that never draws nginx opened on less than
 * the Docker overview's idle list says about a stopped container.
 */
export function EngineIdentity({
  status,
  unit,
  unitName,
  unitError,
  fetchedAt,
  statusAt,
  pending,
  onStartAtBoot,
  serviceBusy,
  certbotVersion,
  renewSource,
  inventory,
  verdict,
  actions,
}: {
  status: ProxyStatus
  unit: SystemdUnit | undefined
  /** The engine's service, named when its state could not be read. */
  unitName?: string
  /** Why the service's state could not be read; neither "running" nor "no service unit" is known then. */
  unitError?: Error
  fetchedAt?: number
  /** When `status` was read, so the ingress's uptime is computed without a clock read during render. */
  statusAt?: number
  /** A start, restart or stop under way, in its present participle. */
  pending?: string
  /** Sets the service to start at boot; absent for an account that may not. */
  onStartAtBoot?: () => void
  /** The service verb in flight, which Start at boot waits behind. */
  serviceBusy?: EngineAction
  certbotVersion?: string
  renewSource?: string | null
  /** What the engine serves — its sites, certificates, streams and open ports — after its own facts. */
  inventory?: React.ReactNode
  /** How its routes are answering, at the line's right end before the commands. */
  verdict?: React.ReactNode
  actions?: React.ReactNode
}) {
  const engine = status.nginx || status.caddy
  // The ingress's Caddyfile is the container's own, not the host path the
  // dashboard is configured with, so that path says nothing about it.
  const ingress = !status.nginx && Boolean(status.ingressContainer)
  const name = status.nginx ? "nginx" : status.caddy ? "Caddy" : "No reverse proxy"
  const version = status.nginx
    ? status.nginxVersion
    : status.caddy
      ? status.caddyVersion
      : undefined
  return (
    <HostIdentity
      mark={engineProduct(status)}
      fallback={Globe}
      title={
        <>
          {name}
          {version && (
            <>
              {" "}
              <span className="font-mono text-body font-normal text-muted-foreground">
                {version}
              </span>
            </>
          )}
        </>
      }
      facts={
        <>
          {pending ? (
            <EngineStatus unit={unit} pending={pending} />
          ) : unitError && unitName ? (
            <span className="font-medium text-warning" title={errorMessage(unitError)}>
              {`couldn't read ${unitName}`}
            </span>
          ) : unit ? (
            <>
              <EngineStatus unit={unit} fetchedAt={fetchedAt} />
              <EngineBoot unit={unit} onStartAtBoot={onStartAtBoot} busy={serviceBusy} />
            </>
          ) : engine && status.ingressContainer ? (
            <IngressUptime startedAt={status.ingressStartedAt} at={statusAt} />
          ) : engine ? (
            <span>no service unit</span>
          ) : (
            <span>nothing found on this host</span>
          )}
          {engine && !ingress && (
            <>
              <FactDot />
              <HostFact>
                <span className="font-mono">
                  {status.nginx ? status.nginxDir : status.caddyFile}
                </span>
              </HostFact>
            </>
          )}
          {status.ingressContainer && (
            <>
              <FactDot />
              <HostFact product="docker">
                ingress <span className="font-mono">{status.ingressContainer}</span>
              </HostFact>
            </>
          )}
          {status.ingressState === "provisionable" && (
            <>
              <FactDot />
              <HostFact product="docker">a Caddy ingress starts with the first deployment</HostFact>
            </>
          )}
          <FactDot />
          {/* certbot is Let's Encrypt's client, and the mark says what it
              issues rather than who wrote it. */}
          {status.certbot ? (
            <HostFact product="lets-encrypt">{certbotVersion ?? "certbot"}</HostFact>
          ) : (
            <span>no certbot</span>
          )}
          {renewSource !== undefined && status.certbot && (
            <>
              <FactDot />
              <span className={renewSource ? undefined : "font-medium text-warning"}>
                {renewSource ? `renews via ${renewSource}` : "renewal not scheduled"}
              </span>
            </>
          )}
          {inventory}
        </>
      }
      aside={
        (verdict || actions) && (
          <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
            {verdict}
            {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
          </div>
        )
      }
    />
  )
}

/**
 * The running ingress's reading: a container Docker found running, and for
 * how long by its own start time. It has no unit, so this stands where the
 * unit's state would.
 */
function IngressUptime({ startedAt, at }: { startedAt?: string; at?: number }) {
  const started = startedAt ? Date.parse(startedAt) : NaN
  const since = at && !Number.isNaN(started) ? Math.max(0, (at - started) / 1000) : 0
  return (
    <Status
      state="active"
      label={`running as a container${since > 60 ? `, up ${duration(since)}` : ""}`}
    />
  )
}

/**
 * Whether the engine comes back after a reboot. A proxy that is running now
 * and disabled at boot is an outage waiting for the next kernel update, and
 * nothing on the page said so; where `systemctl enable` would fix it, the
 * fix is beside the warning.
 */
function EngineBoot({
  unit,
  onStartAtBoot,
  busy,
}: {
  unit: SystemdUnit
  onStartAtBoot?: () => void
  busy?: EngineAction
}) {
  const boot = bootState(unit)
  if (!boot) return null
  return (
    <>
      <FactDot />
      <span className={boot.warn ? "font-medium text-warning" : undefined}>{boot.label}</span>
      {boot.canEnable && onStartAtBoot && (
        <Button
          size="xs"
          variant="outline"
          onClick={onStartAtBoot}
          pending={busy === "enable"}
          disabled={busy !== undefined}
        >
          Start at boot
        </Button>
      )}
    </>
  )
}

/**
 * Test, the command the service's state calls for, and the rest behind the
 * menu. A running engine's command is Reload; a stopped or failed one's is
 * Start, and Reload stands disabled beside it, because reloading an engine
 * that is not running reported "invalid PID" and left the reader to guess.
 * Restart and stop, which take every site offline for a moment, confirm
 * first.
 *
 * Test config opens the test's own panel. A reload runs the same test first,
 * and one it refuses, or passes with warnings, opens that panel on it: the
 * refusal was a toast of nginx's output cut to four hundred characters.
 *
 * Start and restart go through the proxy's own route, which picks the unit
 * itself and runs the config test before either: this host's nginx.service
 * tests before it starts, so a restart over a broken file used to stop nginx
 * and leave every site down. A refusal opens on the test's own lines.
 */
export function EngineActions({
  status,
  unitName,
  unit,
  control,
  testing,
  onTest,
  onReloadTested,
  onChanged,
}: {
  status: ProxyStatus
  unitName: string | undefined
  unit: SystemdUnit | undefined
  control: EngineControl
  /** Whether the config test is running. */
  testing: boolean
  onTest: () => void
  /** Shows a reload's own test: one that refused it, or passed it with warnings. */
  onReloadTested: (validation: ProxyValidation) => void
  onChanged: () => void
}) {
  const router = useRouter()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState<"reload" | "restart-container" | "">("")
  const kind = engineKind(status)
  const engine = control.engine

  const reload = async () => {
    setBusy("reload")
    try {
      const res = await post<ProxyReloadResult>("/proxy/reload", { kind })
      const warnings = warningCount(res.validation)
      const tested = warnings > 0 ? ` Its config test has ${plural(warnings, "warning")}.` : ""
      const proof = res.loadProof ? loadProofText(res.loadProof) : ""
      const description = `${proof}${tested}`.trim() || undefined
      const show =
        warnings > 0 ? { label: "Show", onClick: () => onReloadTested(res.validation) } : undefined
      if (provenLive(res.loadProof)) {
        notify.success(
          `${engine} reloaded`,
          description ? { description, action: show } : undefined,
        )
      } else {
        // The signal went out; the master was not seen loading what it read.
        notify.warning(`${engine} reload not confirmed`, { description, action: show })
      }
      onChanged()
    } catch (err) {
      const refusal = refusalOf(err)
      if (refusal?.validation) {
        onReloadTested(refusal.validation)
      } else if (err instanceof ApiError && err.code === "load_refused") {
        notify.error(`${engine} refused the reload`, err)
      } else {
        notify.error("Reload failed", err)
      }
      // A refused reload's test is the engine's last one now.
      onChanged()
    } finally {
      setBusy("")
    }
  }

  const ingressId = kind === "caddy-ingress" ? status.ingressId : undefined
  const container = status.ingressContainer
  const restartContainer = () =>
    confirm({
      title: "Restart container",
      confirmLabel: "Restart",
      description: (
        <p>
          <b>{container}</b> will be stopped and started again. Every site the ingress serves is
          offline until Caddy is back up.
        </p>
      ),
      action: async (phrase) => {
        setBusy("restart-container")
        try {
          await post(`/docker/containers/${ingressId}/restart`, undefined, { confirm: phrase })
          notify.success(`${container} restarted`)
          onChanged()
        } catch (err) {
          notify.error(`Could not restart ${container}`, err)
          throw err
        } finally {
          setBusy("")
        }
      },
    })

  // With no unit, or one that could not be read, the engine is whatever it
  // is and Reload says so if it cannot.
  const run = unitName && unit ? engineRun(unit) : undefined
  const lifecycle = control.pending !== undefined
  const verbs: Verb[] = []
  if (unitName && unit) {
    if (run !== "stopped") {
      verbs.push({
        key: "restart",
        label: `Restart ${engine}`,
        icon: RefreshClockwise,
        danger: true,
        disabled: lifecycle,
        run: () => control.ask("restart"),
      })
      verbs.push({
        key: "stop",
        label: `Stop ${engine}`,
        icon: Stop,
        danger: true,
        disabled: lifecycle,
        run: () => control.ask("stop"),
      })
    }
    verbs.push({
      key: "unit",
      label: "Service details",
      icon: ListOrdered,
      run: () => router.push(`/processes/services?unit=${encodeURIComponent(unitName)}`),
    })
  }
  // The ingress's lifecycle is its container's, so its restart and its log
  // are Docker's, through the Docker routes and their own gates.
  if (ingressId) {
    if (can("destructive")) {
      verbs.push({
        key: "restart-container",
        label: "Restart container",
        icon: RefreshClockwise,
        danger: true,
        disabled: busy !== "",
        run: restartContainer,
      })
    }
    verbs.push({
      key: "container-logs",
      label: "Container logs",
      icon: Logs,
      run: () => router.push(`/docker/containers/${encodeURIComponent(ingressId)}?tab=logs`),
    })
  }

  // Why Reload cannot run, when the service says it cannot.
  const reloadBlocked =
    run === "stopped"
      ? `${engine} is not running`
      : run === "changing" && unit
        ? `${engine} is ${stoppedLabel(unit)}`
        : undefined
  const startBlocked = unit && isMasked(unit) ? `${unitName} is masked` : undefined

  return (
    <>
      <Button size="sm" variant="outline" onClick={onTest} pending={testing}>
        <CheckCircle className="size-3.5" />
        Test config
      </Button>
      {reloadBlocked ? (
        <Blocked reason={reloadBlocked}>
          <Button size="sm" variant="outline" disabled>
            <RefreshClockwise className="size-3.5" />
            Reload
          </Button>
        </Blocked>
      ) : (
        <Button
          size="sm"
          onClick={reload}
          pending={busy === "reload"}
          disabled={busy !== "" || lifecycle}
        >
          <RefreshClockwise className="size-3.5" />
          Reload
        </Button>
      )}
      {run === "stopped" &&
        (startBlocked ? (
          <Blocked reason={startBlocked}>
            <Button size="sm" disabled>
              <Play className="size-3.5" />
              Start {engine}
            </Button>
          </Blocked>
        ) : (
          <Button
            size="sm"
            onClick={() => control.run("start")}
            pending={control.pending === "start"}
            disabled={lifecycle}
          >
            <Play className="size-3.5" />
            {control.pending === "start" ? PARTICIPLE.start : `Start ${engine}`}
          </Button>
        ))}
      {verbs.length > 0 && <VerbMenu verbs={verbs} label={`More ${engine} actions`} />}
      {dialog}
    </>
  )
}

/**
 * A disabled command with the reason it cannot run, on hover and on focus. A
 * disabled button takes neither, so the reason hangs on a wrapper the
 * keyboard can reach, which the tooltip describes while it is open.
 */
function Blocked({ reason, children }: { reason: string; children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0} className="inline-flex rounded-md focus-ring">
          {children}
        </span>
      </TooltipTrigger>
      <TooltipContent>{reason}</TooltipContent>
    </Tooltip>
  )
}

/**
 * The line under the engine's: which of the modules a proxy is usually asked
 * for this nginx has, and the package manager's part — installing the engine
 * and certbot where they are missing, and upgrading nginx where the host has
 * a newer one waiting. Each install runs as a job whose output streams below.
 *
 * The modules come from GET /proxy/modules; a host whose nginx cannot be read
 * that way, or a server without the route, answers an error and the line
 * says nothing about modules rather than guessing.
 */
export function EngineExtras({
  status,
  admin,
  onChanged,
}: {
  status: ProxyStatus
  /** May install packages (system.admin). */
  admin: boolean
  /** Reads the status and the engine again once an install lands. */
  onChanged: () => void
}) {
  const nginx = status.nginx
  const noEngine = !status.nginx && !status.caddy
  // certbot's nginx plugin is for nginx; Caddy issues its own certificates.
  const wantsCertbot = !status.certbot && !status.caddy
  const updates = usePoll(
    (signal) => get<UpdateReport>("/packages/updates", undefined, signal),
    0,
    [],
    { enabled: nginx || (admin && (noEngine || wantsCertbot)) },
  )
  const console_ = useJobConsole({
    onSuccess: () => {
      onChanged()
      updates.refresh()
    },
  })
  const { confirm, dialog } = useConfirm()
  const [starting, setStarting] = useState("")

  const report = updates.error ? undefined : updates.data
  const manager = report?.available ? report.manager : undefined
  const certbotPackages = manager ? CERTBOT_PACKAGES[manager] : undefined
  // Only apt's install brings an installed package up to its candidate;
  // the others' install leaves it where it is, so they get no Upgrade.
  const upgrade =
    nginx && manager === "apt"
      ? report?.packages.find((p) => NGINX_PACKAGES.includes(p.name))
      : undefined
  const running = console_.job?.status === "running"

  const install = async (key: string, packages: string[], what: string) => {
    setStarting(key)
    try {
      console_.attach(await post<Job>("/packages/install", { packages }))
    } catch (err) {
      notify.error(`Could not install ${what}`, err)
    } finally {
      setStarting("")
    }
  }

  const askUpgrade = (pkg: UpdatePackage) =>
    confirm({
      title: `Upgrade ${pkg.name}`,
      confirmLabel: "Upgrade",
      description: (
        <p>
          {manager} installs {pkg.name} {pkg.candidate} over {pkg.current}, keeping the
          configuration files as they are. The package restarts nginx, so every site is offline for
          a moment.
        </p>
      ),
      action: () => install("upgrade", [pkg.name], pkg.name),
    })

  const offersNginx = admin && noEngine && Boolean(manager)
  const offersCertbot = admin && wantsCertbot && Boolean(certbotPackages)
  if (!upgrade && !offersNginx && !offersCertbot && !console_.job) {
    return null
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-muted-foreground">
        {upgrade && (
          <span className="inline-flex flex-wrap items-center gap-2">
            <span>
              {upgrade.name}{" "}
              <span className="font-mono">
                {upgrade.current} → {upgrade.candidate}
              </span>
            </span>
            {admin && (
              <Button
                size="xs"
                variant="outline"
                onClick={() => askUpgrade(upgrade)}
                pending={starting === "upgrade"}
                disabled={starting !== "" || running}
              >
                Upgrade
              </Button>
            )}
          </span>
        )}
        {offersNginx && (
          <Button
            size="xs"
            variant="outline"
            title={`${manager} install nginx`}
            onClick={() => void install("nginx", ["nginx"], "nginx")}
            pending={starting === "nginx"}
            disabled={starting !== "" || running}
          >
            Install nginx
          </Button>
        )}
        {offersCertbot && certbotPackages && (
          <Button
            size="xs"
            variant="outline"
            title={`${manager} install ${certbotPackages.join(" ")}`}
            onClick={() => void install("certbot", certbotPackages, "certbot")}
            pending={starting === "certbot"}
            disabled={starting !== "" || running}
          >
            Install certbot + nginx plugin
          </Button>
        )}
      </div>
      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />
      {dialog}
    </div>
  )
}

/**
 * The modules a reverse proxy is asked for, as one fact in the engine's line:
 * "6 modules" when every one is there, the count missing in warning when one
 * is not, and each module's state a press away. They were a row of six status
 * dots of their own between the engine and the picture, which read as six
 * findings about a build that had nothing wrong with it.
 */
export function ModulesFact() {
  const modules = usePoll(
    (signal) => get<ModuleReport>("/proxy/modules/", undefined, signal),
    300_000,
  )
  const chips = modules.error ? [] : moduleChips(modules.data)
  if (chips.length === 0) return null
  const missing = chips.filter((c) => c.tone !== "running").length
  return (
    <>
      <FactDot />
      <Popover>
        <PopoverTrigger
          className={cn(
            "rounded-sm focus-ring transition-colors hover:text-foreground hover:underline",
            missing > 0 && "font-medium text-warning",
          )}
        >
          {missing > 0
            ? `${missing} of ${plural(chips.length, "module")} missing`
            : plural(chips.length, "module")}
        </PopoverTrigger>
        <PopoverContent align="start" className="w-64 p-3">
          <p className="eyebrow mb-2">nginx modules</p>
          <ul aria-label="nginx modules" className="space-y-1.5 text-xs">
            {chips.map((chip) => (
              <li
                key={chip.key}
                title={chip.detail}
                className="flex min-w-0 items-center justify-between gap-3"
              >
                <span className="truncate font-mono">{chip.label}</span>
                <Status tone={chip.tone} label={chip.state} />
              </li>
            ))}
          </ul>
        </PopoverContent>
      </Popover>
    </>
  )
}

/**
 * What GET /proxy/modules answers, as far as this line reads it: each module
 * as configure names it, and whether this nginx has it now.
 */
type ModuleReport = {
  modules: {
    name: string
    state: "static" | "loaded" | "not-loaded" | "not-installed" | "unknown"
    package?: string
  }[]
}

/** The modules a reverse proxy is usually asked for, as configure names them. */
const WATCHED_MODULES: { key: string; label: string }[] = [
  { key: "http_v2_module", label: "HTTP/2" },
  { key: "http_v3_module", label: "HTTP/3" },
  { key: "stream", label: "stream" },
  { key: "http_stub_status_module", label: "stub_status" },
  { key: "http_realip_module", label: "realip" },
  { key: "http_auth_request_module", label: "auth_request" },
]

type ModuleChip = { key: string; label: string; state: string; tone: DotTone; detail?: string }

/**
 * One reading per watched module. A module missing from the build list was
 * not built into this nginx at all, which no package on the host changes.
 */
function moduleChips(report: ModuleReport | undefined): ModuleChip[] {
  if (!report) return []
  const byName = new Map(report.modules.map((m) => [m.name, m]))
  return WATCHED_MODULES.map(({ key, label }) => {
    const m = byName.get(key)
    if (!m)
      return { key, label, state: "missing", tone: "stopped", detail: "Not built into this nginx" }
    switch (m.state) {
      case "static":
        return { key, label, state: "built in", tone: "running" }
      case "loaded":
        return { key, label, state: "loaded", tone: "running" }
      case "not-loaded":
        return {
          key,
          label,
          state: "not loaded",
          tone: "notice",
          detail: "Installed as a dynamic module that no load_module line loads",
        }
      case "not-installed":
        return m.package
          ? {
              key,
              label,
              state: "installable",
              tone: "notice",
              detail: `The ${m.package} package provides it`,
            }
          : {
              key,
              label,
              state: "missing",
              tone: "stopped",
              detail:
                "Built as a dynamic module whose file is absent, with no package that provides it",
            }
      default:
        return { key, label, state: "unknown", tone: "unknown" }
    }
  })
}

/** The package names nginx itself goes by: Debian splits it into flavours. */
const NGINX_PACKAGES = ["nginx", "nginx-core", "nginx-full", "nginx-light", "nginx-extras"]

/**
 * certbot and its nginx plugin, per package manager. zypper is absent because
 * openSUSE names them by Python version, which this page cannot know.
 */
const CERTBOT_PACKAGES: Record<string, string[]> = {
  apt: ["certbot", "python3-certbot-nginx"],
  dnf: ["certbot", "python3-certbot-nginx"],
  yum: ["certbot", "python3-certbot-nginx"],
  apk: ["certbot", "certbot-nginx"],
  pacman: ["certbot", "certbot-nginx"],
}
