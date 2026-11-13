"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { useSearchParams } from "next/navigation"
import Link from "next/link"
import { useSessionState } from "@/lib/view-state"
import {
  ArrowRight,
  CheckCircle,
  Crosshair,
  CrossCircle,
  Key,
  Route,
  SecureConnection,
  SignIn,
  TerminalWindow,
  UserMinus,
  Warning,
} from "@/components/icons"
import { plural } from "@/lib/format"
import { get, post } from "@/lib/api"
import { lensFor } from "@/lib/log-lenses"
import type { Job, NetworkConfirmationView, SSHDConfig, SSHSetting } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { FormSections, InfoTip } from "@/components/form"
import { PageContext } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { InitialsMark } from "@/components/account/user-avatar"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ReadingLines } from "@/components/reading-line"
import { Row, RowList } from "@/components/row-list"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { AUTH_LOG } from "@/components/security/host-logs"
import {
  HostLogSection,
  useAddressLineVerbs,
  useHostLog,
  useReadingPress,
} from "@/components/security/log-section"
import { useLensReadings } from "@/components/logs/lens-readings"
import {
  BASTION_PROFILE,
  JUMP_KEYS,
  JumpGenerator,
  JumpHostSection,
  jumpProfiled,
} from "@/components/security/bastion"
import { LensReadingLine, type ReadingLook } from "@/components/security/lens-reading-line"
import { SSHPicture } from "@/components/security/ssh-picture"
import { sshReading } from "@/components/security/ssh-reading"
import { boundaryVerdict } from "@/components/security/boundary"
import { BoundaryImpacts, useBoundaryCheck } from "@/components/security/boundary-view"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { VerbMenu, type Verb } from "@/components/verbs"

/**
 * The SSH server's own settings, which is where a single-server operator is
 * actually attacked.
 *
 * The firewall decides who may knock. sshd decides what happens next, and its
 * defaults are a compromise struck for compatibility across twenty years of
 * clients rather than for a machine on a public address. Three of these are
 * one line each in a file nobody opens, and the difference between them being
 * right and wrong is the difference between a bot wasting its time and a bot
 * getting in.
 *
 * The page opens on the server itself: sshd, where it listens, what it was
 * read from and what holds the listener, with how a login gets in as its
 * state and its verbs at the far end. Then those facts drawn as the doors a
 * login can take (`ssh-picture.tsx`), which follow the draft as it is edited,
 * and under the doors what the last day made of them: the auth log's four
 * counts as lines, each on its own hue with the day's shape, each a press
 * away from the lines it counts, so "passwords on" sits above "412 failed
 * attempts" rather than a page away from it.
 *
 * The settings are one centred column of sections, as a project's settings
 * are. Changes are staged and applied together through the product's one
 * save bar: Save opens a review that lists each old and new value and what
 * the change does to the way the dashboard is reached, sshd is tested with
 * its own parser before the daemon is asked to reload, and the file is put
 * back if the test fails. The one refusal that is not about syntax is the
 * important one: turning off password authentication on a host where nobody
 * has a key.
 *
 * Last is the auth log, read through its lens: who signed in and how, who
 * tried and failed, the addresses behind the failures, every sudo. An
 * attacker's address in it is blocked from the line it is on.
 */
export function SSHPanel() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { data, error, loading, refresh } = usePoll<SSHDConfig>(
    (signal) => get("/ssh/config", undefined, signal),
    0,
    [],
    { enabled: admin },
  )
  const [pending, setPending] = useState<Record<string, string>>({})
  const [only, setOnly] = useSessionState<"all" | "edited">("security.ssh.only", "all")
  const [busy, setBusy] = useState(false)
  const wanted = useSearchParams().get("setting")
  useEffect(() => {
    if (!wanted || !data?.available || !data.settings.some((s) => s.key === wanted)) return
    // A remembered filter must not hide the directive the operator came here to review.
    setOnly("all")
    const timer = window.setTimeout(() => {
      const control = document.getElementById(`ssh-${wanted}`)
      control?.closest("[data-slot=ssh-setting]")?.scrollIntoView({ block: "center" })
      control?.focus({ preventScroll: true })
    }, 0)
    return () => window.clearTimeout(timer)
  }, [wanted, data, setOnly])
  // The network journal's temporary apply covers sshd too: where the host can
  // run the independent watchdog, a change is kept only once this session
  // returns a fresh dashboard response, and restored by the host otherwise.
  const recovery = usePoll<NetworkConfirmationView>(
    (signal) => get("/network/changes/current", undefined, signal),
    0,
    [],
    { enabled: admin },
  )
  const recoverable = recovery.data?.available === true
  const console_ = useJobConsole()
  const jobs = usePoll<Job[]>((signal) => get<Job[]>("/jobs/", undefined, signal), 5000, [], {
    key: "/jobs",
    enabled: admin,
  })
  // Asked beside the config, not after it: the readings wait on it.
  const authLog = useHostLog(AUTH_LOG, admin)
  const readings = useLensReadings(authLog.data?.id ?? "", AUTH_LENS, {
    forcedLens: "auth",
    only: SSH_READINGS,
    enabled: Boolean(authLog.data) && Boolean(data?.available),
  })
  const lineVerbs = useAddressLineVerbs({ comment: "blocked from the auth log", blockOn: ATTACKS })
  const [ask, press] = useReadingPress()

  // The effective configuration is only right once sshd has reloaded.
  const jobStatus = console_.job?.status
  useEffect(() => {
    if (jobStatus === "succeeded") refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jobStatus])

  const changes = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(pending).filter(
          ([key, value]) =>
            data?.available &&
            data.settings.some((setting) => setting.key === key && setting.value !== value),
        ),
      ),
    [pending, data],
  )
  const dirty = Object.keys(changes).length > 0

  // The bar's Save waits on the review: it resolves when the change was sent,
  // or when the reader closed the review without applying.
  const [reviewing, setReviewing] = useState(false)
  const settle = useRef<((applied: boolean) => void) | undefined>(undefined)
  const answer = (applied: boolean) => {
    settle.current?.(applied)
    settle.current = undefined
  }
  const review = () =>
    new Promise<boolean>((resolve) => {
      settle.current = resolve
      setReviewing(true)
    })

  const header = <PageContext eyebrow="Security" title="SSH" />

  if (!admin) {
    return (
      <>
        {header}
        <EmptyState
          icon={TerminalWindow}
          title="SSH needs the admin capability"
          description="Its settings name the accounts that hold keys, which is a map of who can reach this machine, and its log records what was typed at the login prompt, sometimes a password."
        />
      </>
    )
  }
  if (loading && !data) {
    return (
      <>
        {header}
        <HostIdentity
          slot="ssh-identity"
          fallback={SecureConnection}
          title="sshd"
          state={<Status tone="unknown" label="reading" />}
          facts={<span>&nbsp;</span>}
        />
        <LoadingRows rows={8} hold />
      </>
    )
  }
  if (error && !data) {
    return (
      <>
        {header}
        <ErrorState error={error} onRetry={refresh} retryable />
      </>
    )
  }
  if (!data?.available) {
    return (
      <>
        {header}
        <EmptyState
          icon={TerminalWindow}
          title="No SSH server on this host"
          description={data?.error ?? "Neither sshd nor its configuration was found."}
        />
      </>
    )
  }

  const valueOf = (setting: SSHSetting) => pending[setting.key] ?? setting.value
  const changed = (setting: SSHSetting) =>
    pending[setting.key] !== undefined && pending[setting.key] !== setting.value
  const setting = (key: string) => data.settings.find((s) => s.key === key)
  const stage = (key: string, value: string) =>
    setPending((previous) => ({ ...previous, [key]: value }))

  // The jump host's directives are drawn in their own section, so the groups
  // hold the rest, unless the server did not send them, in which case there
  // is no section and nothing may go missing.
  const jumpRows = JUMP_KEYS.map(setting).filter((s) => s !== undefined)
  const jumpable = setting("allowtcpforwarding") !== undefined
  const general = jumpable ? data.settings.filter((s) => !JUMP_KEYS.includes(s.key)) : data.settings
  const edited = data.settings.filter(changed).length
  const noKeys = data.keyedAccounts.length === 0
  const keys = data.keyedAccounts.reduce((n, account) => n + account.keys, 0)
  const showing = only === "edited" && edited > 0 ? "edited" : "all"
  const visible = (s: SSHSetting) => showing === "all" || changed(s)
  const draft = (key: string) => {
    const s = setting(key)
    return s ? valueOf(s) : undefined
  }
  const figure = (id: string) => readings.tiles.find((t) => t.reading.id === id)?.figure?.value
  const verdict = sshReading(data)
  const profiled = jumpProfiled(draft)

  const verbs: Verb[] = [
    ...(jumpable
      ? [
          {
            key: "jump",
            label: "Use it as a jump host",
            icon: Route,
            disabled: profiled,
            run: () => setPending((previous) => ({ ...previous, ...BASTION_PROFILE })),
          },
        ]
      : []),
    ...(jobs.data ?? [])
      .filter((job) => job.kind.startsWith("ssh."))
      .slice(0, 3)
      .map((job) => ({
        key: `job-${job.id}`,
        label: job.title,
        icon: job.status === "failed" ? CrossCircle : CheckCircle,
        group: "Recent",
        run: () => console_.open(job.id),
      })),
  ]

  const row = (s: SSHSetting) => (
    <SettingRow
      key={s.key}
      setting={s}
      value={valueOf(s)}
      changed={changed(s)}
      note={
        s.key.toLowerCase() === "port" && data.socket?.unit
          ? `${data.socket.unit} owns this listener. Applying a port change also updates and restarts that socket.`
          : undefined
      }
      onChange={(value) => stage(s.key, value)}
    />
  )

  return (
    <SaveBarProvider>
      {header}

      <HostIdentity
        label="About sshd"
        slot="ssh-identity"
        fallback={SecureConnection}
        title={
          <span className="flex items-baseline gap-2">
            sshd
            <span className="numeric font-mono text-body font-normal text-muted-foreground">
              port <span className="text-[var(--tag-pink)]">{data.ports.join(", ") || "22"}</span>
            </span>
          </span>
        }
        state={
          verdict && (
            <Status
              tone={
                verdict.tone === "danger"
                  ? "danger"
                  : verdict.tone === "warning"
                    ? "warning"
                    : "running"
              }
              label={verdict.value}
            />
          )
        }
        facts={
          <>
            <span>
              {data.source.endsWith("-T") ? "as sshd reports it" : "read from sshd_config"}
            </span>
            {data.socket?.unit && (
              <>
                <FactDot />
                <span title="The systemd socket that holds the listener">
                  listener held by{" "}
                  <span className="font-mono text-foreground">{data.socket.unit}</span>
                </span>
              </>
            )}
            {data.managedFile && (
              <>
                <FactDot />
                <span className="inline-flex items-center gap-1">
                  saved to a drop-in
                  <InfoTip label="Where SSH changes are saved">{data.managedFile}</InfoTip>
                </span>
              </>
            )}
            {data.hasMatchBlocks && (
              <>
                <FactDot />
                <Tag
                  tone="warning"
                  icon={Warning}
                  title="Some values are overridden for particular users or addresses. What is shown here is the unconditional configuration; the conditional parts are not editable from this page."
                >
                  match blocks
                </Tag>
              </>
            )}
          </>
        }
        command={
          <Button size="sm" asChild>
            <Link href="/system-users">
              <Key aria-hidden />
              <span className="max-sm:sr-only">Add a key</span>
            </Link>
          </Button>
        }
        menu={verbs.length > 0 ? <VerbMenu verbs={verbs} label="SSH actions" /> : undefined}
      />

      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />

      {/* The four facts an attacker cares about (the port, passwords, root
          and who holds a key) as the doors a login can take, before the
          settings that open and shut them. Drawn as a path they are read as
          one answer, and they follow the draft. */}
      <Panel plain>
        <PanelHeader title="Ways in" />
        <PanelBody>
          <SSHPicture
            config={data}
            value={draft}
            traffic={{
              accepted: figure("accepted"),
              failed: figure("failed"),
              attackers: figure("attackers"),
            }}
          />
        </PanelBody>
      </Panel>

      {/* What the last day made of those doors, from the auth log: each a
          press away from the lines it counts. Drawn while the log is still
          being found, so the lines do not arrive after the page has settled. */}
      <ReadingLines>
        {readings.tiles.map((tile) => (
          <LensReadingLine
            key={tile.reading.id}
            tile={tile}
            window={readings.window}
            look={LOOKS[tile.reading.id] ?? LOOKS.attackers}
            onPick={() => press(tile.reading)}
          />
        ))}
      </ReadingLines>

      {/* The one banner left standing. It is not background: it is the
          reason the control below it will refuse, so it belongs above the
          control rather than in a footnote. */}
      {noKeys && (
        <Notice tone="warning" icon={Key} title="No account on this host has an SSH key">
          Password authentication cannot be turned off until one does: the server refuses a change
          that would leave nobody a way in.
        </Notice>
      )}

      {/* The room at the foot is the save bar's, so it never lies over the last row. */}
      <div
        id="ssh-settings"
        className="mx-auto w-full max-w-3xl min-w-0 scroll-mt-20 space-y-8 pb-10"
      >
        <ChipStrip aria-label="Which settings to show">
          <FilterChip selected={showing === "all"} onClick={() => setOnly("all")}>
            All <ChipCount>{data.settings.length}</ChipCount>
          </FilterChip>
          {edited > 0 && (
            <FilterChip selected={showing === "edited"} onClick={() => setOnly("edited")}>
              <span style={{ color: "var(--git-modified)" }}>Edited</span>
              <ChipCount>{edited}</ChipCount>
            </FilterChip>
          )}
        </ChipStrip>

        <SettingForm
          name="sshd settings"
          canEdit
          dirty={dirty}
          changes={Object.keys(changes).length}
          saving={busy}
          applies="immediately"
          onSave={review}
          onDiscard={() => setPending({})}
        >
          {SSH_GROUPS.map((group) => {
            const settings = general.filter((s) => sshGroup(s.key) === group.title && visible(s))
            if (settings.length === 0) return null
            return (
              <SettingSection
                key={group.title}
                title={group.title}
                state={<span className="numeric">{plural(settings.length, "directive")}</span>}
                status={settingStatus({ dirty: settings.some(changed) })}
              >
                <div className="divide-y divide-hairline">{settings.map(row)}</div>
              </SettingSection>
            )
          })}
          {jumpable && jumpRows.some(visible) && (
            <JumpHostSection
              value={draft}
              edited={jumpRows.some(changed)}
              profiled={profiled}
              onPreset={() => setPending((previous) => ({ ...previous, ...BASTION_PROFILE }))}
              rows={jumpRows.filter(visible).map(row)}
            />
          )}
        </SettingForm>

        <FormSections>
          {jumpable && (
            <SettingSection title="Connect through it">
              <JumpGenerator
                value={draft}
                saved={setting("allowtcpforwarding")?.value}
                port={data.ports[0] ?? "22"}
                user={data.keyedAccounts[0]?.user ?? "user"}
              />
            </SettingSection>
          )}
          <SettingSection
            title="Accounts with an authorized key"
            state={
              <span className="numeric">
                {noKeys
                  ? "Every login depends on a password"
                  : `${plural(data.keyedAccounts.length, "account")} · ${plural(keys, "key")}`}
              </span>
            }
            actions={
              <Link
                href="/system-users"
                className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
              >
                Users <ArrowRight className="size-3" />
              </Link>
            }
          >
            {noKeys ? (
              <EmptyNote>None.</EmptyNote>
            ) : (
              <RowList className="animate-rise">
                {data.keyedAccounts.map((account) => (
                  <Row
                    key={account.user}
                    leading={<InitialsMark name={account.user} />}
                    title={account.user}
                    trailing={
                      <span className="numeric text-hint text-muted-foreground">
                        {account.keys} {account.keys === 1 ? "key" : "keys"}
                      </span>
                    }
                    // No inset: the faces stand on the section's own edge.
                    className="px-0 py-2.5"
                  />
                ))}
              </RowList>
            )}
          </SettingSection>
        </FormSections>
      </div>

      <HostLogSection
        title="Auth log"
        log={authLog}
        storageKey="security.ssh.log"
        lineVerbs={lineVerbs}
        ask={ask}
        readings={readings}
      />

      {reviewing && (
        <ApplyReview
          config={data}
          changes={changes}
          recoverable={recoverable}
          onBusy={setBusy}
          onClose={() => {
            setReviewing(false)
            answer(false)
          }}
          onApplied={(job) => {
            console_.attach(job)
            setPending({})
            setReviewing(false)
            answer(true)
          }}
        />
      )}
    </SaveBarProvider>
  )
}

/**
 * The review Save opens: each staged directive with the value sshd runs and
 * the value it would, what the change does to the way the dashboard is
 * reached, and which recovery stands behind it. Where the host has the
 * watchdog the change is kept only once this session confirms a fresh
 * dashboard response, unless the reader turns that off here.
 *
 * Port, forwarding and AllowUsers decide who can still open a tunnel to the
 * dashboard, so the staged change is judged against the boundary while the
 * review is open: an effect is acknowledged by applying, a cut holds the
 * button, and the server decides again whatever this says.
 */
function ApplyReview({
  config,
  changes,
  recoverable,
  onBusy,
  onClose,
  onApplied,
}: {
  config: SSHDConfig
  changes: Record<string, string>
  recoverable: boolean
  onBusy: (busy: boolean) => void
  onClose: () => void
  onApplied: (job: Job) => void
}) {
  const [confirmAfter, setConfirmAfter] = useState(true)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const boundary = useBoundaryCheck({ kind: "ssh", settings: changes })
  const crossing = boundaryVerdict(boundary.impacts ?? [])
  const pendingApply = recoverable && confirmAfter
  const keys = Object.keys(changes)

  const apply = async () => {
    setBusy(true)
    onBusy(true)
    setRefusal(undefined)
    try {
      // The plan, including every lockout guard, is checked before this
      // returns, so a refusal is this dialog's answer. What comes back is a
      // job for the write, the sshd -t and the reload, which is the part
      // worth watching: this is the one operation where "it said it worked"
      // is not the same as knowing the daemon came back.
      const job = await post<Job>(
        "/ssh/config",
        {
          settings: changes,
          ...(crossing === "acknowledge" && { acknowledgeBoundary: true }),
        },
        { networkApply: pendingApply ? "pending" : undefined },
      )
      onApplied(job)
    } catch (err) {
      setRefusal(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
      onBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title="Apply SSH changes"
      description="What changes, what it does to the way the dashboard is reached, and how it is undone."
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={() => void apply()}
            pending={busy}
            disabled={busy || boundary.checking || crossing === "refused"}
          >
            Test and apply
          </Button>
        </>
      }
    >
      <div className="space-y-4 text-body leading-relaxed">
        <ul aria-label="Staged changes" className="space-y-1">
          {keys.map((key) => {
            const s = config.settings.find((candidate) => candidate.key === key)
            return (
              <li key={key} className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                <span className="font-medium">{s?.label ?? key}</span>
                <span className="font-mono text-hint text-muted-foreground">
                  {s?.value || "unset"} → {changes[key] || "unset"}
                </span>
              </li>
            )
          })}
        </ul>
        <p>
          Written to <code className="font-mono">{config.managedFile}</code>, tested with
          sshd&rsquo;s own parser and put back if the test fails. Existing sessions are not
          disconnected by a reload.
        </p>
        <BoundaryImpacts impacts={boundary.impacts} />
        {recoverable && (
          <label className="flex min-h-9 items-center justify-between gap-4 border-y border-hairline py-2 font-medium">
            Restore unless confirmed within 90 seconds
            <Switch
              checked={confirmAfter}
              onCheckedChange={setConfirmAfter}
              aria-label="Restore unless confirmed after reconnecting"
            />
          </label>
        )}
        {pendingApply ? (
          <p>
            The previous files are kept by the host. Unless you verify a new dashboard response and
            confirm within 90 seconds, the host restores them and reloads sshd, even if this
            dashboard can no longer be reached.
          </p>
        ) : (
          <p className="text-destructive">
            Keep this session open and confirm you can still log in from a second terminal before
            closing it.
          </p>
        )}
        {refusal && (
          <p role="alert" className="animate-rise text-destructive">
            {refusal}
          </p>
        )}
      </div>
    </Modal>
  )
}

const AUTH_LENS = lensFor("auth")

/**
 * The auth lens's readings that are about SSH: who got in, who tried, under
 * which names, from how many places. Its sudo failures are a reading of the
 * host's accounts rather than of sshd, and are one quick view away in the log.
 */
const SSH_READINGS = ["accepted", "failed", "invalid", "attackers"]

/** A hue per measure: a login is not a failure, and neither is drawn grey. */
const LOOKS: Record<string, ReadingLook> = {
  accepted: { icon: SignIn, hue: "var(--tag-green)" },
  failed: { icon: Key, hue: "var(--tag-amber)" },
  invalid: { icon: UserMinus, hue: "var(--tag-red)" },
  attackers: { icon: Crosshair, hue: "var(--tag-violet)" },
}

/**
 * The lines whose address a deny answers: a wrong password, an account that
 * does not exist, a connection that ran out of tries or gave up before
 * authenticating. A login is not one of them: the address a deploy key or
 * the operator signs in from is the last one to refuse.
 */
const ATTACKS = [
  "ssh_failed",
  "ssh_invalid_user",
  "ssh_max_attempts",
  "ssh_preauth_closed",
  "ssh_scan",
]

const SSH_GROUPS = [
  {
    title: "Authentication",
    keys: [
      "passwordauthentication",
      "pubkeyauthentication",
      "permitrootlogin",
      "kbdinteractiveauthentication",
      "challengeresponseauthentication",
      "permitemptypasswords",
      "usepam",
    ],
  },
  {
    title: "Access",
    keys: ["port", "listenaddress", "allowusers", "allowgroups", "denyusers", "denygroups"],
  },
  {
    title: "Session limits",
    keys: [
      "maxauthtries",
      "logingracetime",
      "clientaliveinterval",
      "clientalivecountmax",
      "maxstartups",
    ],
  },
  { title: "Other directives", keys: [] },
]

function sshGroup(key: string) {
  return (
    SSH_GROUPS.find((group) => group.keys.includes(key.toLowerCase()))?.title ?? "Other directives"
  )
}

/** The width a row's control and the line under it share. */
const CONTROL = "w-44 sm:w-52"

/** How a choice is drawn: a switch for yes or no, segments for three short words, else a list. */
function choiceControl(setting: SSHSetting): "switch" | "segments" | "select" {
  const options = setting.options ?? []
  if (options.length === 2 && options.includes("yes") && options.includes("no")) return "switch"
  if (options.length <= 3 && options.every((option) => option.length <= 6)) return "segments"
  return "select"
}

/**
 * One directive as a full-width row on a hairline: its name with why behind
 * the ⓘ, its key, and at the far end its control over one line that says
 * what sshd still runs while the value is staged. The line is always there,
 * so a row is one height whether it is edited or not.
 */
function SettingRow({
  setting,
  value,
  changed,
  note,
  onChange,
}: {
  setting: SSHSetting
  value: string
  changed: boolean
  /** An aside that belongs to this directive alone: the socket that owns Port. */
  note?: string
  onChange: (value: string) => void
}) {
  const id = `ssh-${setting.key}`
  const control = setting.kind === "choice" ? choiceControl(setting) : setting.kind
  return (
    <div
      data-slot="ssh-setting"
      data-edited={changed || undefined}
      className="relative flex min-h-[4.25rem] min-w-0 items-center justify-between gap-4 py-2.5"
    >
      {/* A staged change is marked down the row's edge in git's modified
          hue, the colour §3 gives a change that is not applied yet. */}
      {changed && (
        <span
          aria-hidden
          className="absolute inset-y-2.5 -left-3 w-0.5 rounded-full bg-[var(--git-modified)]"
        />
      )}
      <div className="min-w-0 space-y-0.5">
        <div className="flex min-w-0 items-center gap-2">
          <label htmlFor={id} className="min-w-0 text-sm leading-snug font-medium">
            {setting.label}
          </label>
          <InfoTip label={`About ${setting.label}`}>
            {setting.detail}
            {note && ` ${note}`}
          </InfoTip>
        </div>
        <code className="block font-mono text-hint break-all text-muted-foreground">
          {setting.key}
        </code>
      </div>

      <div className={`flex shrink-0 flex-col items-end gap-1 ${CONTROL}`}>
        <div className="flex h-8 w-full items-center justify-end">
          {control === "list" ? (
            <Input
              id={id}
              value={value}
              placeholder="everyone"
              className="h-8 font-mono text-xs"
              aria-label={setting.label}
              onChange={(e) => onChange(e.target.value)}
            />
          ) : control === "switch" ? (
            <Switch
              id={id}
              aria-label={setting.label}
              checked={value === "yes"}
              onCheckedChange={(on) => onChange(on ? "yes" : "no")}
            />
          ) : control === "segments" ? (
            <Segments
              id={id}
              label={setting.label}
              value={value}
              options={(setting.options ?? []).map((option) => ({ value: option, label: option }))}
              onChange={onChange}
              fill
            />
          ) : control === "select" ? (
            <Select value={value} onValueChange={onChange}>
              <SelectTrigger id={id} size="sm" className="w-full" aria-label={setting.label}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {setting.options?.map((option) => (
                  <SelectItem key={option} value={option}>
                    {option}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <Input
              id={id}
              value={value}
              inputMode="numeric"
              className="h-8 font-mono"
              aria-label={setting.label}
              onChange={(e) => onChange(e.target.value)}
            />
          )}
        </div>
        {/* Always there, so a row is one height whatever it has to say. */}
        <p className="numeric h-4 max-w-full truncate text-hint leading-4 text-muted-foreground">
          {changed && (
            <span style={{ color: "var(--git-modified)" }}>
              sshd runs <span className="font-mono">{setting.value || "unset"}</span>
            </span>
          )}
        </p>
      </div>
    </div>
  )
}
