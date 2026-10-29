"use client"

import { useId, useRef, useState, type RefObject } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  ArrowRight,
  Archive,
  Bell,
  Box,
  Cpu,
  Database,
  Gauge,
  PaperAirplane,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshClockwise,
  ShieldOff,
  StopCircle,
  Trash,
  type Icon,
} from "@/components/icons"
import { del, get, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { NotificationChannel } from "@/lib/types"
import {
  HOST_ALERT_DEFAULTS,
  HOST_ALERT_KINDS,
  HOST_ALERT_WORDS,
  cadenceWords,
  describeHostAlert,
  hostAlertCounts,
  hostAlertDraft,
  hostAlertFields,
  readsMetrics,
  type HostAlertKind,
  type HostAlertRule,
  type HostAlerts,
  type HostAlertSubject,
} from "@/lib/host-alerts"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { ProductGlyph, ProductLogo, hasProductLogo } from "@/components/product-logo"
import { ChannelGlyph } from "@/components/deploy/vocabulary"
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
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  AllClear,
  ChannelTests,
  HistoryRow,
  PictureChannel,
  SubjectCard,
  isFiring,
  pictureTargets,
} from "@/components/alerts/alert-parts"

/**
 * The host's alerts: rules over the machine's own readings — its CPU and
 * memory held high, a filesystem filling, a container gone down, a backup job
 * failed — told to the deployments' notification channels once when a
 * subject starts firing and once when it comes back.
 *
 * It is the proxy's Alerts page over different readings, on purpose: the same
 * picture of what the rules watch wired through this server to each channel,
 * what is firing first because that is what a reader comes for, then the
 * rules as rows that open their form in a sheet, and beside them what was
 * told lately and a test per channel. The parts that do not know which
 * readings they show are `components/alerts/alert-parts.tsx`.
 */

const KIND_ICON: Record<HostAlertKind, Icon> = {
  cpu_high: Cpu,
  memory_high: Gauge,
  disk_filling: Database,
  container_down: StopCircle,
  backup_failed: Archive,
  image_vulnerable: ShieldOff,
}

/** What a rule watches, as the picture's left column draws it. */
type Area = {
  key: string
  title: string
  /** The product the area is drawn as, where it has one. */
  product?: string
  fallback: Icon
  kinds: HostAlertKind[]
}

const AREAS: Area[] = [
  { key: "cpu", title: "CPU", fallback: Cpu, kinds: ["cpu_high"] },
  { key: "memory", title: "Memory", fallback: Gauge, kinds: ["memory_high"] },
  { key: "filesystems", title: "Filesystems", fallback: Database, kinds: ["disk_filling"] },
  {
    key: "containers",
    title: "Containers",
    product: "docker",
    fallback: Box,
    kinds: ["container_down"],
  },
  { key: "backups", title: "Backups", fallback: Archive, kinds: ["backup_failed"] },
  { key: "deployments", title: "Deployments", fallback: ShieldOff, kinds: ["image_vulnerable"] },
]

function areaOf(kind: HostAlertKind) {
  return AREAS.find((area) => area.kinds.includes(kind)) ?? AREAS[0]
}

/**
 * A rule's tile: what it watches, drawn as that product where it is one —
 * Docker with a stopped glyph in the corner for a container gone down — and
 * as the kind's own glyph where it is the machine itself.
 */
function KindTile({
  kind,
  tone,
  size = "md",
  className,
}: {
  kind: HostAlertKind
  tone?: "danger" | "warning"
  size?: "sm" | "md"
  className?: string
}) {
  const product = areaOf(kind).product
  const Glyph = KIND_ICON[kind]
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

export function HostAlertsPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const alerts = usePoll((signal) => get<HostAlerts>("/system/alerts", undefined, signal), 30_000)
  const channels = usePoll(
    (signal) => get<NotificationChannel[]>("/deploy/notifications", undefined, signal),
    60_000,
  )
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<{ rule?: HostAlertRule; kind?: HostAlertKind }>()
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
  const firingOf = (rule: HostAlertRule) =>
    subjects.filter((s) => s.ruleId === rule.id && isFiring(s)).length
  const cadence = cadenceWords(data?.intervalSeconds ?? 60)
  const metrics = data?.metrics ?? true

  const edit = (next: { rule?: HostAlertRule; kind?: HostAlertKind }) => {
    setSession((n) => n + 1)
    setEditing(next)
  }

  const check = async () => {
    setChecking(true)
    try {
      await post<HostAlerts>("/system/alerts/evaluate", {})
      alerts.refresh()
    } catch (error) {
      notify.error("Could not check the rules", error)
    } finally {
      setChecking(false)
    }
  }

  const setMuted = async (subject: HostAlertSubject, on: boolean) => {
    setBusy(`mute:${subject.ruleId}:${subject.subject}`)
    try {
      await put("/system/alerts/mutes", {
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

  const testChannel = async (channel: NotificationChannel) => {
    setBusy(`test:${channel.id}`)
    try {
      await post("/system/alerts/test", { channelId: channel.id })
      notify.success(`Test sent to ${channel.name}`)
    } catch (error) {
      notify.error(`Could not send a test to ${channel.name}`, error)
    } finally {
      setBusy(undefined)
    }
  }

  const testRule = async (rule: HostAlertRule) => {
    try {
      const told = await post<{ delivered: number; failed: number }>(
        `/system/alerts/rules/${rule.id}/test`,
        {},
      )
      if (told.failed > 0)
        notify.error(`${told.failed} of the rule's channels did not take the test`)
      else notify.success(`Test sent to ${plural(told.delivered, "channel")}`)
      alerts.refresh()
    } catch (error) {
      notify.error("Could not send the test", error)
    }
  }

  const save = async (rule: HostAlertRule, enabled: boolean) => {
    try {
      await put(`/system/alerts/rules/${rule.id}`, {
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

  const subjectFor = (rule: HostAlertRule) => ({
    mark: <KindTile kind={rule.kind} />,
    name: HOST_ALERT_WORDS[rule.kind].label,
    facts: <span>{describeHostAlert(rule.kind, rule.params)}</span>,
  })

  const verbsFor = (rule: HostAlertRule): Verb[] => [
    { key: "edit", label: "Edit", icon: Pencil, run: () => edit({ rule }) },
    {
      key: "test",
      label: "Send test",
      icon: PaperAirplane,
      disabled: !rule.enabled,
      run: () => void testRule(rule),
    },
    rule.enabled
      ? {
          key: "pause",
          label: "Pause",
          icon: Pause,
          run: () =>
            confirm({
              title: `Pause ${HOST_ALERT_WORDS[rule.kind].label} alert`,
              confirmLabel: "Pause alert",
              subject: subjectFor(rule),
              description:
                "Nobody is told while it is paused, and what it was following starts over when it is resumed.",
              action: async () => {
                await put(`/system/alerts/rules/${rule.id}`, {
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
          title: `Remove ${HOST_ALERT_WORDS[rule.kind].label} alert`,
          confirmLabel: "Remove alert",
          subject: subjectFor(rule),
          description:
            "Nobody is told about it again. Its mutes go with it; what it told stays in the history.",
          action: async () => {
            await del(`/system/alerts/rules/${rule.id}`)
          },
          onDone: () => alerts.refresh(),
        }),
    },
  ]

  const closeForm = () => setEditing(undefined)
  const { firing, holding } = hostAlertCounts(active)
  const paused = rules.filter((rule) => !rule.enabled).length

  return (
    <Page>
      <PageContext
        eyebrow={
          <Link href="/metrics" className="rounded-sm focus-ring hover:underline">
            Metrics
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
                    <AllClear rules={rules.length} cadence={cadence} />
                  ) : (
                    <ul aria-label="Firing" className="animate-rise space-y-2">
                      {active.map((subject) => (
                        <SubjectCard
                          key={`${subject.ruleId}:${subject.subject}`}
                          subject={subject}
                          kindLabel={HOST_ALERT_WORDS[subject.kind]?.label ?? subject.kind}
                          tile={(tone) => <KindTile kind={subject.kind} tone={tone} />}
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
                        Nobody is told when the CPU or memory stays high, a disk fills, a container
                        goes down or a backup fails. A rule is checked {cadence} and tells your
                        channels once on the way over and once on the way back.
                      </p>
                      <ChoiceGrid columns={2} className="grid-cols-1 sm:grid-cols-2 lg:grid-cols-2">
                        {HOST_ALERT_KINDS.map((kind) => (
                          <KindCard
                            key={kind}
                            kind={kind}
                            onClick={admin ? () => edit({ kind }) : undefined}
                          />
                        ))}
                      </ChoiceGrid>
                    </div>
                  ) : (
                    <ChoiceList aria-label="Alert rules" className="animate-rise">
                      {rules.map((rule) => {
                        const fires = firingOf(rule)
                        const words = HOST_ALERT_WORDS[rule.kind]
                        return (
                          <ChoiceRow
                            key={rule.id}
                            leading={
                              <KindTile
                                kind={rule.kind}
                                tone={fires > 0 ? "danger" : undefined}
                                className={cn(!rule.enabled && "opacity-50 grayscale")}
                              />
                            }
                            title={words.label}
                            verb={`Edit ${words.label}`}
                            onSelect={admin ? () => edit({ rule }) : undefined}
                            disabled={!admin}
                            description={
                              <>
                                {describeHostAlert(rule.kind, rule.params)}
                                {" · "}
                                <ChannelNames ids={rule.channels} channels={channelList} />
                                {rule.enabled && rule.unjudged && (
                                  <span className="block text-warning">{rule.unjudged}</span>
                                )}
                              </>
                            }
                            trailing={
                              !rule.enabled ? (
                                <Status tone="stopped" label="Paused" />
                              ) : fires > 0 ? (
                                <Status tone="danger" label={`${fires} firing`} />
                              ) : rule.unjudged ? (
                                <Status tone="warning" label="Cannot judge" />
                              ) : (
                                <Status tone="running" label="Watching" />
                              )
                            }
                            actions={
                              admin && (
                                <VerbActions
                                  dim
                                  verbs={verbsFor(rule)}
                                  menuLabel={`Actions for ${words.label}, ${describeHostAlert(rule.kind, rule.params)}`}
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
                          kindLabel={HOST_ALERT_WORDS[subject.kind]?.label ?? subject.kind}
                          tile={(tone) => <KindTile kind={subject.kind} tone={tone} />}
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
                          firing={event.event === "host.alert.firing"}
                          kindLabel={HOST_ALERT_WORDS[event.kind]?.label ?? event.kind}
                          tile={
                            HOST_ALERT_WORDS[event.kind] ? (
                              <KindTile kind={event.kind} size="sm" />
                            ) : (
                              <ProductLogo size="sm" fallback={Bell} />
                            )
                          }
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
                  <ChannelTests
                    channels={channelList}
                    error={channels.error && !channels.data ? channels.error : undefined}
                    onRetry={channels.refresh}
                    admin={admin}
                    busy={busy}
                    onTest={(channel) => void testChannel(channel)}
                  />
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
            channels={channelList}
            metrics={metrics}
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

function KindCard({ kind, onClick }: { kind: HostAlertKind; onClick?: () => void }) {
  return (
    <ChoiceCard onClick={onClick} disabled={!onClick} className="min-h-0 flex-row gap-3 p-3">
      <KindTile kind={kind} size="sm" />
      <span className="min-w-0 space-y-0.5">
        <ChoiceCardTitle className="truncate">{HOST_ALERT_WORDS[kind].label}</ChoiceCardTitle>
        <ChoiceCardHint className="line-clamp-2">{HOST_ALERT_WORDS[kind].hint}</ChoiceCardHint>
      </span>
    </ChoiceCard>
  )
}

/**
 * What the rules watch, wired through this server to who is told — the proxy
 * page's picture, read left to right. A watched area is drawn as itself; its
 * wire is red while something there fires, amber while one holds, moving
 * where a rule watches it and dashed where none does, and pressing an
 * unwatched one opens a rule for it.
 */
function AlertsPicture({
  rules,
  subjects,
  channels,
  onAdd,
}: {
  rules: HostAlertRule[]
  subjects: HostAlertSubject[]
  channels: NotificationChannel[]
  onAdd?: (kind: HostAlertKind) => void
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
  const targets = pictureTargets(channels)
  const told = channels.filter(reaches).length
  const title = enabled.length === 0 ? "No rules on" : plural(enabled.length, "rule")
  const reach = channels.length === 0 ? "telling nobody yet" : `telling ${plural(told, "channel")}`

  const areas = AREAS.map((area, index) => (
    <PictureArea
      key={area.key}
      area={area}
      index={index}
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
  index,
  rules,
  subjects,
  containerRef,
  hubRef,
  compact,
  onAdd,
}: {
  area: Area
  index: number
  rules: HostAlertRule[]
  subjects: HostAlertSubject[]
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  compact: boolean
  onAdd?: (kind: HostAlertKind) => void
}) {
  const mark = useRef<HTMLDivElement>(null)
  const Glyph = area.fallback
  const { firing, holding } = hostAlertCounts(subjects)
  const watched = rules.length > 0
  const tone = firing > 0 ? "danger" : holding > 0 ? "warning" : undefined
  const size = compact ? "sm" : "md"
  const glyph = hasProductLogo(area.product) ? (
    <ProductGlyph id={area.product} />
  ) : (
    <Glyph aria-hidden />
  )
  const drawn = watched ? (
    <WireMark tone={tone ?? "logo"} size={size}>
      {glyph}
    </WireMark>
  ) : (
    <WireLink
      label={`Watch ${area.title.toLowerCase()}`}
      onClick={onAdd ? () => onAdd(area.kinds[0]) : undefined}
    >
      <WirePlaceholder size={size} product={area.product} fallback={Glyph} />
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
      delay={(index % 5) * 0.35}
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

/**
 * One rule as a form: what to watch, its line and minutes where the kind has
 * them, and who to tell. It brings its own footer, so the command stays beside
 * the rule it saves.
 */
function RuleForm({
  rule,
  kind: preset,
  channels,
  metrics,
  onDone,
  onCancel,
}: {
  rule?: HostAlertRule
  kind?: HostAlertKind
  channels: NotificationChannel[]
  metrics: boolean
  onDone: () => void
  onCancel: () => void
}) {
  const start = rule?.kind ?? preset ?? "cpu_high"
  const [kind, setKind] = useState<HostAlertKind>(start)
  const initial = { ...HOST_ALERT_DEFAULTS[start], ...rule?.params }
  const [threshold, setThreshold] = useState(String(initial.threshold ?? ""))
  const [minutes, setMinutes] = useState(String(initial.minutes ?? ""))
  const [selected, setSelected] = useState<number[]>(rule?.channels ?? [])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const formId = useId()
  const fields = hostAlertFields(kind)
  const draft = hostAlertDraft(kind, { threshold, minutes })

  const choose = (next: HostAlertKind) => {
    setKind(next)
    setError(undefined)
    const defaults = HOST_ALERT_DEFAULTS[next]
    if (defaults.threshold) setThreshold(String(defaults.threshold))
    if (defaults.minutes) setMinutes(String(defaults.minutes))
  }

  const save = async () => {
    if (draft.problem) {
      setError(draft.problem)
      return
    }
    setSaving(true)
    setError(undefined)
    const body = { kind, params: draft.params, channels: selected, enabled: rule?.enabled ?? true }
    try {
      if (rule) await put(`/system/alerts/rules/${rule.id}`, body)
      else await post("/system/alerts/rules", body)
      notify.success(rule ? "Alert updated" : "Alert added", {
        description: `${HOST_ALERT_WORDS[kind].label}: ${describeHostAlert(kind, body.params)}.`,
      })
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  const minutesField = fields.minutes && (
    <Field
      label={kind === "container_down" ? "Down for" : "Averaged over"}
      htmlFor="host-alert-minutes"
      hint={
        kind === "container_down"
          ? "From a minute to a day before it is told."
          : "The recorded samples it averages, from a minute to a day."
      }
    >
      <InputGroup>
        <InputGroupInput
          id="host-alert-minutes"
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
  )

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
            {HOST_ALERT_KINDS.map((option) => (
              <ChoiceCard
                key={option}
                selected={kind === option}
                onClick={() => choose(option)}
                className="min-h-0 flex-row gap-3 p-3"
              >
                <KindTile kind={option} size="sm" />
                <span className="min-w-0 space-y-0.5">
                  <ChoiceCardTitle className="truncate">
                    {HOST_ALERT_WORDS[option].label}
                  </ChoiceCardTitle>
                  <ChoiceCardHint className="line-clamp-2">
                    {HOST_ALERT_WORDS[option].hint}
                  </ChoiceCardHint>
                </span>
              </ChoiceCard>
            ))}
          </ChoiceGrid>
        </div>
      </Field>

      {fields.threshold ? (
        <FieldRow>
          <Field
            label="At or above"
            htmlFor="host-alert-threshold"
            hint={
              kind === "disk_filling"
                ? "Of a filesystem's space or inodes; it recovers five points under."
                : "It recovers once it averages five points under."
            }
          >
            <InputGroup>
              <InputGroupInput
                id="host-alert-threshold"
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
          {minutesField}
        </FieldRow>
      ) : (
        minutesField
      )}

      {kind === "backup_failed" && (
        <FormNote>
          A job a database alert already follows for failed backups is left to that rule, so one
          failure is told once.
        </FormNote>
      )}
      {kind === "image_vulnerable" && (
        <FormNote>
          Each environment&apos;s scan terms decide what counts: scanning off means nothing is told
          for it, and its severity floor is the line. Set them on the project&apos;s overview.
        </FormNote>
      )}
      {!metrics && readsMetrics(kind) && (
        <FormNote tone="warning">
          Metrics history is off on this host (JD_METRICS_RETENTION=0), so this rule has nothing to
          read until it is turned back on.
        </FormNote>
      )}
      {error && <FormNote tone="danger">{error}</FormNote>}

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
          {HOST_ALERT_WORDS[kind].label}: {describeHostAlert(kind, draft.params)}.
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
