"use client"

import { useId, useRef, useState, type RefObject } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  ArrowRight,
  Bell,
  CheckCircle,
  Clock,
  CrossCircle,
  Envelope,
  Globe,
  LockClosed,
  NetworkDevice,
  PaperAirplane,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshClockwise,
  Servers,
  ShieldOff,
  Slash,
  Trash,
  type Icon,
} from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type {
  NotificationChannel,
  NotificationChannelKind,
  ProxyAlertEvent,
  ProxyAlertKind,
  ProxyAlertParams,
  ProxyAlertRule,
  ProxyAlerts,
  ProxyAlertSubject,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import {
  ProductGlyph,
  ProductLogo,
  channelProduct,
  hasProductLogo,
} from "@/components/product-logo"
import { ChannelGlyph, channelMark } from "@/components/deploy/vocabulary"
import { ChannelNames } from "@/components/deploy/settings/traffic-alerts"
import { WireHost, WireLink, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { SidePanel, SidePanelFooter } from "@/components/side-panel"
import { EmptyNote, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, type Verb } from "@/components/verbs"
import { useProxy } from "@/components/proxy/proxy-context"
import { engineProduct } from "@/components/proxy/marks"

/**
 * The proxy's alerts: rules over the readings the overview shows, told to the
 * deployments' notification channels once when a subject starts firing and
 * once when it comes back.
 *
 * It was a sheet over the overview, grey rows under five headings, and the
 * operator asked for a page of its own drawn the way Notifications is. It
 * opens on the same kind of picture: what the rules watch — certificates as
 * Let's Encrypt, the engine as itself, upstreams, sites and watched endpoints
 * — wired into this server and out to each channel as its own service, each
 * wire in the colour of what it carries. Under it what is firing now, because
 * that is what a reader comes for, then the rules as cards that open their
 * form; beside them what was told lately and a test per channel. A rule is
 * edited in a sheet, so the page behind it stays as it was.
 */

type KindWords = {
  label: string
  hint: string
  icon: Icon
  describe: (params: ProxyAlertParams) => string
}

const EXPIRY_DAYS = [21, 7, 3, 1]

const KINDS: Record<ProxyAlertKind, KindWords> = {
  cert_expiring: {
    label: "Certificate expiring",
    hint: "Told once at each threshold a certificate crosses.",
    icon: Clock,
    describe: (p) => `at ${(p.days ?? EXPIRY_DAYS).join(", ")} days left`,
  },
  cert_expired: {
    label: "Certificate expired",
    hint: "A certificate on this host has run out.",
    icon: CrossCircle,
    describe: () => "once a certificate runs out",
  },
  renewal_failed: {
    label: "Renewal failed",
    hint: "Certbot's last renewal or reload hook failed.",
    icon: RefreshClockwise,
    describe: () => "after a failed certbot renewal or reload hook",
  },
  served_drift: {
    label: "Served certificate differs",
    hint: "An nginx site serves a different certificate from the one in its file.",
    icon: ShieldOff,
    describe: () => "when a site serves a different certificate",
  },
  engine_down: {
    label: "Engine not running",
    hint: "The engine's unit stopped or failed, on two checks in a row.",
    icon: Servers,
    describe: () => "stopped or failed on two checks in a row",
  },
  upstream_down: {
    label: "Upstream down",
    hint: "An address nginx forwards to stops answering.",
    icon: NetworkDevice,
    describe: (p) => `down for ${p.minutes ?? 5} min`,
  },
  watch_unreachable: {
    label: "Watched endpoint unreachable",
    hint: "No TLS handshake on two checks in a row.",
    icon: Globe,
    describe: () => "no TLS handshake on two checks in a row",
  },
  watch_untrusted: {
    label: "Watched endpoint untrusted",
    hint: "It serves a certificate the system does not trust.",
    icon: ShieldOff,
    describe: () => "serves a certificate that is not trusted",
  },
  watch_grade_below: {
    label: "Watched TLS grade below minimum",
    hint: "A full TLS scan is below your chosen grade on two checks in a row.",
    icon: Globe,
    describe: (p) => `below grade ${p.grade ?? "A"} on two checks in a row`,
  },
  site_errors: {
    label: "Site failing",
    hint: "A site's share of 5xx answers over a window.",
    icon: Slash,
    describe: (p) =>
      `more than ${p.threshold ?? 5}% answered 5xx over ${p.minutes ?? 15} min, from ${p.minRequests ?? 20} requests`,
  },
}

const KIND_ORDER = Object.keys(KINDS) as ProxyAlertKind[]

const DEFAULT_PARAMS: Record<ProxyAlertKind, ProxyAlertParams> = {
  cert_expiring: { days: EXPIRY_DAYS },
  cert_expired: {},
  renewal_failed: {},
  served_drift: {},
  engine_down: {},
  upstream_down: { minutes: 5 },
  watch_unreachable: {},
  watch_untrusted: {},
  watch_grade_below: { grade: "A" },
  site_errors: { threshold: 5, minutes: 15, minRequests: 20 },
}

/**
 * What a rule watches, as the picture's left column draws it: the things a
 * reader thinks in — a certificate, the engine, a site — rather than the ten
 * kinds of rule there are.
 */
type Area = {
  key: string
  title: string
  /** The product the area is drawn as, where it has one. */
  product?: (engine: string | undefined) => string | undefined
  fallback: Icon
  kinds: ProxyAlertKind[]
}

const AREAS: Area[] = [
  {
    key: "certificates",
    title: "Certificates",
    product: () => "lets-encrypt",
    fallback: LockClosed,
    kinds: ["cert_expiring", "cert_expired", "renewal_failed", "served_drift"],
  },
  {
    key: "engine",
    title: "The engine",
    product: (engine) => engine,
    fallback: Servers,
    kinds: ["engine_down"],
  },
  { key: "upstreams", title: "Upstreams", fallback: NetworkDevice, kinds: ["upstream_down"] },
  { key: "sites", title: "Sites", fallback: Globe, kinds: ["site_errors"] },
  {
    key: "watched",
    title: "Watched endpoints",
    fallback: ShieldOff,
    kinds: ["watch_unreachable", "watch_untrusted", "watch_grade_below"],
  },
]

/** The channel kinds the picture offers where no channel is set up yet. */
const CHANNEL_KINDS: { kind: NotificationChannelKind; title: string }[] = [
  { kind: "slack", title: "Slack" },
  { kind: "discord", title: "Discord" },
  { kind: "telegram", title: "Telegram" },
  { kind: "email", title: "E-mail" },
]

function areaOf(kind: ProxyAlertKind) {
  return AREAS.find((area) => area.kinds.includes(kind)) ?? AREAS[0]
}

/** The product a kind of rule is about: Let's Encrypt for a certificate, the engine for itself. */
function kindProduct(kind: ProxyAlertKind, engine: string | undefined) {
  return areaOf(kind).product?.(engine)
}

/**
 * A rule's tile: what it watches, drawn as that product where it is one, with
 * the kind's own glyph in the corner — Let's Encrypt with a clock is a
 * certificate running low, with a cross one that ran out.
 */
function KindTile({
  kind,
  engine,
  tone,
  size = "md",
  className,
}: {
  kind: ProxyAlertKind
  engine?: string
  tone?: "danger" | "warning"
  size?: "sm" | "md"
  className?: string
}) {
  const product = kindProduct(kind, engine)
  const Glyph = KINDS[kind].icon
  if (!hasProductLogo(product))
    return (
      <ProductLogo
        size={size}
        fallback={Glyph}
        className={cn(
          tone === "danger" && "border-rule-danger [&_svg]:text-destructive",
          tone === "warning" && "border-rule-warning [&_svg]:text-warning",
          className,
        )}
      />
    )
  return (
    <span className={cn("relative flex shrink-0", className)}>
      <ProductLogo
        id={product}
        size={size}
        className={cn(
          tone === "danger" && "border-rule-danger",
          tone === "warning" && "border-rule-warning",
        )}
      />
      <span
        aria-hidden
        className={cn(
          "absolute -right-1 -bottom-1 flex size-4.5 items-center justify-center rounded-md border bg-background",
          tone === "danger"
            ? "border-rule-danger text-destructive"
            : tone === "warning"
              ? "border-rule-warning text-warning"
              : "border-hairline text-muted-foreground",
        )}
      >
        <Glyph className="size-3" />
      </span>
    </span>
  )
}

function isFiring(subject: ProxyAlertSubject) {
  return subject.state === "firing" && !subject.muted
}

export function AlertsPage() {
  const { status } = useProxy()
  const engine = status ? engineProduct(status) : undefined
  const { can } = useAuth()
  const admin = can("system.admin")
  const alerts = usePoll((signal) => get<ProxyAlerts>("/proxy/alerts", undefined, signal), 30_000)
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    60_000,
  )
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<{ rule?: ProxyAlertRule; kind?: ProxyAlertKind }>()
  // A counter keyed into the form, so a New form never inherits a stale draft.
  const [session, setSession] = useState(0)
  const [checking, setChecking] = useState(false)
  const [busy, setBusy] = useState<string>()

  const data = alerts.data
  const channelList = channels.data ?? []
  const rules = data?.rules ?? []
  const ruleOf = (id: number) => rules.find((rule) => rule.id === id)
  const subjects = data?.subjects ?? []
  // Firing first, then those holding before they are told.
  const active = subjects
    .filter((s) => s.state !== "quiet")
    .sort((a, b) => Number(b.state === "firing") - Number(a.state === "firing"))
  const muted = subjects.filter((s) => s.state === "quiet" && s.muted)
  const firingOf = (rule: ProxyAlertRule) =>
    subjects.filter((s) => s.ruleId === rule.id && isFiring(s)).length

  const edit = (next: { rule?: ProxyAlertRule; kind?: ProxyAlertKind }) => {
    setSession((n) => n + 1)
    setEditing(next)
  }

  const check = async () => {
    setChecking(true)
    try {
      await post<ProxyAlerts>("/proxy/alerts/evaluate", {})
      alerts.refresh()
    } catch (error) {
      notify.error("Could not check the rules", error)
    } finally {
      setChecking(false)
    }
  }

  const setMuted = async (subject: ProxyAlertSubject, on: boolean) => {
    setBusy(`mute:${subject.ruleId}:${subject.subject}`)
    try {
      await put("/proxy/alerts/mutes", {
        ruleId: subject.ruleId,
        subject: subject.subject,
        muted: on,
      })
      alerts.refresh()
    } catch (error) {
      notify.error(on ? "Could not mute it" : "Could not unmute it", error)
    } finally {
      setBusy(undefined)
    }
  }

  const test = async (channel: NotificationChannel) => {
    setBusy(`test:${channel.id}`)
    try {
      await post("/proxy/alerts/test", { channelId: channel.id })
      notify.success(`Test sent to ${channel.name}`)
    } catch (error) {
      notify.error(`Could not send a test to ${channel.name}`, error)
    } finally {
      setBusy(undefined)
    }
  }

  const save = async (rule: ProxyAlertRule, enabled: boolean) => {
    try {
      await put(`/proxy/alerts/rules/${rule.id}`, {
        kind: rule.kind,
        params: rule.params,
        channels: rule.channels,
        enabled,
      })
      alerts.refresh()
    } catch (error) {
      notify.error("Could not change the rule", error)
    }
  }

  const subjectFor = (rule: ProxyAlertRule) => ({
    mark: <KindTile kind={rule.kind} engine={engine} />,
    name: KINDS[rule.kind].label,
    facts: <span>{KINDS[rule.kind].describe(rule.params)}</span>,
  })

  const verbsFor = (rule: ProxyAlertRule): Verb[] => [
    { key: "edit", label: "Edit", icon: Pencil, run: () => edit({ rule }) },
    rule.enabled
      ? {
          key: "pause",
          label: "Pause",
          icon: Pause,
          run: () =>
            confirm({
              title: `Pause ${KINDS[rule.kind].label} alert`,
              confirmLabel: "Pause alert",
              subject: subjectFor(rule),
              description:
                "Nobody is told while it is paused, and what it was following starts over when it is resumed.",
              action: async () => {
                await put(`/proxy/alerts/rules/${rule.id}`, {
                  kind: rule.kind,
                  params: rule.params,
                  channels: rule.channels,
                  enabled: false,
                })
              },
              onDone: () => alerts.refresh(),
            }),
        }
      : { key: "resume", label: "Resume", icon: Play, run: () => void save(rule, true) },
    {
      key: "remove",
      label: "Remove",
      icon: Trash,
      danger: true,
      run: () =>
        confirm({
          title: `Remove ${KINDS[rule.kind].label} alert`,
          confirmLabel: "Remove alert",
          subject: subjectFor(rule),
          description:
            "Nobody is told about it again. Its mutes go with it; what it told stays in the history.",
          action: async () => {
            await del(`/proxy/alerts/rules/${rule.id}`)
          },
          onDone: () => alerts.refresh(),
        }),
    },
  ]

  const closeForm = () => setEditing(undefined)
  const firing = active.filter(isFiring).length
  const holding = active.filter((s) => s.state === "pending").length
  const paused = rules.filter((rule) => !rule.enabled).length
  const every = Math.round((data?.intervalSeconds ?? 300) / 60)

  return (
    <Page>
      <PageContext
        eyebrow={
          <Link href="/proxy" className="rounded-sm focus-ring hover:underline">
            Proxy & TLS
          </Link>
        }
        title="Alerts"
      />

      {alerts.error && !data ? (
        <ErrorState error={alerts.error} onRetry={alerts.refresh} />
      ) : alerts.loading && !data ? (
        <LoadingPanel plain rows={4} />
      ) : (
        <>
          {/* On the page's own ground, as Notifications' picture is. */}
          <div className="relative animate-rise py-2 lg:py-6">
            <AlertsPicture
              rules={rules}
              subjects={subjects}
              channels={channelList}
              engine={engine}
              onAdd={admin ? (kind) => edit({ kind }) : undefined}
            />
          </div>

          <div className="grid min-w-0 items-start gap-8 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] 2xl:gap-x-10 [&>*]:min-w-0">
            <div className="min-w-0 space-y-8">
              <Panel plain aria-label="Firing">
                <PanelHeader
                  title="Firing"
                  actions={
                    <span className="flex flex-wrap items-center gap-3">
                      {data?.lastPass && (
                        <span className="numeric text-hint text-muted-foreground">
                          checked {relativeTime(data.lastPass)}
                        </span>
                      )}
                      {(firing > 0 || holding > 0) && (
                        <span className="numeric text-hint text-muted-foreground">
                          <span className={cn("font-medium", firing > 0 && "text-destructive")}>
                            {firing}
                          </span>{" "}
                          firing
                          {holding > 0 && (
                            <>
                              {" · "}
                              <span className="font-medium text-warning">{holding}</span> holding
                            </>
                          )}
                        </span>
                      )}
                      {admin && (
                        <Button size="sm" variant="outline" onClick={check} pending={checking}>
                          <RefreshClockwise className="size-3.5" />
                          Check now
                        </Button>
                      )}
                    </span>
                  }
                />
                <PanelBody>
                  {active.length === 0 ? (
                    <AllClear rules={rules.length} every={every} />
                  ) : (
                    <ul aria-label="Firing" className="animate-rise space-y-2">
                      {active.map((subject) => (
                        <SubjectCard
                          key={`${subject.ruleId}:${subject.subject}`}
                          subject={subject}
                          engine={engine}
                          admin={admin}
                          busy={busy === `mute:${subject.ruleId}:${subject.subject}`}
                          onMute={(on) => void setMuted(subject, on)}
                        />
                      ))}
                    </ul>
                  )}
                </PanelBody>
              </Panel>

              <Panel plain aria-label="Rules">
                <PanelHeader
                  title="Rules"
                  actions={
                    <span className="flex flex-wrap items-center gap-3">
                      {rules.length > 0 && (
                        <span className="numeric text-hint text-muted-foreground">
                          {plural(rules.length, "rule")}
                          {paused > 0 && ` · ${paused} paused`}
                        </span>
                      )}
                      {admin && (
                        <Button size="sm" onClick={() => edit({})}>
                          <Plus className="size-3.5" />
                          Add rule
                        </Button>
                      )}
                    </span>
                  }
                />
                <PanelBody>
                  {rules.length === 0 ? (
                    <div className="space-y-4">
                      <p className="max-w-prose text-body text-pretty text-muted-foreground">
                        Nobody is told when a certificate runs low, the engine stops or a site
                        starts failing. A rule is checked every {every} minutes and tells your
                        channels once on the way over and once on the way back.
                      </p>
                      <ChoiceGrid columns={2} className="grid-cols-1 sm:grid-cols-2 lg:grid-cols-2">
                        {KIND_ORDER.map((kind) => (
                          <KindCard
                            key={kind}
                            kind={kind}
                            engine={engine}
                            onClick={admin ? () => edit({ kind }) : undefined}
                          />
                        ))}
                      </ChoiceGrid>
                    </div>
                  ) : (
                    <ChoiceList aria-label="Alert rules" className="animate-rise">
                      {rules.map((rule) => {
                        const fires = firingOf(rule)
                        return (
                          <ChoiceRow
                            key={rule.id}
                            leading={
                              <KindTile
                                kind={rule.kind}
                                engine={engine}
                                tone={fires > 0 ? "danger" : undefined}
                                className={cn(!rule.enabled && "opacity-50 grayscale")}
                              />
                            }
                            title={KINDS[rule.kind].label}
                            verb={`Edit ${KINDS[rule.kind].label}`}
                            onSelect={admin ? () => edit({ rule }) : undefined}
                            disabled={!admin}
                            description={
                              <>
                                {KINDS[rule.kind].describe(rule.params)}
                                {" · "}
                                <ChannelNames ids={rule.channels} channels={channelList} />
                              </>
                            }
                            trailing={
                              !rule.enabled ? (
                                <Status tone="stopped" label="Paused" />
                              ) : fires > 0 ? (
                                <Status tone="danger" label={`${fires} firing`} />
                              ) : (
                                <Status tone="running" label="Watching" />
                              )
                            }
                            actions={
                              admin && (
                                <VerbActions
                                  dim
                                  verbs={verbsFor(rule)}
                                  menuLabel={`Actions for ${KINDS[rule.kind].label}, ${KINDS[rule.kind].describe(rule.params)}`}
                                />
                              )
                            }
                          />
                        )
                      })}
                    </ChoiceList>
                  )}
                </PanelBody>
              </Panel>

              {muted.length > 0 && (
                <Panel plain aria-label="Muted">
                  <PanelHeader
                    title="Muted"
                    actions={
                      <span className="numeric text-hint text-muted-foreground">
                        {muted.length} nobody is told about
                      </span>
                    }
                  />
                  <PanelBody>
                    <ul aria-label="Muted" className="space-y-2">
                      {muted.map((subject) => (
                        <SubjectCard
                          key={`${subject.ruleId}:${subject.subject}`}
                          subject={subject}
                          engine={engine}
                          admin={admin}
                          busy={busy === `mute:${subject.ruleId}:${subject.subject}`}
                          onMute={(on) => void setMuted(subject, on)}
                        />
                      ))}
                    </ul>
                  </PanelBody>
                </Panel>
              )}
            </div>

            <div className="min-w-0 space-y-8">
              <Panel plain aria-label="Recently told">
                <PanelHeader
                  title="Recently told"
                  actions={
                    (data?.history ?? []).length > 0 && (
                      <span className="numeric text-hint text-muted-foreground">
                        newest {Math.min((data?.history ?? []).length, 100)}
                      </span>
                    )
                  }
                />
                <PanelBody>
                  {(data?.history ?? []).length === 0 ? (
                    <EmptyNote className="px-0 text-left">Nothing has been told yet.</EmptyNote>
                  ) : (
                    <ul aria-label="Alert history" className="animate-rise">
                      {(data?.history ?? []).slice(0, 12).map((event) => (
                        <HistoryRow
                          key={event.id}
                          event={event}
                          engine={engine}
                          ruleGone={!ruleOf(event.ruleId)}
                        />
                      ))}
                    </ul>
                  )}
                </PanelBody>
              </Panel>

              <Panel plain aria-label="Channels">
                <PanelHeader
                  title="Channels"
                  actions={
                    <Link
                      href="/deploy/notifications"
                      className="flex items-center gap-1 text-hint font-medium text-muted-foreground hover:text-foreground"
                    >
                      Notifications <ArrowRight className="size-3" />
                    </Link>
                  }
                />
                <PanelBody>
                  {channels.error && !channels.data ? (
                    <ErrorState error={channels.error} onRetry={channels.refresh} />
                  ) : channelList.length === 0 ? (
                    <FormNote>
                      No notification channels yet, so a rule tells nobody.{" "}
                      <Link
                        href="/deploy/notifications"
                        className="text-foreground underline-offset-2 hover:underline"
                      >
                        Add a channel
                      </Link>
                    </FormNote>
                  ) : (
                    <ul aria-label="Notification channels" className="space-y-2">
                      {channelList.map((channel) => (
                        <li
                          key={channel.id}
                          className="flex min-w-0 items-center gap-3 rounded-xl border bg-choice-surface px-3 py-2.5"
                        >
                          <ProductLogo
                            id={channelMark(channel)}
                            size="sm"
                            fallback={Envelope}
                            className={cn(!channel.enabled && "opacity-50 grayscale")}
                          />
                          <span className="min-w-0 flex-1">
                            <span className="block truncate text-body font-medium">
                              {channel.name}
                            </span>
                            <span className="block truncate font-mono text-hint text-muted-foreground">
                              {channel.target || channel.url}
                            </span>
                          </span>
                          {!channel.enabled && <Status tone="stopped" label="Paused" />}
                          {admin && (
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={!channel.enabled}
                              pending={busy === `test:${channel.id}`}
                              onClick={() => void test(channel)}
                              aria-label={`Send a test alert to ${channel.name}`}
                            >
                              <PaperAirplane className="size-3.5" />
                              Send test
                            </Button>
                          )}
                        </li>
                      ))}
                    </ul>
                  )}
                </PanelBody>
              </Panel>
            </div>
          </div>
        </>
      )}

      <SidePanel
        open={Boolean(editing)}
        onOpenChange={(open) => !open && closeForm()}
        title={editing?.rule ? "Edit alert" : "Add alert"}
        description="What to watch, and who is told when it starts and when it stops."
        width="lg"
      >
        {editing && (
          <RuleForm
            key={session}
            rule={editing.rule}
            kind={editing.kind}
            engine={engine}
            channels={channelList}
            onDone={() => {
              closeForm()
              alerts.refresh()
            }}
            onCancel={closeForm}
          />
        )}
      </SidePanel>
      {dialog}
    </Page>
  )
}

/**
 * Nothing firing, said as the good news it is rather than a grey line — and,
 * with no rule on, as the plain fact that nothing is followed, which is not.
 */
function AllClear({ rules, every }: { rules: number; every: number }) {
  const good = rules > 0
  return (
    <div
      className={cn(
        "flex min-w-0 animate-rise items-center gap-3.5 rounded-xl border bg-choice-surface p-4",
        good && "border-rule-success",
      )}
    >
      <span
        className={cn(
          "flex size-10 shrink-0 rounded-lg border bg-background",
          good ? "border-rule-success" : "border-hairline",
        )}
      >
        <span
          className={cn(
            "flex size-full items-center justify-center rounded-[inherit]",
            good && "bg-wash-success",
          )}
        >
          {good ? (
            <CheckCircle aria-hidden className="size-5 text-success" />
          ) : (
            <Bell aria-hidden className="size-4 text-muted-foreground" />
          )}
        </span>
      </span>
      <div className="min-w-0 space-y-0.5">
        <p className="text-body font-medium">
          {good ? "Nothing is firing" : "No rules yet, so nothing is followed"}
        </p>
        <p className="text-body text-pretty text-muted-foreground">
          {good
            ? `${plural(rules, "rule")} checked every ${every} minutes; you are told once when one starts and once when it stops.`
            : "Add a rule below and it is checked from the next pass."}
        </p>
      </div>
    </div>
  )
}

function KindCard({
  kind,
  engine,
  onClick,
}: {
  kind: ProxyAlertKind
  engine?: string
  onClick?: () => void
}) {
  return (
    <ChoiceCard onClick={onClick} disabled={!onClick} className="min-h-0 flex-row gap-3 p-3">
      <KindTile kind={kind} engine={engine} size="sm" />
      <span className="min-w-0 space-y-0.5">
        <ChoiceCardTitle className="truncate">{KINDS[kind].label}</ChoiceCardTitle>
        <ChoiceCardHint className="line-clamp-2">{KINDS[kind].hint}</ChoiceCardHint>
      </span>
    </ChoiceCard>
  )
}

/**
 * One subject a rule follows that is firing, holding or muted: a card in the
 * colour of its state, what it is, its reading, and its mute.
 */
function SubjectCard({
  subject,
  engine,
  admin,
  busy,
  onMute,
}: {
  subject: ProxyAlertSubject
  engine?: string
  admin: boolean
  busy: boolean
  onMute: (muted: boolean) => void
}) {
  const tone = subject.muted
    ? undefined
    : subject.state === "firing"
      ? "danger"
      : subject.state === "pending"
        ? "warning"
        : undefined
  const status =
    subject.state === "firing" ? (
      <Status
        tone={subject.muted ? "stopped" : "danger"}
        label={`Firing${subject.since ? ` since ${relativeTime(subject.since)}` : ""}`}
      />
    ) : subject.state === "pending" ? (
      <Status tone="warning" label="Holding before it is told" />
    ) : (
      <Status tone="stopped" label="Quiet" />
    )
  return (
    <li
      className={cn(
        "flex min-w-0 items-center gap-3.5 rounded-xl border bg-choice-surface px-4 py-3",
        tone === "danger" && "border-rule-danger",
        tone === "warning" && "border-rule-warning",
      )}
    >
      <KindTile kind={subject.kind} engine={engine} tone={tone} />
      <div className="min-w-0 flex-1 space-y-0.5">
        <p className="min-w-0 text-body leading-snug">
          <span className="font-medium break-all">{subject.label || subject.subject}</span>{" "}
          <span className="text-muted-foreground">· {KINDS[subject.kind].label}</span>
        </p>
        {subject.detail && (
          <p className="text-hint text-pretty text-muted-foreground">{subject.detail}</p>
        )}
        <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-hint">
          {status}
          {subject.muted && <span className="text-muted-foreground">muted, nobody is told</span>}
        </p>
      </div>
      {admin && (
        <Button
          size="sm"
          variant="outline"
          pending={busy}
          onClick={() => onMute(!subject.muted)}
          aria-label={`${subject.muted ? "Unmute" : "Mute"} ${subject.label || subject.subject}`}
        >
          {subject.muted ? "Unmute" : "Mute"}
        </Button>
      )}
    </li>
  )
}

function HistoryRow({
  event,
  engine,
  ruleGone,
}: {
  event: ProxyAlertEvent
  engine?: string
  ruleGone: boolean
}) {
  const firing = event.event === "proxy.alert.firing"
  const outcome = event.muted
    ? "muted, nobody told"
    : event.delivered === 0 && event.failed === 0
      ? "no channel to tell"
      : `told ${plural(event.delivered, "channel")}${event.failed > 0 ? `, ${event.failed} failed` : ""}`
  return (
    <li className="flex min-w-0 items-start gap-3 border-b border-hairline py-3 first:pt-0 last:border-b-0 last:pb-0">
      {KINDS[event.kind] ? (
        <KindTile kind={event.kind} engine={engine} size="sm" />
      ) : (
        <ProductLogo size="sm" fallback={Bell} />
      )}
      <div className="min-w-0 flex-1 space-y-0.5">
        <p className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-body">
          <Status tone={firing ? "danger" : "running"} label={firing ? "Fired" : "Resolved"} />
          <span className="min-w-0 font-medium break-all">{event.label || event.subject}</span>
        </p>
        <p className="text-hint text-pretty text-muted-foreground">
          {KINDS[event.kind]?.label ?? event.kind}
          {ruleGone && " (rule removed)"} · {relativeTime(event.createdAt)} ·{" "}
          <span className={cn(event.failed > 0 && "text-destructive")}>{outcome}</span>
          {event.detail && ` · ${event.detail}`}
        </p>
      </div>
    </li>
  )
}

/**
 * What the rules watch, wired through this server to who is told — the
 * Notifications picture's shape, read left to right. A watched area is drawn
 * as its product; its wire is red while something there fires, amber while
 * one holds, moving where a rule watches it and dashed where none does, and
 * pressing an unwatched one opens a rule for it. A channel's wire moves while
 * an enabled rule reaches it; a paused one is still and grey.
 */
function AlertsPicture({
  rules,
  subjects,
  channels,
  engine,
  onAdd,
}: {
  rules: ProxyAlertRule[]
  subjects: ProxyAlertSubject[]
  channels: NotificationChannel[]
  engine?: string
  onAdd?: (kind: ProxyAlertKind) => void
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const router = useRouter()
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const enabled = rules.filter((rule) => rule.enabled)
  // A rule with no channels named tells every enabled one.
  const reaches = (channel: NotificationChannel) =>
    channel.enabled &&
    enabled.some((rule) => rule.channels.length === 0 || rule.channels.includes(channel.id))
  const missing = CHANNEL_KINDS.filter(
    (item) => !channels.some((channel) => channel.kind === item.kind),
  )
  const targets = [
    ...channels.map((channel) => ({ key: `channel-${channel.id}`, channel, kind: channel.kind })),
    ...(channels.length === 0
      ? missing.map((item) => ({ key: `add-${item.kind}`, channel: undefined, kind: item.kind }))
      : []),
  ]
  const told = channels.filter(reaches).length
  const title = enabled.length === 0 ? "No rules on" : plural(enabled.length, "rule")
  const reach = channels.length === 0 ? "telling nobody yet" : `telling ${plural(told, "channel")}`

  const areas = AREAS.map((area) => (
    <PictureArea
      key={area.key}
      area={area}
      engine={engine}
      rules={enabled.filter((rule) => area.kinds.includes(rule.kind))}
      subjects={subjects.filter((s) => area.kinds.includes(s.kind))}
      containerRef={container}
      hubRef={hub}
      compact={!wide}
      onAdd={onAdd}
    />
  ))
  const channelNodes = targets.map((target) => (
    <PictureChannel
      key={target.key}
      channel={target.channel}
      kind={target.kind}
      live={target.channel ? reaches(target.channel) : false}
      containerRef={container}
      hubRef={hub}
      compact={!wide}
      onAdd={() => router.push("/deploy/notifications")}
    />
  ))

  return (
    <div ref={container} className="relative mx-auto max-w-5xl">
      {wide ? (
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(3rem,0.4fr)_auto_minmax(4.5rem,0.5fr)_minmax(0,1.1fr)] items-center">
          <div
            role="group"
            aria-label="What the rules watch"
            className="flex flex-col items-end gap-4"
          >
            {areas}
          </div>
          <div aria-hidden />
          <WireNode
            nodeRef={hub}
            align="center"
            mark={<WireHost />}
            eyebrow="This server"
            title={title}
            hint={reach}
          />
          <div aria-hidden />
          <div role="group" aria-label="Who is told" className="flex flex-col gap-4">
            {channelNodes}
          </div>
        </div>
      ) : (
        <div className="flex flex-col items-center text-center">
          <p className="eyebrow">This server</p>
          <p className="text-body leading-snug font-medium">{title}</p>
          <p className="text-hint leading-snug text-muted-foreground">{reach}</p>
          <div
            role="group"
            aria-label="What the rules watch"
            className="mt-5 flex flex-wrap justify-center gap-4"
          >
            {areas}
          </div>
          <div ref={hub} className="relative z-10 mt-10 flex">
            <WireHost size="md" />
          </div>
          <div
            role="group"
            aria-label="Who is told"
            className="mt-10 flex flex-wrap justify-center gap-4"
          >
            {channelNodes}
          </div>
        </div>
      )}
    </div>
  )
}

function PictureArea({
  area,
  engine,
  rules,
  subjects,
  containerRef,
  hubRef,
  compact,
  onAdd,
}: {
  area: Area
  engine?: string
  rules: ProxyAlertRule[]
  subjects: ProxyAlertSubject[]
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  compact: boolean
  onAdd?: (kind: ProxyAlertKind) => void
}) {
  const mark = useRef<HTMLDivElement>(null)
  const product = area.product?.(engine)
  const Glyph = area.fallback
  const firing = subjects.filter(isFiring).length
  const holding = subjects.filter((s) => s.state === "pending" && !s.muted).length
  const watched = rules.length > 0
  const tone = firing > 0 ? "danger" : holding > 0 ? "warning" : undefined
  const size = compact ? "sm" : "md"
  const glyph = hasProductLogo(product) ? <ProductGlyph id={product} /> : <Glyph aria-hidden />
  const drawn = watched ? (
    <WireMark tone={tone ?? "logo"} size={size}>
      {glyph}
    </WireMark>
  ) : (
    <WireLink
      label={`Watch ${area.title.toLowerCase()}`}
      onClick={onAdd ? () => onAdd(area.kinds[0]) : undefined}
    >
      <WirePlaceholder size={size} product={product} fallback={Glyph} />
    </WireLink>
  )
  const words = !watched
    ? "not watched"
    : firing > 0
      ? `${firing} firing`
      : holding > 0
        ? `${holding} holding`
        : `${plural(rules.length, "rule")}, all quiet`
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={mark}
      toRef={hubRef}
      still={!watched || tone !== undefined}
      dashed={!watched}
      tone={tone ?? "default"}
      duration={2.4}
      delay={(AREAS.indexOf(area) % 5) * 0.35}
    />
  )
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={mark} className="relative z-10 flex">
          {drawn}
        </div>
        <span className="sr-only">{`${area.title}: ${words}`}</span>
      </div>
    )
  return (
    <div className="min-w-0">
      {beam}
      <WireNode
        nodeRef={mark}
        align="end"
        mark={drawn}
        title={watched ? area.title : <span className="text-muted-foreground">{area.title}</span>}
        hint={
          <span
            className={cn(
              tone === "danger" && "font-medium text-destructive",
              tone === "warning" && "font-medium text-warning",
            )}
          >
            {words}
          </span>
        }
      />
    </div>
  )
}

function PictureChannel({
  channel,
  kind,
  live,
  containerRef,
  hubRef,
  compact,
  onAdd,
}: {
  channel?: NotificationChannel
  kind: NotificationChannelKind
  live: boolean
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  compact: boolean
  onAdd: () => void
}) {
  const mark = useRef<HTMLDivElement>(null)
  const size = compact ? "sm" : "md"
  const title = CHANNEL_KINDS.find((item) => item.kind === kind)?.title ?? kind
  const drawn = channel ? (
    <WireMark tone="logo" size={size}>
      <ChannelGlyph channel={channel} />
    </WireMark>
  ) : (
    <WireLink label={`Add a ${title} channel`} onClick={onAdd}>
      <WirePlaceholder size={size} product={channelProduct(kind)} fallback={Envelope} />
    </WireLink>
  )
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={hubRef}
      toRef={mark}
      still={!live}
      dashed={!channel}
      duration={2.2}
      delay={channel ? (channel.id % 5) * 0.3 : 0}
    />
  )
  const hint = channel
    ? !channel.enabled
      ? "paused"
      : live
        ? (channel.target || channel.url) && `told · ${channel.target || channel.url}`
        : "no rule tells it"
    : "add on Notifications"
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={mark} className="relative z-10 flex">
          {drawn}
        </div>
        {channel && <span className="sr-only">{`${channel.name}: ${hint}`}</span>}
      </div>
    )
  return (
    <div className="min-w-0">
      {beam}
      <WireNode
        nodeRef={mark}
        mark={drawn}
        title={channel ? channel.name : <span className="text-muted-foreground">{title}</span>}
        hint={<span className="block truncate">{hint}</span>}
      />
    </div>
  )
}

/**
 * One rule as a form: what to watch, its line where the kind has one, and
 * who to tell. It brings its own footer, so the command stays beside the rule
 * it saves.
 */
function RuleForm({
  rule,
  kind: preset,
  engine,
  channels,
  onDone,
  onCancel,
}: {
  rule?: ProxyAlertRule
  kind?: ProxyAlertKind
  engine?: string
  channels: NotificationChannel[]
  onDone: () => void
  onCancel: () => void
}) {
  const start = rule?.kind ?? preset ?? "cert_expiring"
  const [kind, setKind] = useState<ProxyAlertKind>(start)
  const initial = { ...DEFAULT_PARAMS[start], ...rule?.params }
  const [days, setDays] = useState<string[]>((initial.days ?? EXPIRY_DAYS).map(String))
  const [minutes, setMinutes] = useState(String(initial.minutes ?? ""))
  const [threshold, setThreshold] = useState(String(initial.threshold ?? ""))
  const [minRequests, setMinRequests] = useState(String(initial.minRequests ?? ""))
  const [grade, setGrade] = useState(initial.grade ?? "A")
  const [selected, setSelected] = useState<number[]>(rule?.channels ?? [])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const formId = useId()

  const choose = (next: ProxyAlertKind) => {
    setKind(next)
    const defaults = DEFAULT_PARAMS[next]
    if (defaults.minutes) setMinutes(String(defaults.minutes))
    if (defaults.threshold) setThreshold(String(defaults.threshold))
    if (defaults.minRequests) setMinRequests(String(defaults.minRequests))
    if (defaults.grade) setGrade(defaults.grade)
  }

  const params = (): ProxyAlertParams => {
    switch (kind) {
      case "cert_expiring":
        return { days: days.map(Number).sort((a, b) => b - a) }
      case "upstream_down":
        return { minutes: Number(minutes) }
      case "watch_grade_below":
        return { grade }
      case "site_errors":
        return {
          threshold: Number(threshold),
          minutes: Number(minutes),
          minRequests: Number(minRequests),
        }
      default:
        return {}
    }
  }

  const save = async () => {
    setSaving(true)
    setError(undefined)
    const body = { kind, params: params(), channels: selected, enabled: rule?.enabled ?? true }
    try {
      if (rule) await put(`/proxy/alerts/rules/${rule.id}`, body)
      else await post("/proxy/alerts/rules", body)
      notify.success(rule ? "Alert updated" : "Alert added", {
        description: `${KINDS[kind].label}: ${KINDS[kind].describe(body.params)}.`,
      })
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form
      id={formId}
      className="flex flex-col gap-6"
      onSubmit={(event) => {
        event.preventDefault()
        void save()
      }}
    >
      <Field label="Watch for">
        <div role="group" aria-label="Alert kind">
          <ChoiceGrid columns={2} className="grid-cols-1 sm:grid-cols-2 lg:grid-cols-2">
            {KIND_ORDER.map((option) => (
              <ChoiceCard
                key={option}
                selected={kind === option}
                onClick={() => choose(option)}
                className="min-h-0 flex-row gap-3 p-3"
              >
                <KindTile kind={option} engine={engine} size="sm" />
                <span className="min-w-0 space-y-0.5">
                  <ChoiceCardTitle className="truncate">{KINDS[option].label}</ChoiceCardTitle>
                  <ChoiceCardHint className="line-clamp-2">{KINDS[option].hint}</ChoiceCardHint>
                </span>
              </ChoiceCard>
            ))}
          </ChoiceGrid>
        </div>
      </Field>

      {kind === "cert_expiring" && (
        <Field label="Tell at" hint="Days left; each is told once." error={error}>
          <ToggleGroup
            type="multiple"
            value={days}
            onValueChange={(next) => next.length > 0 && setDays(next)}
            variant="outline"
            size="sm"
            className="w-full"
            aria-label="Expiry thresholds"
          >
            {EXPIRY_DAYS.map((d) => (
              <ToggleGroupItem key={d} value={String(d)} className="numeric flex-1 text-hint">
                {d} {d === 1 ? "day" : "days"}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
      )}

      {kind === "upstream_down" && (
        <Field
          label="Down for"
          htmlFor="proxy-alert-minutes"
          hint="From 5 minutes to a day; checked every 5 minutes."
          error={error}
        >
          <InputGroup>
            <InputGroupInput
              id="proxy-alert-minutes"
              inputMode="numeric"
              value={minutes}
              onChange={(event) => setMinutes(event.target.value)}
              className="numeric"
            />
            <InputGroupAddon align="inline-end">
              <InputGroupText>min</InputGroupText>
            </InputGroupAddon>
          </InputGroup>
        </Field>
      )}

      {kind === "watch_grade_below" && (
        <Field label="Minimum grade" hint="Full scans run in batches while this rule is on.">
          <ToggleGroup
            type="single"
            value={grade}
            onValueChange={(next) => next && setGrade(next)}
            variant="outline"
            size="sm"
            className="w-full"
            aria-label="Minimum TLS grade"
          >
            {["A+", "A", "B", "C"].map((value) => (
              <ToggleGroupItem key={value} value={value} className="numeric flex-1 text-hint">
                {value}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
      )}

      {kind === "site_errors" && (
        <FieldRow>
          <Field
            label="5xx share above"
            htmlFor="proxy-alert-threshold"
            hint="Of the site's requests."
            error={error}
          >
            <InputGroup>
              <InputGroupInput
                id="proxy-alert-threshold"
                inputMode="decimal"
                value={threshold}
                onChange={(event) => setThreshold(event.target.value)}
                className="numeric"
              />
              <InputGroupAddon align="inline-end">
                <InputGroupText>%</InputGroupText>
              </InputGroupAddon>
            </InputGroup>
          </Field>
          <Field label="Over" htmlFor="proxy-alert-window" hint="At least 5 minutes.">
            <InputGroup>
              <InputGroupInput
                id="proxy-alert-window"
                inputMode="numeric"
                value={minutes}
                onChange={(event) => setMinutes(event.target.value)}
                className="numeric"
              />
              <InputGroupAddon align="inline-end">
                <InputGroupText>min</InputGroupText>
              </InputGroupAddon>
            </InputGroup>
          </Field>
          <Field label="From at least" htmlFor="proxy-alert-requests" hint="Fewer is not judged.">
            <InputGroup>
              <InputGroupInput
                id="proxy-alert-requests"
                inputMode="numeric"
                value={minRequests}
                onChange={(event) => setMinRequests(event.target.value)}
                className="numeric"
              />
              <InputGroupAddon align="inline-end">
                <InputGroupText>requests</InputGroupText>
              </InputGroupAddon>
            </InputGroup>
          </Field>
        </FieldRow>
      )}

      {kind !== "cert_expiring" && kind !== "upstream_down" && kind !== "site_errors" && error && (
        <FormNote tone="danger">{error}</FormNote>
      )}

      <Field label="Tell">
        {channels.length === 0 ? (
          <FormNote>
            No notification channels yet — the rule reaches every channel you add later.{" "}
            <Link
              href="/deploy/notifications"
              className="text-foreground underline-offset-2 hover:underline"
            >
              Add a channel
            </Link>
          </FormNote>
        ) : (
          <div className="space-y-1.5">
            <OptionList>
              {channels.map((channel) => (
                <OptionRow
                  key={channel.id}
                  title={
                    <span className="inline-flex items-center gap-2">
                      <ChannelGlyph channel={channel} />
                      {channel.name}
                    </span>
                  }
                  hint={`${channel.target || channel.url}${channel.enabled ? "" : " · paused"}`}
                  checked={selected.includes(channel.id)}
                  onCheckedChange={(on) =>
                    setSelected((prev) =>
                      on ? [...prev, channel.id] : prev.filter((id) => id !== channel.id),
                    )
                  }
                />
              ))}
            </OptionList>
            <FormNote>
              {selected.length === 0
                ? "None chosen: every enabled channel is told."
                : `${selected.length} chosen.`}
            </FormNote>
          </div>
        )}
      </Field>

      <SidePanelFooter>
        <p className="mr-auto min-w-0 basis-full text-hint text-muted-foreground sm:basis-auto">
          {KINDS[kind].label}: {KINDS[kind].describe(params())}.
        </p>
        <Button type="button" variant="outline" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
        <Button type="submit" form={formId} pending={saving}>
          {rule ? "Save alert" : "Add alert"}
        </Button>
      </SidePanelFooter>
    </form>
  )
}
