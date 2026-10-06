"use client"

import { useEffect, useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { ArrowRight, Key, SecureConnection, TerminalWindow, Warning } from "@/components/icons"
import { plural } from "@/lib/format"
import { get, post } from "@/lib/api"
import { lensFor } from "@/lib/log-lenses"
import type { Job, Posture, SecurityFinding, SSHDConfig, SSHSetting } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { FormSection, InfoTip } from "@/components/form"
import { PageContext } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { InitialsMark } from "@/components/account/user-avatar"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid } from "@/components/stat-tile"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { AreaFindings } from "@/components/security/posture-panel"
import { AUTH_LOG } from "@/components/security/host-logs"
import {
  HostLogSection,
  useAddressLineVerbs,
  useHostLog,
  useReadingPress,
} from "@/components/security/log-section"
import { ReadingTile, useLensReadings } from "@/components/logs/lens-readings"
import { Status, StatusDot } from "@/components/status-dot"
import { SSHPicture } from "@/components/security/ssh-picture"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

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
 * The page opens on the server itself — sshd, where it listens, what it was
 * read from and what holds the listener, in the identity line every page
 * that describes a thing opens on, with its verdict at the right end — then
 * on those three and the port drawn as the doors a login can take
 * (`ssh-picture.tsx`), which follow the draft as it is edited; the settings
 * follow as two columns of sections, and
 * changes are staged and applied together: sshd is tested
 * with its own parser before the daemon is asked to reload, and the file is
 * put back if the test fails. The one refusal that is not about syntax is the
 * important one — turning off password authentication on a host where nobody
 * has a key.
 *
 * Last is what those settings let happen: the auth log, read through its
 * lens — who signed in and how, who tried and failed, the addresses behind
 * the failures, every sudo — with the day's counts as the page's readings
 * under the doors, so "passwords on" sits above "412 failed attempts" rather
 * than a page away from it. An attacker's address in it is blocked from the
 * line it is on.
 */
export function SSHPanel({
  posture,
  onFix,
}: {
  posture: Posture | undefined
  onFix?: (finding: SecurityFinding) => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  const { data, error, loading, refresh } = usePoll<SSHDConfig>(
    (signal) => get("/ssh/config", undefined, signal),
    0,
    [],
    { enabled: admin },
  )
  const [pending, setPending] = useState<Record<string, string>>({})
  const [only, setOnly] = useSessionState<"all" | "attention" | "edited">(
    "security.ssh.only",
    "all",
  )
  const [busy, setBusy] = useState(false)
  const console_ = useJobConsole()
  // Asked beside the config, not after it: the grid's second row waits on it.
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

  const header = <PageContext eyebrow="Security" title="SSH" />

  if (!admin) {
    return (
      <>
        {header}
        <EmptyState
          icon={TerminalWindow}
          title="SSH needs the admin capability"
          description="Its settings name the accounts that hold keys, which is a map of who can reach this machine, and its log records what was typed at the login prompt — sometimes a password."
        />
      </>
    )
  }
  if (loading && !data) {
    return (
      <>
        {header}
        <LoadingPanel />
      </>
    )
  }
  if (error && !data) {
    return (
      <>
        {header}
        <ErrorState error={error} />
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
  const insecure = data.settings.filter((s) => !s.secure).length
  const changed = (setting: SSHSetting) =>
    pending[setting.key] !== undefined && pending[setting.key] !== setting.value
  const setting = (key: string) => data.settings.find((s) => s.key === key)

  const noKeys = data.keyedAccounts.length === 0
  const shown =
    only === "attention"
      ? data.settings.filter((s) => !s.secure || changed(s))
      : only === "edited"
        ? data.settings.filter(changed)
        : data.settings
  const draft = (key: string) => {
    const s = setting(key)
    return s ? valueOf(s) : undefined
  }
  const figure = (id: string) => readings.tiles.find((t) => t.reading.id === id)?.figure?.value

  const apply = () =>
    confirm({
      title: "Apply SSH changes",
      confirmLabel: "Test and apply",
      description: (
        <div className="space-y-2">
          <p>
            The new configuration is written to{" "}
            <code className="font-mono">{data.managedFile}</code>, tested with sshd&rsquo;s own
            parser and put back if the test fails. Existing sessions are not disconnected by a
            reload.
          </p>
          <p className="text-destructive">
            Keep this session open and confirm you can still log in from a second terminal before
            closing it.
          </p>
        </div>
      ),
      action: async (c) => {
        setBusy(true)
        try {
          // The plan — including every lockout guard — is checked before this
          // returns, so a refusal is this dialog's answer. What comes back is
          // a job for the write, the sshd -t and the reload, which is the part
          // worth watching: this is the one operation where "it said it
          // worked" is not the same as knowing the daemon came back.
          const job = await post<Job>("/ssh/config", { settings: changes }, { confirm: c })
          console_.attach(job)
          setPending({})
        } finally {
          setBusy(false)
        }
      },
    })

  return (
    <>
      {header}

      <HostIdentity
        fallback={SecureConnection}
        title={
          <>
            sshd{" "}
            <span className="numeric font-mono text-body font-normal text-muted-foreground">
              port <span className="text-[var(--tag-pink)]">{data.ports.join(", ") || "22"}</span>
            </span>
          </>
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
                <span>
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
        aside={
          <span className="flex flex-wrap items-center gap-3">
            <RecentJobs kinds={["ssh."]} onOpen={console_.open} />
            <Status
              verdict={insecure === 0 ? "ok" : "warning"}
              label={insecure === 0 ? "Hardened" : `${insecure} below recommendation`}
              className="text-body"
            />
          </span>
        }
      />

      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />

      {/* The four facts an attacker cares about — the port, passwords, root
          and who holds a key — as the doors a login can take, before the
          settings that open and shut them. They were four tiles; drawn as a
          path they are read as one answer, and they follow the draft. */}
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

      {/* What the last day made of those doors, from the auth log: the page's
          readings, each a press away from the lines it counts. Drawn while
          the log is still being found so the row does not arrive after the
          page has settled. Two-up on a phone. */}
      {readings.tiles.length > 0 && (
        <StatGrid columns={4} dense>
          {readings.tiles.map((tile) => (
            <ReadingTile
              key={tile.reading.id}
              tile={tile}
              window={readings.window}
              onPick={() => press(tile.reading)}
            />
          ))}
        </StatGrid>
      )}

      <AreaFindings posture={posture} area="ssh" onFix={onFix} />

      {/* The one banner left standing. It is not background: it is the
          reason the control below it will refuse, so it belongs above the
          control rather than in a footnote. */}
      {noKeys && (
        <Notice tone="warning" icon={Key} title="No account on this host has an SSH key">
          Password authentication cannot safely be turned off until one does — with no key anywhere,
          doing so would leave nobody a way in, and the server refuses the change for that reason.
          Add a key from the Users page first.
        </Notice>
      )}

      <Panel plain>
        <PanelHeader
          title="Settings"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {data.settings.length} directives · staged here, applied together
            </span>
          }
        />
        <PanelToolbar>
          <ChipStrip aria-label="Which settings to show">
            <FilterChip selected={only === "all"} onClick={() => setOnly("all")}>
              All <ChipCount>{data.settings.length}</ChipCount>
            </FilterChip>
            <FilterChip selected={only === "attention"} onClick={() => setOnly("attention")}>
              <StatusDot tone={insecure ? "warning" : "running"} />
              Below recommendation <ChipCount>{insecure}</ChipCount>
            </FilterChip>
            {dirty && (
              <FilterChip selected={only === "edited"} onClick={() => setOnly("edited")}>
                <span style={{ color: "var(--git-modified)" }}>Edited</span>
                <ChipCount>{Object.keys(changes).length}</ChipCount>
              </FilterChip>
            )}
          </ChipStrip>
        </PanelToolbar>
        <PanelBody flush>
          {/* Two columns from `xl`, as the Configuration page lays its
              settings out: a 48rem column of twelve directives left most of
              a wide screen to nothing, and two heads on a line read as two
              parts of one server. */}
          <div className="grid min-w-0 gap-x-12 gap-y-12 pt-6 xl:grid-cols-2">
            {SSH_GROUPS.map((group) => {
              const settings = shown.filter((setting) => sshGroup(setting.key) === group.title)
              if (settings.length === 0) return null
              const warnings = settings.filter((setting) => !setting.secure).length
              const edited = settings.some(changed)
              return (
                <FormSection
                  aside
                  key={group.title}
                  title={group.title}
                  className="max-w-none py-0 first:pt-0 last:pb-0"
                  actions={edited && <Tag style={{ color: "var(--git-modified)" }}>Edited</Tag>}
                  hint={
                    <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
                      <Status
                        verdict={warnings ? "warning" : "ok"}
                        label={warnings ? `${warnings} below recommendation` : "At recommendation"}
                      />
                      <span className="numeric">{plural(settings.length, "directive")}</span>
                    </span>
                  }
                >
                  <div className="divide-y divide-hairline">
                    {settings.map((s) => (
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
                        onChange={(value) =>
                          setPending((previous) => ({ ...previous, [s.key]: value }))
                        }
                      />
                    ))}
                  </div>
                </FormSection>
              )
            })}
          </div>
          {shown.length === 0 && (
            <EmptyNote>Every setting is at or above its recommendation.</EmptyNote>
          )}
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Accounts with an authorized key"
          actions={
            <Link
              href="/system-users"
              className="flex items-center gap-1 rounded-md text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
            >
              Users <ArrowRight className="size-3" />
            </Link>
          }
        />
        <PanelBody flush>
          {noKeys ? (
            <p className="py-3 text-body text-muted-foreground">
              None. Every login on this host currently depends on a password.
            </p>
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
                  className="py-2.5"
                />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>

      <HostLogSection
        title="Auth log"
        log={authLog}
        storageKey="security.ssh.log"
        lineVerbs={lineVerbs}
        ask={ask}
        readings={readings}
      />

      {/* The apply bar follows the reader, as the Configuration page's does:
          a change may be staged at the top of the settings and the log is a
          screen under them. It names what changed in git's modified hue. */}
      {dirty && (
        <div className="sticky bottom-0 z-20 -mx-5 mt-auto animate-rise border-t border-hairline bg-background/85 px-5 py-3 backdrop-blur md:-mx-8 md:px-8">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-body">
              <span className="font-medium">
                {plural(Object.keys(changes).length, "unsaved change")}
              </span>
              <span className="hidden min-w-0 truncate font-mono text-hint text-[var(--git-modified)] sm:inline">
                {Object.keys(changes)
                  .map((key) => setting(key)?.label ?? key)
                  .join(" · ")}
              </span>
              <span className="text-muted-foreground">— tested with sshd -t before it reloads</span>
            </p>
            <div className="flex gap-2">
              <Button size="sm" variant="ghost" onClick={() => setPending({})} disabled={busy}>
                Discard
              </Button>
              <Button size="sm" onClick={apply} disabled={busy}>
                Test and apply
              </Button>
            </div>
          </div>
        </div>
      )}

      {dialog}
    </>
  )
}

const AUTH_LENS = lensFor("auth")

/**
 * The auth lens's readings that are about SSH, for the second row: who got
 * in, who tried, under which names, from how many places. Its sudo failures
 * are a reading of the host's accounts rather than of sshd, and are one
 * quick view away in the log.
 */
const SSH_READINGS = ["accepted", "failed", "invalid", "attackers"]

/**
 * The lines whose address a deny answers: a wrong password, an account that
 * does not exist, a connection that ran out of tries or gave up before
 * authenticating. A login is not one of them — the address a deploy key or
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
      "maxsessions",
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

/** Keep the control in one column and its recommendation beside the setting it explains. */
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
  /** An aside that belongs to this directive alone — the socket that owns Port. */
  note?: string
  onChange: (value: string) => void
}) {
  const below = !setting.secure
  const segmented =
    setting.kind === "choice" && setting.options?.length === 2 && setting.options.includes(value)

  return (
    <div className="relative grid min-w-0 items-center gap-x-6 gap-y-3 py-5 first:pt-0 sm:grid-cols-[minmax(0,1fr)_12rem]">
      {/* A staged change is marked down the row's edge in git's modified
          hue, the colour §3 gives a change that is not applied yet. */}
      {changed && (
        <span
          aria-hidden
          className="absolute inset-y-5 -left-3 w-0.5 rounded-full bg-[var(--git-modified)]"
        />
      )}
      <div className="min-w-0 space-y-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <label htmlFor={`ssh-${setting.key}`} className="text-body font-medium">
            {setting.label}
          </label>
          <InfoTip label={`About ${setting.label}`}>
            {setting.detail}
            {note && ` ${note}`}
          </InfoTip>
          {changed ? (
            <Tag style={{ color: "var(--git-modified)" }}>edited</Tag>
          ) : (
            below && <Status verdict="warning" label="below recommendation" />
          )}
        </div>
        <code className="block font-mono text-hint text-muted-foreground">{setting.key}</code>
        {below && (
          <p className="max-w-md text-hint leading-relaxed text-warning">
            Recommended <span className="font-mono">{setting.recommended}</span>.
            {setting.risk && <span className="text-muted-foreground"> {setting.risk}</span>}
          </p>
        )}
      </div>

      <div className="min-w-0">
        {setting.kind === "list" ? (
          <Input
            id={`ssh-${setting.key}`}
            value={value}
            placeholder="deploy admin — empty allows everyone"
            className="font-mono text-xs"
            aria-label={setting.label}
            onChange={(e) => onChange(e.target.value)}
          />
        ) : segmented ? (
          <ToggleGroup
            type="single"
            value={value}
            onValueChange={(v) => v && onChange(v)}
            variant="outline"
            size="sm"
            className="w-full"
            aria-label={setting.label}
            id={`ssh-${setting.key}`}
          >
            {setting.options!.map((option) => (
              <ToggleGroupItem key={option} value={option} className="flex-1 text-xs capitalize">
                {option}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        ) : setting.kind === "choice" ? (
          <Select value={value} onValueChange={onChange}>
            <SelectTrigger id={`ssh-${setting.key}`} className="w-full" aria-label={setting.label}>
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
            id={`ssh-${setting.key}`}
            value={value}
            inputMode="numeric"
            aria-label={setting.label}
            onChange={(e) => onChange(e.target.value)}
          />
        )}
      </div>
    </div>
  )
}
