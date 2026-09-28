"use client"

import { useCallback, useEffect, useEffectEvent, useMemo, useRef, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import {
  ArrowRight,
  Code,
  FolderOpen,
  Globe,
  Plus,
  Trash,
  Warning,
  type Icon,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type {
  AuthFile,
  Certificate,
  CORSMethod,
  CSPDirective,
  CloudflareRanges,
  CloudflareRefresh,
  Container,
  DroppedLine,
  ErrorPageCode,
  Exposure,
  HeaderValue,
  Listener,
  RequestLimit,
  LocationMatch,
  PreflightCheck,
  PreflightLevel,
  PermissionRule,
  PoolMethod,
  PoolServer,
  ProxyCache,
  RetryCondition,
  SiteCORS,
  SiteCSP,
  SiteHeaders,
  SiteLimits,
  SiteLocation,
  SiteMaintenance,
  SitePageName,
  SitePool,
  SitePreflight,
  SitePreview,
  SiteRead,
  SiteResult,
  SiteSpec,
  StaticCache,
} from "@/lib/types"
import { plural, relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceCard, ChoiceGrid, ProductCard } from "@/components/choice-card"
import { ProductLogo } from "@/components/product-logo"
import { CodeEditor } from "@/components/code-editor"
import { DiffView } from "@/components/files/diff-view"
import {
  Disclosure,
  Field,
  FieldRow,
  FormNote,
  FormSection,
  OptionList,
  OptionRow,
} from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Group, Pane, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, Notice } from "@/components/state"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  deriveIdentity,
  fileNameProblem,
  fixedFor,
  FOLLOW_DOMAINS,
  type IdentityFixed,
} from "@/components/proxy/site-identity"
import { ConfigEditor } from "@/components/proxy/config-editor"
import {
  changeCount,
  changesDiff,
  draftFate,
  droppedCount,
  droppedDiff,
  movableLines,
  sameSpec,
  specFromServer,
  withMovedLines,
  type DraftBase,
} from "@/components/proxy/site-draft"
import { NEW_SITE_DRAFT } from "@/components/proxy/site-link"
import {
  applyPreset,
  BLANK,
  leavePreset,
  presetById,
  PRESETS,
} from "@/components/proxy/site-presets"
import { saveOutcome, saveRequest, sendableSpec } from "@/components/proxy/site-save"
import { SiteCertificate } from "@/components/proxy/site-certificate"
import {
  nothingListening,
  upstreamOptions,
  type UpstreamOption,
} from "@/components/proxy/upstream-options"
import { UpstreamPicker } from "@/components/proxy/upstream-picker"
import { PAGE_LABEL, PageEditor } from "@/components/proxy/page-editor"
import {
  MATCH_LABEL,
  renderedPath,
  renderedUpstream,
  siteRoutes,
  splitUpstream,
} from "@/components/proxy/site-routes"

/**
 * Putting a domain in front of a port, without writing nginx.
 *
 * This is the thing everyone actually does to a server — something is running
 * on 127.0.0.1:3000 and it needs to be app.example.com with a certificate —
 * and doing it by hand means knowing eight proxy_set_header lines by heart.
 * Getting one wrong produces a site that works until somebody logs in.
 *
 * The config is rendered on the *server* and shown live beside the form, for
 * the reason the Docker create form shows its docker run line: there is
 * exactly one implementation of what a spec means, the form is not a black
 * box, and the file it produces is ordinary nginx that can be committed and
 * edited by hand afterwards.
 *
 * The form is built from `components/form.tsx`: a `Field` is a label, a
 * control and one line; an `OptionRow` is a switch with its sentence. It had
 * its own Field, Toggle and Section before, which is how a form two clicks
 * from the databases' dialogs arrived at a different label size.
 */
export function SiteForm({
  open,
  editing,
  copyFrom,
  session,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  /** The site being edited, or null for a new one. */
  editing: string | null
  /** A site whose settings a new one starts from. */
  copyFrom?: string | null
  /** Bumped on every open, so a closed and reopened form never keeps a stale draft. */
  session?: number
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  // Keyed on the site so opening another never inherits the previous one's
  // buffer — saving that under the wrong name would be a real outage.
  return (
    <SiteFormBody
      key={`${editing ?? (copyFrom ? `copy:${copyFrom}` : "new")}:${session ?? 0}`}
      open={open}
      editing={editing}
      copyFrom={copyFrom ?? null}
      onOpenChange={onOpenChange}
      onSaved={onSaved}
    />
  )
}

/**
 * The footer's saves: an enabled or new site is saved, or saved and
 * reloaded; a disabled one is saved as it is, or enabled and reloaded.
 */
type SaveMode = "save" | "reload" | "keep" | "enable"
const SAVE_MODES: Record<SaveMode, { reload: boolean; enable?: boolean }> = {
  save: { reload: false },
  reload: { reload: true },
  keep: { reload: false },
  enable: { reload: true, enable: true },
}

/** The glyph for a preset with no product of its own: its kind's. */
const KIND_MARK: Record<SiteSpec["kind"], Icon> = {
  proxy: Globe,
  static: FolderOpen,
  redirect: ArrowRight,
}

function SiteFormBody({
  open,
  editing,
  copyFrom,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  editing: string | null
  copyFrom: string | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const source = editing ?? copyFrom
  // Kept for the tab while the form is open (the panel forgets it on close):
  // a site is a long form, and a look at a port or a certificate half-way
  // through it should not mean typing it again. An existing site's draft
  // outlives a trip away only while its file is still the version the draft
  // started from; the server's copy used to replace it on every open.
  const draft = source ? `proxy.site.form.${source}` : NEW_SITE_DRAFT
  const [spec, setSpec] = useSessionState<SiteSpec>(`${draft}.spec`, BLANK)
  const [domainText, setDomainText] = useSessionState(`${draft}.domains`, "")
  // The preset last picked, drawn as the chosen card; the fields it filled
  // stay the operator's to change.
  const [preset, setPreset] = useSessionState<string | null>(`${draft}.preset`, null)
  const presetNote = useRef<HTMLParagraphElement>(null)
  // Whether somebody chose the upstream — typed, picked, a preset's or a
  // link's. Until then it is BLANK's suggestion, and a blank form opened on
  // a warning that nothing listens behind an address nobody had given.
  const [upstreamSet, setUpstreamSet] = useSessionState(`${draft}.upstreamSet`, false)
  // The rendered file and where it goes, for the spec named here: a preview
  // of another name says nothing about this one's file.
  const [preview, setPreview] = useState<(SitePreview & { name: string }) | null>(null)
  const [previewError, setPreviewError] = useState("")
  // What nginx -t cannot say about the draft: files, upstreams and DNS.
  const [preflight, setPreflight] = useState<SitePreflight | null>(null)
  const [preflightError, setPreflightError] = useState("")
  // The blocking checks the operator allowed a save over, by id: a new
  // failure holds the save again, an unrelated edit does not.
  const [overridden, setOverridden] = useState<string | null>(null)
  const [managed, setManaged] = useSessionState(`${draft}.managed`, true)
  // Which of the name and certificate paths the operator has set, so that
  // typing the domains stops rewriting them.
  const [fixed, setFixed] = useSessionState<IdentityFixed>(`${draft}.fixed`, FOLLOW_DOMAINS)
  // The save in flight, so the button pressed is the one that spins.
  const [busy, setBusy] = useState<SaveMode | "anyway" | null>(null)
  const [loaded, setLoaded] = useState(source === null)
  // Whether nginx reads the site being edited, whether it is in conf.d, and
  // whether nginx serves a file of its own under its name instead, as it was
  // read back; each preview then says all three afresh.
  const [readFile, setReadFile] = useState<Pick<SiteRead, "enabled" | "confd" | "servedCopy">>({})
  // A save refused over a name another server block claims, with the
  // server's sentence saying which of the two nginx answers. Kept with the
  // spec it was about, so any edit puts the question away.
  const [conflict, setConflict] = useState<{
    message: string
    mode: SaveMode
    spec: SiteSpec
  } | null>(null)
  const conflictRef = useRef<HTMLDivElement>(null)
  // What the draft started from: the file's version and the spec the form
  // read from it, or the spec a new site opened with. Unsaved changes are
  // measured against it.
  const [base, setBase] = useSessionState<DraftBase | null>(`${draft}.base`, null)
  // The file as last read, for an edit: what the Changes tab compares with.
  const [disk, setDisk] = useState<SiteRead | null>(null)
  // Bumped to read the file again: after the raw editor saved it, after a
  // save refused because it changed, or when a preview found a newer one.
  const [reads, setReads] = useState(0)
  // A newer version of the file under a draft with edits in it — or the
  // file gone — waiting for the operator to say which of the two wins.
  const [stale, setStale] = useState<SiteRead | "gone" | null>(null)
  const staleRef = useRef<HTMLDivElement>(null)
  // A save was refused because the file changed: the question is asked
  // even of a draft with no edits, since Save is what was pressed.
  const refused = useRef(false)
  const [discarding, setDiscarding] = useState(false)
  const [rawOpen, setRawOpen] = useState(false)
  const customRef = useRef<HTMLTextAreaElement>(null)

  const set = useCallback(
    <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => {
      setSpec((s) => ({ ...s, [key]: value }))
    },
    [setSpec],
  )

  // The file as it is now becomes the draft, and what it is measured from.
  const take = (r: SiteRead) => {
    const fresh = specFromServer(r.spec)
    setSpec(fresh)
    setDomainText(r.spec.domains.join(" "))
    setFixed(fixedFor(r.spec, true))
    setBase({ digest: r.digest, spec: fresh })
    setStale(null)
  }
  // The draft goes on, now measured against — and saved over — the file as
  // it is now, or written as a new file where the old one went.
  const keepDraft = () => {
    if (stale === "gone") setBase((b) => (b ? { ...b, digest: "" } : b))
    else if (stale) setBase({ digest: stale.digest, spec: specFromServer(stale.spec) })
    setStale(null)
  }

  // Load an existing site back into the form — as itself, or as the start of
  // a new one with the name, domains and certificate paths cleared, since
  // those three are the things a duplicate exists to change. A draft already
  // under way is kept while the file is the version it started from, given
  // way to the file when nothing in it was changed, and otherwise the
  // operator is asked which of the two wins.
  const onRead = useEffectEvent((r: SiteRead) => {
    if (copyFrom && !editing) {
      if (!base) {
        const copied: SiteSpec = {
          ...specFromServer(r.spec),
          name: "",
          domains: [],
          certPath: undefined,
          keyPath: undefined,
          managedAcme: false,
        }
        setSpec(copied)
        setDomainText("")
        setManaged(true)
        setFixed(FOLLOW_DOMAINS)
        setBase({ digest: "", spec: copied })
      }
      setLoaded(true)
      return
    }
    setDisk(r)
    setManaged(r.managed)
    setReadFile({ enabled: r.enabled, confd: r.confd, servedCopy: r.servedCopy })
    const fate = draftFate(base, spec, r.digest)
    if (fate === "stale" || (fate === "take" && refused.current)) setStale(r)
    else if (fate === "take") take(r)
    refused.current = false
    setLoaded(true)
  })
  // The file went while a draft of it was kept: the draft can be saved as
  // the file again, or let go.
  const onReadFailed = useEffectEvent((err: unknown) => {
    if (editing && base && err instanceof ApiError && err.status === 404) {
      setStale("gone")
      setLoaded(true)
      return
    }
    notify.error("Could not load the site", err)
  })
  useEffect(() => {
    if (!open || !source) return
    const controller = new AbortController()
    get<SiteRead>(`/proxy/sites/${encodeURIComponent(source)}`, undefined, controller.signal)
      .then((r) => onRead(r))
      .catch((err) => !controller.signal.aborted && onReadFailed(err))
    return () => controller.abort()
  }, [open, source, reads])
  // A preview says which version of the file is on disk now; one other than
  // the version last read means it changed while the form was open, and it
  // is read again so the draft and the Changes tab go by what is there.
  const onPreviewed = useEffectEvent((r: SitePreview) => {
    if (editing && disk && r.digest && r.digest !== disk.digest) setReads((n) => n + 1)
  })

  // The live preview. Debounced, because it is a request per keystroke
  // otherwise and the answer only matters once typing stops.
  useEffect(() => {
    const controller = new AbortController()
    const ready = open && loaded && spec.domains.length > 0 && spec.name !== ""
    // Everything happens in the timeout, including clearing the preview. A
    // setState in the effect body itself is a cascading render, and the
    // not-ready case is the one that would fire on every keystroke.
    const timer = setTimeout(
      () => {
        if (!ready) {
          setPreview(null)
          setPreviewError("")
          return
        }
        post<SitePreview>(
          "/proxy/sites/preview",
          { spec: sendableSpec(spec) },
          { signal: controller.signal },
        )
          .then((r) => {
            setPreview({ ...r, name: spec.name })
            setPreviewError("")
            onPreviewed(r)
          })
          .catch((err) => {
            if (controller.signal.aborted) return
            setPreview(null)
            setPreviewError(String(err))
          })
      },
      ready ? 400 : 0,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [open, loaded, spec])

  // The preflight. Slower to debounce than the preview: it connects to the
  // upstreams and resolves every domain, which is not worth a request per
  // keystroke.
  useEffect(() => {
    const controller = new AbortController()
    const ready = open && loaded && spec.domains.length > 0
    const timer = setTimeout(
      () => {
        if (!ready) {
          setPreflight(null)
          setPreflightError("")
          return
        }
        post<SitePreflight>(
          "/proxy/sites/preflight",
          { spec: sendableSpec(spec) },
          { signal: controller.signal },
        )
          .then((r) => {
            setPreflight(r)
            setPreflightError("")
          })
          .catch((err) => {
            if (controller.signal.aborted) return
            setPreflight(null)
            setPreflightError(String(err))
          })
      },
      ready ? 800 : 0,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [open, loaded, spec])

  // What the password field can point at, and whether the certificate the
  // form names is one this dashboard has seen. Both polled only while the
  // form is open and the field is in play.
  const authFiles = usePoll<AuthFile[]>(
    (signal) => get("/proxy/auth-files/", undefined, signal),
    0,
    [],
    { enabled: open },
  )
  const certs = usePoll<Certificate[]>(
    (signal) => get("/certificates/", undefined, signal),
    0,
    [],
    { enabled: open && spec.tls },
  )
  // What is running for the upstream to point at, re-read while the form is
  // open: an app started half-way through filling it in should appear, and
  // the warning that nothing listens should go.
  const forwards = open && spec.kind === "proxy"
  const listeners = usePoll<Listener[]>((signal) => get("/ports", undefined, signal), 15_000, [], {
    enabled: forwards,
  })
  // Docker is optional: a host without it just has no containers to offer.
  const containers = usePoll<Container[]>(
    (signal) => get("/docker/containers/", { all: "false" }, signal),
    30_000,
    [],
    { enabled: forwards },
  )
  const options = useMemo(
    () => upstreamOptions(listeners.data, containers.data),
    [listeners.data, containers.data],
  )
  const picker = {
    options,
    listeners: listeners.data,
    containers: containers.data,
    loading: listeners.loading,
    failed: Boolean(listeners.error) && !listeners.data,
    onRetry: listeners.refresh,
  }

  const commitDomains = (text: string) => {
    setDomainText(text)
    const domains = text.split(/[\s,]+/).filter(Boolean)
    setSpec((s) => ({ ...s, domains, ...deriveIdentity(domains, s, fixed) }))
  }

  // A certificate path typed by hand stays; emptied, it follows the domains
  // again.
  const setCertificate = (key: "certPath" | "keyPath", value: string) => {
    set(key, value)
    setFixed((f) => ({ ...f, [key]: value !== "" }))
  }
  // A certificate picked or issued is the one the site uses, whatever the
  // domains become.
  const pickCertificate = ({ certPath, keyPath }: { certPath: string; keyPath: string }) => {
    setSpec((s) => ({ ...s, tls: true, certPath, keyPath }))
    setFixed((f) => ({ ...f, certPath: true, keyPath: true }))
  }
  // A name typed by hand stays whatever the domains become, emptied or not:
  // "Match the domain" is the way back.
  const setName = (name: string) => {
    set("name", name)
    setFixed((f) => ({ ...f, name: true }))
  }
  const follow = (parts: (keyof IdentityFixed)[]) => {
    const unfixed = { ...fixed }
    for (const part of parts) unfixed[part] = false
    setFixed(unfixed)
    setSpec((s) => ({ ...s, ...deriveIdentity(s.domains, s, unfixed) }))
  }
  const choosePreset = (id: string) => {
    const chosen = presetById(id)
    if (!chosen) return
    setSpec((s) => applyPreset(s, chosen))
    setPreset(id)
    setUpstreamSet(true)
    // What to set on the application's side lands under the cards, often
    // past the pane's edge on a phone.
    requestAnimationFrame(() => presetNote.current?.scrollIntoView({ block: "nearest" }))
  }
  // Another kind picked by hand is no longer the preset's site: its card
  // lets go, its note goes, and so does what it put in.
  const chooseKind = (kind: SiteSpec["kind"]) => {
    const chosen = presetById(preset)
    if (chosen && chosen.spec.kind !== kind) {
      setSpec((s) => leavePreset(s, chosen, kind))
      setPreset(null)
      return
    }
    set("kind", kind)
  }
  const setUpstream = (upstream: string) => {
    set("upstream", upstream)
    setUpstreamSet(true)
  }

  const save = async (mode: SaveMode, allowConflict = false) => {
    setBusy(allowConflict ? "anyway" : mode)
    try {
      const existing = editing !== null
      const res = await post<SiteResult>(
        "/proxy/sites/",
        saveRequest(spec, {
          existing,
          ...SAVE_MODES[mode],
          allowConflict,
          baseDigest: base?.digest,
        }),
      )
      const outcome = saveOutcome(res, { existing })
      notify[outcome.tone](outcome.title, { description: outcome.description })
      onSaved()
      onOpenChange(false)
    } catch (err) {
      if (err instanceof ApiError && err.code === "name_conflict") {
        setConflict({ message: err.message, mode, spec })
      } else if (err instanceof ApiError && err.code === "site_changed") {
        // Somebody saved the file after the form read it. Nothing was
        // written: the file is read again and the operator asked which of
        // the two wins, rather than their change being written over.
        refused.current = true
        setReads((n) => n + 1)
      } else {
        notify.error("Not applied", err)
      }
    } finally {
      setBusy(null)
    }
  }

  // The refusal is said where the question is: focus moves onto it, so the
  // next Tab reaches "Save anyway" and a screen reader reads it, instead of
  // staying on a footer button that simply comes back enabled.
  useEffect(() => {
    if (!conflict) return
    conflictRef.current?.scrollIntoView({ block: "nearest" })
    conflictRef.current?.focus({ preventScroll: true })
  }, [conflict])
  const conflictShown = conflict?.spec === spec ? conflict : null
  useEffect(() => {
    if (!stale) return
    staleRef.current?.scrollIntoView({ block: "nearest" })
    staleRef.current?.focus({ preventScroll: true })
  }, [stale])

  // Unsaved changes: the draft is not what it started from. An edit whose
  // file has not been read yet has nothing to be measured against.
  const start = base?.spec ?? (source ? undefined : BLANK)
  const dirty = start !== undefined && !sameSpec(spec, start)
  // Every way out of the panel — Escape, the overlay, the close button —
  // comes through here, so one question covers them all. A trip to another
  // page is not a way out: the draft is kept for the tab.
  const requestClose = (next: boolean) => {
    if (busy) return
    if (next || !dirty) onOpenChange(next)
    else setDiscarding(true)
  }

  // What the identity would be if nothing were typed by hand, for the
  // "Match the domain" actions.
  const derived = deriveIdentity(spec.domains, spec, FOLLOW_DOMAINS)
  const file = preview?.name === spec.name ? preview : null
  const nameProblem = editing ? undefined : fileNameProblem(spec.name)
  const nameError =
    nameProblem && (spec.name !== "" || fixed.name)
      ? nameProblem
      : !editing && file?.exists
        ? `A site called ${spec.name} already exists. Pick another name, or open that site to edit it.`
        : !editing && file?.enabledElsewhere
          ? `${file.enabledElsewhere}, so the name is taken. Pick another name.`
          : undefined
  const certificateFollows =
    spec.domains.length === 0 ||
    (derived.certPath === spec.certPath && derived.keyPath === spec.keyPath)
  const idle =
    spec.kind === "proxy" && !spec.pool && (source !== null || upstreamSet)
      ? nothingListening(spec.upstream ?? "", picker.listeners, picker.containers)
      : undefined
  const chosenPreset = !source ? presetById(preset) : undefined
  const warnings = preview?.warnings ?? []
  const checks = preflight?.checks ?? []
  const dnsChecks = checks.filter((c) => c.domain !== undefined)
  const failed = checks.filter((c) => c.level === "fail").length
  const cautions = checks.filter((c) => c.level === "warning").length
  const blockingKey = checks
    .filter((c) => c.blocking)
    .map((c) => c.id)
    .join("\n")
  const held = blockingKey !== "" && overridden !== blockingKey
  const heldNote = held
    ? `${plural(failed, "check")} failed. Save waits until they pass, or until you allow it under Checks.`
    : null

  // Not while the file changed under the draft: the save would be refused
  // until the operator says which of the two wins.
  const ready =
    spec.domains.length > 0 &&
    spec.name !== "" &&
    file !== null &&
    !nameProblem &&
    !stale &&
    (editing !== null || (!file.exists && !file.enabledElsewhere))
  // A disabled site is saved as it is or enabled on purpose; "Save and
  // reload" did neither, and reloading changes nothing about a file nginx
  // does not read.
  const disabled = editing !== null && (file?.enabled ?? readFile.enabled) === false
  // Nor is a site disabled whose sites-enabled entry is a file of its own:
  // nginx serves that file, and a save here does not reach it.
  const servedCopy = editing !== null && (file?.servedCopy ?? readFile.servedCopy) === true
  // A conf.d site is on while its name ends in .conf, and a save does not
  // rename it: enabling one is not the form's to offer.
  const confd = file?.confd ?? readFile.confd
  const certKnown =
    !spec.tls || !spec.certPath || !certs.data || certs.data.some((c) => c.path === spec.certPath)
  const issueHref = `/proxy/certificates?issue=${encodeURIComponent(spec.domains.join(" "))}`

  // What a save drops of the file that the form cannot hold: as the latest
  // preview says for this draft, which leaves out lines moved into the extra
  // configuration, or, until there is one, as the file was read.
  const dropped: DroppedLine[] | undefined = editing ? (file?.dropped ?? disk?.dropped) : undefined
  const droppedLines = dropped ? droppedCount(dropped) : 0
  const movable = dropped ? movableLines(dropped, spec.custom) : []
  const filePath = file?.path
  const fileName = filePath ? filePath.slice(filePath.lastIndexOf("/") + 1) : spec.name
  const moveLines = () => {
    if (!dropped) return
    set("custom", withMovedLines(spec.custom, dropped))
    // Where they went, in view, rather than a notice that shrinks.
    requestAnimationFrame(() => customRef.current?.scrollIntoView({ block: "nearest" }))
  }
  // What the save writes over the file as it is on disk.
  const changes =
    editing && disk && file ? changesDiff(disk.content, file.content, fileName) : undefined
  const changed = changeCount(changes ?? null)

  // Ctrl or Cmd+S is the footer's own command: save and reload, or, for a
  // site nginx does not read, save it as it is. The raw editor and the
  // discard question over the form are their own.
  const primary: SaveMode = servedCopy || disabled ? "keep" : "reload"
  const onSaveKey = useEffectEvent((event: KeyboardEvent) => {
    const key = event.key.toLowerCase() === "s" && (event.ctrlKey || event.metaKey)
    if (!key || event.altKey || event.shiftKey || rawOpen || discarding) return
    event.preventDefault()
    if (ready && !held && busy === null) void save(primary)
  })
  useEffect(() => {
    if (!open) return
    const listener = (event: KeyboardEvent) => onSaveKey(event)
    window.addEventListener("keydown", listener)
    return () => window.removeEventListener("keydown", listener)
  }, [open])

  return (
    <SidePanel
      open={open}
      onOpenChange={requestClose}
      width="xl"
      title={
        <>
          <ProductLogo id="nginx-static" size="sm" />
          {editing ? `Edit ${editing}` : copyFrom ? `New site from ${copyFrom}` : "New site"}
          {dirty && <Tag tone="warning">unsaved</Tag>}
        </>
      }
      description={
        spec.domains.length > 0
          ? spec.domains.join(", ")
          : "A domain, where to send it, and whether it is encrypted"
      }
      bodyClassName="flex min-h-0 flex-1 flex-col gap-0 p-0 lg:flex-row"
      footer={
        servedCopy ? (
          <>
            <span className="mr-auto text-hint text-muted-foreground">
              {heldNote ?? (
                <>
                  nginx serves <code className="font-mono">sites-enabled/{spec.name}</code>, a file
                  of its own, not this one. Saving here changes nothing it serves until that file is
                  replaced by a link to this one.
                </>
              )}
            </span>
            <Button
              size="sm"
              onClick={() => save("keep")}
              disabled={!ready || held || busy !== null}
              pending={busy === "keep"}
              aria-keyshortcuts="Control+S Meta+S"
            >
              Save
            </Button>
          </>
        ) : disabled ? (
          <>
            <span className="mr-auto text-hint text-muted-foreground">
              {heldNote ??
                (file?.enabledElsewhere
                  ? `${file.enabledElsewhere}, so this site can be neither enabled nor tested under its name.`
                  : confd
                    ? "Disabled: nginx reads only the conf.d files ending in .conf. nginx tests it as if it did, and it stays off until it is renamed."
                    : "Disabled. nginx tests it as if enabled, and it stays off until you enable it.")}
            </span>
            {!file?.enabledElsewhere && !confd && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => save("enable")}
                disabled={!ready || held || busy !== null}
                pending={busy === "enable"}
              >
                Save and enable
              </Button>
            )}
            <Button
              size="sm"
              onClick={() => save("keep")}
              disabled={!ready || held || busy !== null}
              pending={busy === "keep"}
              aria-keyshortcuts="Control+S Meta+S"
            >
              Save (stays disabled)
            </Button>
          </>
        ) : (
          <>
            <span className="mr-auto text-hint text-muted-foreground">
              {heldNote ??
                (droppedLines > 0
                  ? `Saving drops ${plural(droppedLines, "line")} of the file, listed above.`
                  : "Validated with nginx\u2019s own parser before it takes effect, and rolled back if the test fails.")}
            </span>
            <Button
              size="sm"
              variant="outline"
              onClick={() => save("save")}
              disabled={!ready || held || busy !== null}
              pending={busy === "save"}
            >
              Save only
            </Button>
            <Button
              size="sm"
              onClick={() => save("reload")}
              disabled={!ready || held || busy !== null}
              pending={busy === "reload"}
              aria-keyshortcuts="Control+S Meta+S"
            >
              Save and reload
            </Button>
          </>
        )
      }
    >
      <div className="min-h-0 flex-1 overflow-y-auto p-4 lg:w-[26rem] lg:shrink-0 lg:border-r lg:border-hairline">
        <div className="space-y-6">
          {conflictShown && (
            <div ref={conflictRef} role="alert" tabIndex={-1} className="rounded-lg focus-ring">
              <Notice tone="warning" icon={Warning} title="Already served elsewhere">
                {conflictShown.message}
                <div className="mt-2">
                  <Button
                    size="xs"
                    variant="outline"
                    onClick={() => save(conflictShown.mode, true)}
                    disabled={busy !== null}
                    pending={busy === "anyway"}
                  >
                    Save anyway
                  </Button>
                </div>
              </Notice>
            </div>
          )}
          {stale && (
            <div ref={staleRef} role="alert" tabIndex={-1} className="rounded-lg focus-ring">
              <Notice
                tone="warning"
                icon={Warning}
                title={
                  stale === "gone"
                    ? `${fileName} is not on disk any more`
                    : `${fileName} changed on disk`
                }
              >
                <p>
                  {stale === "gone"
                    ? "Somebody removed it after the form read it. Keeping your draft writes it again when you save."
                    : "Somebody saved it after the form read it \u2014 by hand, in the raw editor, or from another tab. Keeping your draft saves over their change; reloading takes theirs and drops the edits made here."}
                </p>
                <div className="mt-2 flex flex-wrap gap-2">
                  {stale !== "gone" && (
                    <Button size="xs" variant="outline" onClick={() => take(stale)}>
                      Reload from disk
                    </Button>
                  )}
                  <Button size="xs" variant="outline" onClick={keepDraft}>
                    Keep my draft
                  </Button>
                </div>
              </Notice>
            </div>
          )}
          {editing && (!managed || droppedLines > 0) && (
            <Notice
              tone="warning"
              icon={Warning}
              title={
                droppedLines > 0
                  ? `Saving drops ${plural(droppedLines, "line")} of this ${managed ? "file" : "hand-written file"}`
                  : "This file was written by hand"
              }
            >
              {dropped && droppedLines > 0 ? (
                <>
                  <p>
                    {managed
                      ? `Added by hand, with no field in the form, so a save leaves ${dropped.length === 1 ? "it" : "them"} out.`
                      : `The form has no field for ${dropped.length === 1 ? "this" : "these"}, so a save leaves ${dropped.length === 1 ? "it" : "them"} out.`}
                    {!managed && (
                      <>
                        {" "}
                        The current file is kept as{" "}
                        <code className="font-mono">{fileName}.bak</code>.
                      </>
                    )}
                  </p>
                  <Well className="mt-2 overflow-hidden p-0">
                    <DiffView
                      body={droppedDiff(dropped)}
                      singleFile
                      lineNumbers
                      className="max-h-56"
                    />
                  </Well>
                  {movable.length < dropped.length && (
                    <p className="text-hint text-muted-foreground">
                      Only lines directly in the server block can move as they are. The others stay
                      only if the file is edited by hand.
                    </p>
                  )}
                  {dropped.some((d) => d.reason) && (
                    <ul className="text-hint text-muted-foreground">
                      {dropped
                        .filter((d) => d.reason)
                        .map((d) => (
                          <li key={`${d.line}:${d.text}`}>
                            Line {d.line}: {d.reason}.
                          </li>
                        ))}
                    </ul>
                  )}
                  <div className="mt-2 flex flex-wrap gap-2">
                    {movable.length > 0 && (
                      <Button size="xs" variant="outline" onClick={moveLines}>
                        Move {plural(droppedCount(movable), "line")} into Extra configuration
                      </Button>
                    )}
                    {filePath && (
                      <Button size="xs" variant="outline" onClick={() => setRawOpen(true)}>
                        Edit the raw file
                      </Button>
                    )}
                  </div>
                </>
              ) : dropped ? (
                <p>
                  The form reads every directive in it. Saving rewrites it in the form&rsquo;s own
                  layout, without its comments, and keeps the current file as{" "}
                  <code className="font-mono">{fileName}.bak</code>.
                </p>
              ) : (
                <p>
                  The form has read what it recognises. Saving replaces the file with what the form
                  produces, so anything it could not read is lost; the current file is kept as{" "}
                  <code className="font-mono">{fileName}.bak</code>.
                </p>
              )}
            </Notice>
          )}

          <Field
            label="Domains"
            htmlFor="site-domains"
            hint={
              editing
                ? "Space-separated."
                : "Space-separated. The first names the file and the certificate."
            }
          >
            <Input
              id="site-domains"
              value={domainText}
              onChange={(e) => commitDomains(e.target.value)}
              // The one field every new site needs, so the keyboard starts
              // here rather than on the first preset card.
              autoFocus={!editing}
              placeholder="app.example.com www.app.example.com"
              className="font-mono text-xs"
            />
          </Field>
          <DomainChecks checks={dnsChecks.filter((c) => spec.domains.includes(c.domain ?? ""))} />

          <Field
            label="File name"
            htmlFor="site-name"
            error={nameError}
            hint={
              file?.path
                ? `Saved as ${file.path}`
                : editing
                  ? "The file this site is saved in."
                  : fixed.name
                    ? "Stays as typed, whatever the domains become."
                    : "Follows the first domain until you change it."
            }
            trailing={
              !editing &&
              fixed.name &&
              derived.name !== "" &&
              derived.name !== spec.name && (
                <Button size="xs" variant="ghost" onClick={() => follow(["name"])}>
                  Match the domain
                </Button>
              )
            }
          >
            <Input
              id="site-name"
              value={spec.name}
              readOnly={editing !== null}
              onChange={(e) => setName(e.target.value)}
              placeholder="app.example.com"
              aria-invalid={nameError ? true : undefined}
              className={cn("font-mono text-xs", editing && "text-muted-foreground")}
            />
          </Field>

          {!source && (
            <FormSection title="Start from">
              <ChoiceGrid columns={2} className="grid-cols-2">
                {PRESETS.map((p) => (
                  <ProductCard
                    key={p.id}
                    product={p.product}
                    fallback={KIND_MARK[p.spec.kind ?? "proxy"]}
                    label={p.label}
                    detail={p.detail}
                    selected={preset === p.id}
                    onClick={() => choosePreset(p.id)}
                  />
                ))}
              </ChoiceGrid>
              {chosenPreset?.note && <FormNote ref={presetNote}>{chosenPreset.note}</FormNote>}
            </FormSection>
          )}

          <FormSection title="What it serves">
            <ChoiceGrid columns={3} className="grid-cols-3">
              {(
                [
                  { kind: "proxy", label: "An app" },
                  { kind: "static", label: "Files" },
                  { kind: "redirect", label: "A redirect" },
                ] as const
              ).map(({ kind, label }) => {
                const Mark = KIND_MARK[kind]
                return (
                  <ChoiceCard
                    key={kind}
                    selected={spec.kind === kind}
                    onClick={() => chooseKind(kind)}
                    className="min-h-20 justify-center"
                  >
                    <Mark aria-hidden className="size-4 text-muted-foreground" />
                    <span className="text-body font-medium">{label}</span>
                  </ChoiceCard>
                )
              })}
            </ChoiceGrid>
          </FormSection>

          {spec.kind === "proxy" && (
            <ToggleGroup
              type="single"
              aria-label="How many servers"
              value={spec.pool ? "pool" : "one"}
              onValueChange={(v) => {
                if (v === "pool" && !spec.pool) set("pool", poolFrom(spec.upstream))
                if (v === "one") set("pool", undefined)
              }}
              variant="outline"
              size="sm"
              className="w-full"
            >
              <ToggleGroupItem value="one" className="flex-1 text-hint">
                One server
              </ToggleGroupItem>
              <ToggleGroupItem value="pool" className="flex-1 text-hint">
                Several servers
              </ToggleGroupItem>
            </ToggleGroup>
          )}
          {spec.kind === "proxy" && spec.pool && (
            <PoolField pool={spec.pool} onChange={(pool) => set("pool", pool)} />
          )}
          {spec.kind === "proxy" && !spec.pool && (
            <Field
              label="Send it to"
              htmlFor="site-upstream"
              hint={
                idle ? (
                  <span className="text-warning">{idle}</span>
                ) : (
                  "Where the application is listening. Usually loopback on this machine."
                )
              }
            >
              <UpstreamPicker
                id="site-upstream"
                value={spec.upstream ?? ""}
                onChange={setUpstream}
                options={picker.options}
                loading={picker.loading}
                failed={picker.failed}
                onRetry={picker.onRetry}
                placeholder="http://127.0.0.1:3000"
              />
            </Field>
          )}
          {spec.kind === "static" && (
            <>
              <Field label="Directory" htmlFor="site-root" hint="The folder holding index.html.">
                <Input
                  id="site-root"
                  value={spec.root ?? ""}
                  onChange={(e) => set("root", e.target.value)}
                  placeholder="/var/www/site"
                  className="font-mono text-xs"
                />
              </Field>
              <OptionList>
                <OptionRow
                  title="Single-page app"
                  hint="A path with no file of its own gets index.html, so the app's router answers deep links and reloads."
                  checked={!!spec.spa}
                  onCheckedChange={(v) => set("spa", v)}
                />
              </OptionList>
            </>
          )}
          {spec.kind === "redirect" && (
            <>
              <Field
                label="Redirect to"
                htmlFor="site-redirect"
                hint="The path and query are carried across."
              >
                <Input
                  id="site-redirect"
                  value={spec.redirectTo ?? ""}
                  onChange={(e) => set("redirectTo", e.target.value)}
                  placeholder="https://new.example.com"
                  className="font-mono text-xs"
                />
              </Field>
              <OptionList>
                <OptionRow
                  title="Permanent (301)"
                  hint="Browsers cache a permanent redirect more or less forever. Use 302 while you are still deciding."
                  checked={!!spec.permanent}
                  onCheckedChange={(v) => set("permanent", v)}
                />
              </OptionList>
            </>
          )}

          <FormSection title="Encryption">
            <OptionList>
              <OptionRow
                title="Serve over HTTPS"
                hint="Needs a certificate on disk: use an installed one below, or get one."
                checked={spec.tls}
                onCheckedChange={(v) => set("tls", v)}
              >
                <div className="space-y-3">
                  <FieldRow>
                    <Field label="Certificate" htmlFor="site-cert">
                      <Input
                        id="site-cert"
                        value={spec.certPath ?? ""}
                        onChange={(e) => setCertificate("certPath", e.target.value)}
                        className="font-mono text-hint"
                      />
                    </Field>
                    <Field label="Private key" htmlFor="site-key">
                      <Input
                        id="site-key"
                        value={spec.keyPath ?? ""}
                        onChange={(e) => setCertificate("keyPath", e.target.value)}
                        className="font-mono text-hint"
                      />
                    </Field>
                  </FieldRow>
                  {!certificateFollows && (
                    <Button
                      size="xs"
                      variant="ghost"
                      className="-ml-2"
                      onClick={() => follow(["certPath", "keyPath"])}
                    >
                      Use the domain&rsquo;s certificate
                    </Button>
                  )}
                  {!certKnown && (
                    <FormNote tone="warning">
                      No certificate is listed at this path. If it does not exist yet, nginx refuses
                      the site at reload —{" "}
                      <Link href={issueHref} className="underline underline-offset-2">
                        issue one for {spec.domains[0] ?? "these domains"} first
                      </Link>
                      .
                    </FormNote>
                  )}
                </div>
              </OptionRow>
              {spec.tls && (
                <>
                  <OptionRow
                    title="Send HTTP visitors to HTTPS"
                    hint="The certificate protects nobody who arrives on the unencrypted port."
                    checked={spec.forceHttps}
                    onCheckedChange={(v) => set("forceHttps", v)}
                  />
                  <OptionRow
                    title="HSTS"
                    hint="Tells the browser never to use plain HTTP for this name again. Hard to undo — a mistake sticks for six months."
                    checked={spec.hsts}
                    onCheckedChange={(v) => set("hsts", v)}
                  />
                  <OptionRow
                    title="HTTP/2"
                    hint="Faster for pages with many small assets."
                    checked={spec.http2}
                    onCheckedChange={(v) => set("http2", v)}
                  />
                </>
              )}
            </OptionList>
            {spec.domains.length > 0 && (
              <SiteCertificate
                spec={spec}
                disk={editing ? disk : null}
                existing={editing !== null}
                baseDigest={base?.digest}
                served={!disabled && !servedCopy}
                onUse={pickCertificate}
                onRebased={(r) => {
                  setDisk(r)
                  setBase({ digest: r.digest, spec: specFromServer(r.spec) })
                  onSaved()
                }}
                onDone={(res) => {
                  const outcome = saveOutcome(res, { existing: true })
                  notify[outcome.tone](outcome.title, { description: outcome.description })
                  onSaved()
                  onOpenChange(false)
                }}
              />
            )}
          </FormSection>

          {spec.kind === "proxy" && (
            <FormSection title="Behaviour">
              <OptionList>
                <OptionRow
                  title="WebSockets"
                  hint="Needed by anything with live updates: a chat, a terminal, a dashboard."
                  checked={spec.webSockets}
                  onCheckedChange={(v) => set("webSockets", v)}
                />
              </OptionList>
              <FieldRow>
                <Field
                  label="Upload limit"
                  htmlFor="site-body"
                  hint="nginx refuses a larger request body with a 413."
                >
                  <Input
                    id="site-body"
                    value={spec.clientMaxBody ?? ""}
                    onChange={(e) => set("clientMaxBody", e.target.value)}
                    placeholder="50m"
                    className="font-mono text-xs"
                  />
                </Field>
                <Field
                  label="Timeout"
                  htmlFor="site-timeout"
                  hint="Seconds nginx waits for the application to answer."
                >
                  <Input
                    id="site-timeout"
                    value={String(spec.proxyTimeout ?? 60)}
                    inputMode="numeric"
                    onChange={(e) => set("proxyTimeout", Number(e.target.value) || 0)}
                    className="font-mono text-xs"
                  />
                </Field>
              </FieldRow>
              <OptionList>
                <OptionRow
                  title="Buffer responses"
                  hint="nginx holds the answer until it has it, freeing a slow application sooner. Off streams it as it is produced, which server-sent events and progress output need."
                  checked={spec.buffering ?? false}
                  onCheckedChange={(v) => set("buffering", v)}
                />
                <OptionRow
                  title="Stream uploads"
                  hint="The application gets a request body as it arrives, rather than after nginx has read all of it."
                  checked={spec.streamUploads ?? false}
                  onCheckedChange={(v) => set("streamUploads", v)}
                />
              </OptionList>
              <UpstreamAdvanced spec={spec} set={set} />
            </FormSection>
          )}

          <FormSection title="Hardening">
            <OptionList>
              <OptionRow
                title="Security headers"
                hint="nosniff, SAMEORIGIN and a referrer policy. Safe defaults for almost any site."
                checked={spec.securityHeaders}
                onCheckedChange={(v) => set("securityHeaders", v)}
              />
              <OptionRow
                title="Block common probes"
                hint="Refuses requests for dotfiles and backup extensions — the shapes scanners ask for all day."
                checked={spec.blockExploits}
                onCheckedChange={(v) => set("blockExploits", v)}
              />
              <OptionRow
                title="Compress responses"
                hint="gzip for text, JSON, scripts and SVG."
                checked={spec.gzip}
                onCheckedChange={(v) => set("gzip", v)}
              />
              <OptionRow
                title="Access log"
                hint="Off keeps the disk quiet; on is what you want when something goes wrong."
                checked={spec.accessLog}
                onCheckedChange={(v) => set("accessLog", v)}
              />
            </OptionList>
            {spec.accessLog && (
              <Field
                label="Log format"
                htmlFor="site-log-format"
                hint="Timed is what the traffic pages read latency from."
              >
                <Select
                  value={spec.logFormat ?? "timed"}
                  onValueChange={(v) => set("logFormat", v as SiteSpec["logFormat"])}
                >
                  <SelectTrigger id="site-log-format" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="timed">Timed (adds response time)</SelectItem>
                    <SelectItem value="combined">Standard</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            )}
          </FormSection>

          <FormSection title="Who may reach it">
            <ListField
              id="site-allow"
              label="Allow only these"
              placeholder="10.0.0.0/8"
              values={spec.allowFrom}
              onChange={(v) => set("allowFrom", v)}
              hint="Filling this in refuses everything else. Include however you reach the site yourself."
            />
            <ListField
              id="site-deny"
              label="Deny"
              placeholder="203.0.113.0/24"
              values={spec.denyFrom}
              onChange={(v) => set("denyFrom", v)}
              hint="Exceptions, checked before the allow list. The fence at the end is written for you."
            />
            <Field
              label="Password file"
              htmlFor="site-auth"
              hint={
                authFiles.data && authFiles.data.length > 0
                  ? "One of the files managed below, or any htpasswd file. Leave empty for no password."
                  : "An htpasswd file. Create one in Password files below, or leave empty for no password."
              }
            >
              <Input
                id="site-auth"
                value={spec.basicAuthFile ?? ""}
                onChange={(e) => set("basicAuthFile", e.target.value)}
                placeholder="/etc/nginx/jd-auth/staging"
                list="site-auth-files"
                className="font-mono text-hint"
              />
              <datalist id="site-auth-files">
                {authFiles.data?.map((f) => (
                  <option key={f.path} value={f.path} />
                ))}
              </datalist>
            </Field>
          </FormSection>

          <RealIPSection spec={spec} set={set} open={open} />

          {spec.kind !== "redirect" && <LimitsSection spec={spec} set={set} />}

          {spec.kind !== "redirect" && <CachingSection spec={spec} set={set} />}

          <HeadersSection spec={spec} set={set} />

          <PagesSection spec={spec} set={set} site={editing} />

          {spec.kind === "proxy" && (
            <FormSection
              title="Paths that go somewhere else"
              hint="Everything not matched by one of these goes to the site's main upstream."
            >
              <LocationsField
                locations={spec.locations}
                onChange={(v) => set("locations", v)}
                picker={picker}
              />
              {spec.locations.length > 0 && <RoutesTable spec={spec} />}
            </FormSection>
          )}

          <FormSection title="Anything else">
            <Field
              label="Extra configuration"
              htmlFor="site-custom"
              hint="Added verbatim inside the server block."
            >
              <Textarea
                id="site-custom"
                ref={customRef}
                value={spec.custom ?? ""}
                onChange={(e) => set("custom", e.target.value)}
                rows={4}
                className="font-mono text-hint"
                placeholder="# valid nginx directives"
              />
            </Field>
          </FormSection>
        </div>
      </div>

      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 p-4">
        <Tabs defaultValue="preview" className="flex min-h-0 flex-1 flex-col gap-3">
          <TabsList>
            <TabsTrigger value="preview">
              <Code className="size-3.5" />
              nginx config
            </TabsTrigger>
            {editing && (
              <TabsTrigger value="changes">
                Changes
                {changed > 0 && (
                  <span className="numeric ml-1 text-hint font-medium text-muted-foreground">
                    {changed}
                  </span>
                )}
              </TabsTrigger>
            )}
            <TabsTrigger value="checks">
              Checks
              {failed + cautions > 0 && (
                <span
                  className={cn(
                    "numeric ml-1 text-hint font-medium",
                    failed > 0 ? "text-destructive" : "text-warning",
                  )}
                >
                  {failed || cautions}
                </span>
              )}
            </TabsTrigger>
            <TabsTrigger value="notes">
              Notes
              {warnings.length > 0 && (
                <span className="numeric ml-1 text-hint font-medium text-warning">
                  {warnings.length}
                </span>
              )}
            </TabsTrigger>
          </TabsList>
          <TabsContent value="preview" className="min-h-0 flex-1">
            <Pane className="h-full">
              {previewError ? (
                <EmptyNote className="my-auto text-destructive">{previewError}</EmptyNote>
              ) : preview ? (
                <CodeEditor className="h-full" language="ini" value={preview.content} readOnly />
              ) : (
                <EmptyNote className="my-auto">
                  Enter a domain and the config appears here, rendered by the server that will write
                  it.
                </EmptyNote>
              )}
            </Pane>
          </TabsContent>
          {editing && (
            <TabsContent value="changes" className="min-h-0 flex-1">
              <Pane className="h-full">
                {changes === undefined ? (
                  <EmptyNote className={cn("my-auto", previewError && "text-destructive")}>
                    {previewError ||
                      "What a save changes in the file appears here once the form has read it."}
                  </EmptyNote>
                ) : changes === null ? (
                  <EmptyNote className="my-auto">
                    Too many changes to line up. The nginx config tab has the whole file a save
                    writes.
                  </EmptyNote>
                ) : changes === "" ? (
                  <EmptyNote className="my-auto">
                    Saving writes the file exactly as it is on disk.
                  </EmptyNote>
                ) : (
                  <DiffView body={changes} singleFile lineNumbers className="h-full" />
                )}
              </Pane>
            </TabsContent>
          )}
          <TabsContent value="checks" className="min-h-0 flex-1 space-y-3 overflow-y-auto">
            {held && (
              <Notice tone="danger" icon={Warning} title="Save waits for these">
                <p>
                  Each failed check below is a site that will not work as saved. Fix them, or save
                  it as it is.
                </p>
                <div className="mt-2">
                  <Button size="xs" variant="outline" onClick={() => setOverridden(blockingKey)}>
                    Allow saving anyway
                  </Button>
                </div>
              </Notice>
            )}
            {preflightError ? (
              <EmptyNote className="my-auto text-destructive">{preflightError}</EmptyNote>
            ) : !preflight ? (
              <EmptyNote className="my-auto">
                Enter a domain and what nginx -t cannot check appears here: the certificate and key,
                password files, folders, upstreams and DNS.
              </EmptyNote>
            ) : (
              <ul className="divide-y divide-hairline">
                {checks.map((check) => (
                  <li key={check.id} className="flex items-start gap-2.5 py-2.5 text-body">
                    <StatusDot tone={CHECK_TONE[check.level]} className="mt-1.5" />
                    <span className="min-w-0 leading-relaxed">
                      <span className="font-medium">
                        {check.domain && <span className="font-mono">{check.domain}: </span>}
                        {check.title}
                      </span>
                      {check.detail && (
                        <span className="block text-muted-foreground">{check.detail}</span>
                      )}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </TabsContent>
          <TabsContent value="notes" className="min-h-0 flex-1 overflow-y-auto">
            {warnings.length === 0 ? (
              <p className="flex items-center gap-2.5 py-2 text-body text-muted-foreground">
                <StatusDot tone="running" />
                Every setting here is one this dashboard would have chosen.
              </p>
            ) : (
              <ul className="divide-y divide-hairline">
                {warnings.map((warning) => (
                  <li key={warning} className="flex items-start gap-2.5 py-2.5 text-body">
                    <StatusDot tone="warning" className="mt-1.5" />
                    <span className="min-w-0 leading-relaxed">{warning}</span>
                  </li>
                ))}
              </ul>
            )}
          </TabsContent>
        </Tabs>
      </div>
      {/* Inside the sheet, so each is layered over it: Escape and a click
          outside close it, not the form behind it. */}
      <Modal
        open={discarding}
        onOpenChange={setDiscarding}
        size="sm"
        title="Discard changes?"
        footer={
          <>
            <Button variant="outline" onClick={() => setDiscarding(false)}>
              Keep editing
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                setDiscarding(false)
                onOpenChange(false)
              }}
            >
              Discard
            </Button>
          </>
        }
      >
        <p className="text-body leading-relaxed">
          {editing
            ? `What you changed in ${editing} is not saved. Discarding it leaves the file as it is.`
            : "This new site is not saved. Discarding it throws away what was filled in."}
        </p>
      </Modal>
      {editing && filePath && (
        <ConfigEditor
          open={rawOpen}
          onOpenChange={setRawOpen}
          path={filePath}
          kind="nginx"
          title={fileName}
          initialLine={dropped?.[0]?.line}
          siteDisabled={disabled}
          onSaved={() => {
            setReads((n) => n + 1)
            onSaved()
          }}
        />
      )}
    </SidePanel>
  )
}

const CHECK_TONE: Record<PreflightLevel, DotTone> = {
  ok: "running",
  info: "stopped",
  warning: "warning",
  fail: "danger",
}

/**
 * Where each domain points, from the preflight.
 *
 * The first question of every reverse-proxy setup and the cause of most of the
 * failures: certbot cannot prove control of a name that resolves somewhere
 * else, and the error it gives says "challenge failed" rather than "your DNS
 * is not updated yet". Every domain, not only the first: a www alias that
 * points elsewhere fails the certificate order just the same.
 */
function DomainChecks({ checks }: { checks: PreflightCheck[] }) {
  if (checks.length === 0) return null
  return (
    <ul className="-mt-3 space-y-1">
      {checks.map((check) => (
        <li key={check.id} className="flex items-start gap-2 text-hint leading-relaxed">
          <StatusDot tone={CHECK_TONE[check.level]} className="mt-1.5" />
          <span className="min-w-0 text-muted-foreground">
            <span className="font-mono text-foreground">{check.domain}</span> {check.title}.{" "}
            {check.level !== "ok" && check.detail}
          </span>
        </li>
      ))}
    </ul>
  )
}

function ListField({
  id,
  label,
  placeholder,
  values,
  onChange,
  hint,
}: {
  id: string
  label: string
  placeholder: string
  values: string[]
  onChange: (values: string[]) => void
  hint?: string
}) {
  const [draft, setDraft] = useState("")
  const add = () => {
    if (!draft.trim()) return
    onChange([...values, draft.trim()])
    setDraft("")
  }
  return (
    <Field label={label} htmlFor={id} hint={hint}>
      <div className="space-y-2">
        {values.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            {values.map((value, i) => (
              <Tag key={`${value}-${i}`} mono className="gap-1.5 pr-0.5">
                {value}
                <IconAction
                  label={`Remove ${value}`}
                  size="icon-xs"
                  className="size-4 text-muted-foreground hover:text-destructive [&_svg:not([class*='size-'])]:size-2.5"
                  onClick={() => onChange(values.filter((_, j) => j !== i))}
                >
                  <Trash />
                </IconAction>
              </Tag>
            ))}
          </div>
        )}
        <div className="flex gap-2">
          <Input
            id={id}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault()
                add()
              }
            }}
            placeholder={placeholder}
            className="font-mono text-xs"
          />
          <Button
            size="sm"
            variant="outline"
            onClick={add}
            disabled={!draft.trim()}
            aria-label={`Add to ${label.toLowerCase()}`}
          >
            <Plus className="size-3.5" />
          </Button>
        </div>
      </div>
    </Field>
  )
}

/**
 * Extra paths, each sent somewhere other than the site's default.
 *
 * The commonest reverse-proxy layout after "one app on one domain" is two:
 * /api to a backend and everything else to a static build, or /ws to a socket
 * server. The renderer has always supported it and the form did not, which
 * meant the one arrangement past the simplest sent people to the raw editor.
 *
 * nginx matches the longest prefix regardless of order, so these are rendered
 * before the catch-all purely because that is the order a reader expects.
 */
/** What the upstream pickers offer, read once for the whole form. */
type Picker = {
  options: UpstreamOption[]
  listeners: Listener[] | undefined
  containers: Container[] | undefined
  loading: boolean
  failed: boolean
  onRetry: () => void
}

function LocationsField({
  locations,
  onChange,
  picker,
}: {
  locations: SiteLocation[]
  onChange: (locations: SiteLocation[]) => void
  picker: Picker
}) {
  const update = (i: number, patch: Partial<SiteLocation>) =>
    onChange(locations.map((loc, j) => (j === i ? { ...loc, ...patch } : loc)))

  return (
    <div className="space-y-2">
      {locations.map((loc, i) => (
        <LocationCard
          key={i}
          index={i}
          loc={loc}
          update={(patch) => update(i, patch)}
          remove={() => onChange(locations.filter((_, j) => j !== i))}
          picker={picker}
        />
      ))}
      <Button
        size="sm"
        variant="outline"
        onClick={() => onChange([...locations, { path: "", upstream: "", webSockets: false }])}
      >
        <Plus className="size-3.5" />
        Add a path
      </Button>
    </div>
  )
}

const MATCHES: LocationMatch[] = ["", "^~", "=", "~", "~*"]

/** One extra path: how it matches, where it goes, and what it does differently. */
function LocationCard({
  index,
  loc,
  update,
  remove,
  picker,
}: {
  index: number
  loc: SiteLocation
  update: (patch: Partial<SiteLocation>) => void
  remove: () => void
  picker: Picker
}) {
  const id = `site-loc-${index}`
  const match = loc.match ?? ""
  const prefix = match === "" || match === "^~"
  const regex = match === "~" || match === "~*"
  const folder = !loc.upstream && Boolean(loc.root)
  const forwards = Boolean(loc.upstream)
  const shown = renderedPath(loc)
  const facts = [
    loc.bodyLimit && `uploads ${loc.bodyLimit}`,
    loc.timeout && `${loc.timeout}s timeout`,
    loc.buffering && `buffering ${loc.buffering}`,
    loc.requestBuffering && `upload buffering ${loc.requestBuffering}`,
    loc.basicAuthFile && "own password",
    (loc.allowFrom?.length || loc.denyFrom?.length) && "own address list",
  ].filter(Boolean)
  // An exact or regex path cannot serve a folder with alias, so a folder
  // there is nginx's root, which adds the path to it.
  const setMatch = (next: LocationMatch) =>
    update({
      match: next || undefined,
      ...(next === "" || next === "^~"
        ? {}
        : { stripPrefix: false, spa: false, ...(loc.root ? { rootMode: "root" as const } : {}) }),
    })
  return (
    <Group className="space-y-2">
      <div className="flex items-center gap-2">
        <Select
          value={match || "prefix"}
          onValueChange={(v) => setMatch(v === "prefix" ? "" : (v as LocationMatch))}
        >
          <SelectTrigger aria-label="Match" className="w-44 shrink-0">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {MATCHES.map((m) => (
              <SelectItem key={m || "prefix"} value={m || "prefix"}>
                {MATCH_LABEL[m]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          value={loc.path}
          onChange={(e) => update({ path: e.target.value })}
          placeholder={regex ? "\\.(png|jpg)$" : "/api"}
          aria-label="Path"
          className="font-mono text-xs"
        />
        <IconAction
          label={`Remove ${loc.path || "location"}`}
          className="text-destructive"
          onClick={remove}
        >
          <Trash />
        </IconAction>
      </div>
      <UpstreamPicker
        value={loc.upstream ?? ""}
        onChange={(upstream) => update({ upstream, root: "" })}
        options={picker.options}
        loading={picker.loading}
        failed={picker.failed}
        onRetry={picker.onRetry}
        placeholder={
          regex
            ? "http://127.0.0.1:4000 — no path after the port"
            : "http://127.0.0.1:4000 — or leave empty and give a folder"
        }
        label="Upstream"
        pickLabel={`Pick from running services for ${loc.path || "this path"}`}
      />
      {loc.upstream && (
        <IdleNote
          upstream={loc.upstream}
          listeners={picker.listeners}
          containers={picker.containers}
        />
      )}
      {!loc.upstream && (
        <Input
          value={loc.root ?? ""}
          onChange={(e) =>
            update({ root: e.target.value, ...(prefix ? {} : { rootMode: "root" as const }) })
          }
          placeholder="/var/www/assets"
          aria-label="Folder"
          className="font-mono text-xs"
        />
      )}
      {folder && loc.rootMode === "root" && (
        <FormNote>
          Files come from{" "}
          <code className="font-mono">
            {loc.root?.replace(/\/$/, "")}
            {prefix ? loc.path : "<the request's path>"}
          </code>
          : nginx adds the path to the folder.
          {prefix && (
            <>
              {" "}
              <button
                type="button"
                className="underline underline-offset-2 hover:text-foreground"
                onClick={() => update({ rootMode: undefined })}
              >
                Serve the folder itself at {loc.path || "the path"}
              </button>
            </>
          )}
        </FormNote>
      )}
      {forwards && prefix && (
        <label className="flex items-center gap-2 text-hint text-muted-foreground">
          <Checkbox
            checked={loc.stripPrefix ?? false}
            onCheckedChange={(v) => update({ stripPrefix: Boolean(v) })}
          />
          Strip {loc.path || "the path"} before forwarding
          {loc.stripPrefix && loc.path && (
            <span className="font-mono">
              ({shown.replace(/\/$/, "")}/users arrives as {splitUpstream(renderedUpstream(loc))[1]}
              users)
            </span>
          )}
        </label>
      )}
      {folder && prefix && (
        <label className="flex items-center gap-2 text-hint text-muted-foreground">
          <Checkbox
            checked={loc.spa ?? false}
            onCheckedChange={(v) => update({ spa: Boolean(v) })}
          />
          Single-page app: a path with no file gets {shown}index.html
        </label>
      )}
      {forwards && (
        <label className="flex items-center gap-2 text-hint text-muted-foreground">
          <Checkbox
            checked={loc.webSockets}
            onCheckedChange={(v) => update({ webSockets: Boolean(v) })}
          />
          WebSockets on this path
        </label>
      )}
      <label className="flex items-center gap-2 text-hint text-muted-foreground">
        <Checkbox
          checked={Boolean(loc.rateLimit)}
          onCheckedChange={(v) =>
            update({ rateLimit: v ? { rate: "10r/m", burst: 5 } : undefined })
          }
        />
        A request rate of its own
      </label>
      {loc.rateLimit && (
        <RequestLimitFields
          id={`${id}-limit`}
          value={loc.rateLimit}
          onChange={(rateLimit) => update({ rateLimit })}
        />
      )}
      <Disclosure quiet summary="Advanced" facts={facts.length > 0 ? facts.join(" · ") : undefined}>
        <div className="space-y-3 pt-2">
          {forwards && (
            <>
              <FieldRow>
                <Field label="Upload limit" htmlFor={`${id}-body`} hint="Empty keeps the site's.">
                  <Input
                    id={`${id}-body`}
                    value={loc.bodyLimit ?? ""}
                    onChange={(e) => update({ bodyLimit: e.target.value || undefined })}
                    placeholder="200m"
                    className="font-mono text-xs"
                  />
                </Field>
                <Field
                  label="Timeout"
                  htmlFor={`${id}-timeout`}
                  hint="Seconds; empty keeps the site's."
                >
                  <Input
                    id={`${id}-timeout`}
                    value={loc.timeout ? String(loc.timeout) : ""}
                    inputMode="numeric"
                    onChange={(e) => update({ timeout: Number(e.target.value) || undefined })}
                    placeholder="300"
                    className="font-mono text-xs"
                  />
                </Field>
              </FieldRow>
              <FieldRow>
                <OnOffField
                  id={`${id}-buffering`}
                  label="Response buffering"
                  value={loc.buffering}
                  onChange={(buffering) => update({ buffering })}
                />
                <OnOffField
                  id={`${id}-request-buffering`}
                  label="Upload buffering"
                  value={loc.requestBuffering}
                  onChange={(requestBuffering) => update({ requestBuffering })}
                />
              </FieldRow>
            </>
          )}
          <Field
            label="Password file"
            htmlFor={`${id}-auth`}
            hint="In place of the site's on this path. Empty keeps the site's."
          >
            <Input
              id={`${id}-auth`}
              value={loc.basicAuthFile ?? ""}
              onChange={(e) => update({ basicAuthFile: e.target.value || undefined })}
              placeholder="/etc/nginx/jd-auth/api"
              list="site-auth-files"
              className="font-mono text-hint"
            />
          </Field>
          <ListField
            id={`${id}-allow`}
            label="Allow only these"
            placeholder="10.0.0.0/8"
            values={loc.allowFrom ?? []}
            onChange={(allowFrom) => update({ allowFrom })}
            hint="A list here replaces the site's on this path — nginx uses only one."
          />
          <ListField
            id={`${id}-deny`}
            label="Deny"
            placeholder="203.0.113.0/24"
            values={loc.denyFrom ?? []}
            onChange={(denyFrom) => update({ denyFrom })}
          />
        </div>
      </Disclosure>
    </Group>
  )
}

/** The site's setting, or on or off, for this path. */
function OnOffField({
  id,
  label,
  value,
  onChange,
}: {
  id: string
  label: string
  value: "on" | "off" | undefined
  onChange: (value: "on" | "off" | undefined) => void
}) {
  return (
    <Field label={label} htmlFor={id}>
      <Select
        value={value ?? "site"}
        onValueChange={(v) => onChange(v === "on" || v === "off" ? v : undefined)}
      >
        <SelectTrigger id={id} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="site">The site&apos;s</SelectItem>
          <SelectItem value="on">On</SelectItem>
          <SelectItem value="off">Off</SelectItem>
        </SelectContent>
      </Select>
    </Field>
  )
}

/** Each path as nginx will decide it, most decisive first. */
function RoutesTable({ spec }: { spec: SiteSpec }) {
  const routes = siteRoutes(spec)
  return (
    <Group className="p-0">
      <Table aria-label="What each path does">
        <TableHeader>
          <TableRow>
            <TableHead>Path</TableHead>
            <TableHead>Goes to</TableHead>
            <TableHead>For example</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {routes.map((route) => (
            <TableRow key={route.location}>
              <TableCell className="align-top">
                <div className="font-mono">{route.location}</div>
                <div className="text-hint text-muted-foreground">{route.rule}</div>
              </TableCell>
              <TableCell className="align-top">
                <div className="font-mono break-all">{route.target || "—"}</div>
                {route.notes.length > 0 && (
                  <div className="text-hint text-muted-foreground">{route.notes.join(" · ")}</div>
                )}
              </TableCell>
              <TableCell className="align-top font-mono text-hint text-muted-foreground">
                {route.example
                  ? `${route.example.request} → ${route.example.reaches}`
                  : "the request's own path"}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Group>
  )
}

/** The Host header and how nginx talks to an HTTPS upstream. */
function UpstreamAdvanced({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const https =
    spec.pool?.scheme === "https" ||
    [spec.pool ? "" : spec.upstream, ...spec.locations.map((loc) => loc.upstream)].some((u) =>
      u?.startsWith("https://"),
    )
  const host = spec.hostHeader ?? ""
  const facts = [
    host === "upstream" && "Host: the upstream's",
    host === "custom" && `Host: ${spec.hostHeaderValue ?? ""}`,
    spec.upstreamSni && "sends SNI",
    spec.upstreamVerify && "verifies the certificate",
  ].filter(Boolean)
  return (
    <Disclosure
      quiet
      summary="Advanced upstream"
      facts={facts.length > 0 ? facts.join(" · ") : undefined}
    >
      <div className="space-y-3 pt-2">
        <Field
          label="Host header"
          htmlFor="site-host-header"
          hint="What the application is told it was asked for. Most applications want the visitor's."
        >
          <ToggleGroup
            id="site-host-header"
            type="single"
            value={host || "visitor"}
            onValueChange={(v) => {
              if (!v) return
              set("hostHeader", v === "visitor" ? undefined : (v as SiteSpec["hostHeader"]))
            }}
            variant="outline"
            size="sm"
            className="w-full"
          >
            <ToggleGroupItem value="visitor" className="flex-1 text-hint">
              The visitor&apos;s
            </ToggleGroupItem>
            <ToggleGroupItem
              value="upstream"
              disabled={Boolean(spec.pool)}
              className="flex-1 text-hint"
            >
              The upstream&apos;s
            </ToggleGroupItem>
            <ToggleGroupItem value="custom" className="flex-1 text-hint">
              Custom
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
        {host === "custom" && (
          <Field label="Host" htmlFor="site-host-value">
            <Input
              id="site-host-value"
              value={spec.hostHeaderValue ?? ""}
              onChange={(e) => set("hostHeaderValue", e.target.value)}
              placeholder="app.internal"
              className="font-mono text-xs"
            />
          </Field>
        )}
        {!https && (
          <FormNote>
            The settings below apply to an https:// upstream, and this site forwards to none.
          </FormNote>
        )}
        <OptionList>
          <OptionRow
            title="Send the name in the TLS handshake (SNI)"
            hint="nginx does not by default, and a host serving several names over HTTPS answers without one with the wrong certificate or not at all."
            checked={spec.upstreamSni ?? false}
            onCheckedChange={(v) => set("upstreamSni", v)}
          />
          <OptionRow
            title="Verify the upstream's certificate"
            hint="Without this nginx accepts any certificate the upstream presents."
            checked={spec.upstreamVerify ?? false}
            onCheckedChange={(v) => set("upstreamVerify", v)}
          >
            <Field
              label="CA file"
              htmlFor="site-upstream-ca"
              hint="Empty checks against the system's CAs."
            >
              <Input
                id="site-upstream-ca"
                value={spec.upstreamCa ?? ""}
                onChange={(e) => set("upstreamCa", e.target.value)}
                placeholder="/etc/ssl/certs/ca-certificates.crt"
                className="font-mono text-xs"
              />
            </Field>
          </OptionRow>
        </OptionList>
        {(spec.upstreamSni || spec.upstreamVerify) && (
          <Field
            label="TLS name"
            htmlFor="site-upstream-name"
            hint={
              spec.pool?.scheme === "https"
                ? "Sent and checked for every server of the pool — required, since the pool's own name is internal."
                : "Sent and checked in place of the upstream's host — for an upstream addressed by IP. Empty uses the host."
            }
          >
            <Input
              id="site-upstream-name"
              value={spec.upstreamTlsName ?? ""}
              onChange={(e) => set("upstreamTlsName", e.target.value)}
              placeholder="api.example.com"
              className="font-mono text-xs"
            />
          </Field>
        )}
      </div>
    </Disclosure>
  )
}

const POOL_METHODS: { value: PoolMethod; label: string }[] = [
  { value: "", label: "Round robin" },
  { value: "least_conn", label: "Least connections" },
  { value: "ip_hash", label: "Client IP" },
  { value: "hash", label: "URI hash" },
  { value: "random", label: "Random" },
]

const RETRY_CONDITIONS: { value: RetryCondition; label: string }[] = [
  { value: "error", label: "Connection error" },
  { value: "timeout", label: "Timeout" },
  { value: "invalid_header", label: "Invalid response" },
  { value: "http_502", label: "502" },
  { value: "http_503", label: "503" },
  { value: "http_504", label: "504" },
  { value: "http_500", label: "500" },
  { value: "http_429", label: "429" },
  { value: "non_idempotent", label: "Also POST, PUT and DELETE" },
]

/** nginx's own when proxy_next_upstream is not written. */
const DEFAULT_RETRY: RetryCondition[] = ["error", "timeout"]

/** A new pool starts from the single upstream it replaces, when that is a bare address. */
function poolFrom(upstream: string | undefined): SitePool {
  const match = /^(https?):\/\/([^/]+)$/.exec(upstream?.trim() ?? "")
  if (!match) return { servers: [{ address: "" }] }
  return { scheme: match[1] === "https" ? "https" : undefined, servers: [{ address: match[2] }] }
}

/** Whole numbers only; empty is nginx's default. */
function count(value: string): number | undefined {
  return Number(value) || undefined
}

/** Several servers sharing the site's requests, as one nginx upstream block. */
function PoolField({ pool, onChange }: { pool: SitePool; onChange: (pool: SitePool) => void }) {
  const set = <K extends keyof SitePool>(key: K, value: SitePool[K]) =>
    onChange({ ...pool, [key]: value })
  const update = (i: number, patch: Partial<PoolServer>) =>
    set(
      "servers",
      pool.servers.map((server, j) => (j === i ? { ...server, ...patch } : server)),
    )
  const method = pool.method ?? ""
  // nginx refuses a backup server beside a method that picks from the request.
  const noBackup = method === "ip_hash" || method === "hash" || method === "random"
  const retry = pool.retryOn?.length ? pool.retryOn : DEFAULT_RETRY
  const toggleRetry = (cond: RetryCondition, on: boolean) => {
    const next = RETRY_CONDITIONS.map((c) => c.value).filter((c) =>
      c === cond ? on : retry.includes(c),
    )
    set("retryOn", next.length > 0 ? next : ["off"])
  }
  return (
    <div className="space-y-3">
      <FieldRow>
        <Field label="Balancing" htmlFor="site-pool-method">
          <Select
            value={method || "round_robin"}
            onValueChange={(v) => {
              const next = v === "round_robin" ? "" : (v as PoolMethod)
              const keepsBackup = next === "" || next === "least_conn"
              onChange({
                ...pool,
                method: next || undefined,
                servers: keepsBackup
                  ? pool.servers
                  : pool.servers.map((server) => ({ ...server, backup: undefined })),
              })
            }}
          >
            <SelectTrigger id="site-pool-method" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {POOL_METHODS.map((m) => (
                <SelectItem key={m.value || "round_robin"} value={m.value || "round_robin"}>
                  {m.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="Spoken to over" htmlFor="site-pool-scheme">
          <ToggleGroup
            id="site-pool-scheme"
            type="single"
            value={pool.scheme || "http"}
            onValueChange={(v) => v && set("scheme", v === "https" ? "https" : undefined)}
            variant="outline"
            size="sm"
            className="w-full"
          >
            <ToggleGroupItem value="http" className="flex-1 text-hint">
              http
            </ToggleGroupItem>
            <ToggleGroupItem value="https" className="flex-1 text-hint">
              https
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
      </FieldRow>
      {pool.servers.map((server, i) => (
        <Group key={i} className="space-y-2">
          <div className="flex items-center gap-2">
            <Input
              value={server.address}
              onChange={(e) => update(i, { address: e.target.value })}
              placeholder="127.0.0.1:3000"
              aria-label={`Server ${i + 1} address`}
              className="font-mono text-xs"
            />
            <IconAction
              label={`Remove ${server.address || `server ${i + 1}`}`}
              className="text-destructive"
              disabled={pool.servers.length === 1}
              onClick={() =>
                set(
                  "servers",
                  pool.servers.filter((_, j) => j !== i),
                )
              }
            >
              <Trash />
            </IconAction>
          </div>
          <FieldRow columns={3}>
            <Field label="Weight" htmlFor={`site-pool-${i}-weight`}>
              <Input
                id={`site-pool-${i}-weight`}
                value={server.weight ? String(server.weight) : ""}
                inputMode="numeric"
                placeholder="1"
                onChange={(e) => update(i, { weight: count(e.target.value) })}
                className="font-mono text-xs"
              />
            </Field>
            <Field label="Max fails" htmlFor={`site-pool-${i}-fails`}>
              <Input
                id={`site-pool-${i}-fails`}
                value={server.maxFails ? String(server.maxFails) : ""}
                inputMode="numeric"
                placeholder="1"
                onChange={(e) => update(i, { maxFails: count(e.target.value) })}
                className="font-mono text-xs"
              />
            </Field>
            <Field label="Fail timeout" htmlFor={`site-pool-${i}-timeout`}>
              <Input
                id={`site-pool-${i}-timeout`}
                value={server.failTimeout ? String(server.failTimeout) : ""}
                inputMode="numeric"
                placeholder="10"
                onChange={(e) => update(i, { failTimeout: count(e.target.value) })}
                className="font-mono text-xs"
              />
            </Field>
          </FieldRow>
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            <label className="flex items-center gap-2 text-hint text-muted-foreground">
              <Checkbox
                checked={server.backup ?? false}
                disabled={noBackup}
                onCheckedChange={(v) => update(i, { backup: Boolean(v) || undefined })}
              />
              Backup: used only while the others are unavailable
            </label>
            <label className="flex items-center gap-2 text-hint text-muted-foreground">
              <Checkbox
                checked={server.down ?? false}
                onCheckedChange={(v) => update(i, { down: Boolean(v) || undefined })}
              />
              Down: sent nothing
            </label>
          </div>
        </Group>
      ))}
      <Button
        size="sm"
        variant="outline"
        onClick={() => set("servers", [...pool.servers, { address: "" }])}
      >
        <Plus className="size-3.5" />
        Add a server
      </Button>
      <FormNote>
        Health checking is passive: after max fails of a server&apos;s real requests fail within its
        fail timeout, nginx sends it nothing for the fail timeout, then tries it again. Nothing
        probes the servers in between.
        {noBackup && " A backup server needs round robin or least connections."}
      </FormNote>
      <Field
        label="Retry on the next server after"
        hint="Nothing ticked never retries. nginx does not retry a POST, PUT or DELETE unless told to, since the first server may have acted on it."
      >
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          {RETRY_CONDITIONS.map((c) => (
            <label
              key={c.value}
              className="flex items-center gap-2 text-hint text-muted-foreground"
            >
              <Checkbox
                checked={retry.includes(c.value)}
                onCheckedChange={(v) => toggleRetry(c.value, Boolean(v))}
              />
              {c.label}
            </label>
          ))}
        </div>
      </Field>
      <FieldRow>
        <Field
          label="Tries"
          htmlFor="site-pool-tries"
          hint="Servers one request may try, the first included. Empty is no cap."
        >
          <Input
            id="site-pool-tries"
            value={pool.tries ? String(pool.tries) : ""}
            inputMode="numeric"
            placeholder="no cap"
            onChange={(e) => set("tries", count(e.target.value))}
            className="font-mono text-xs"
          />
        </Field>
        <Field
          label="Kept-open connections"
          htmlFor="site-pool-keepalive"
          hint="Idle connections each nginx worker keeps to the servers. Empty opens one per request."
        >
          <Input
            id="site-pool-keepalive"
            value={pool.keepalive ? String(pool.keepalive) : ""}
            inputMode="numeric"
            placeholder="32"
            onChange={(e) => set("keepalive", count(e.target.value))}
            className="font-mono text-xs"
          />
        </Field>
      </FieldRow>
    </div>
  )
}

/** Said under a path's upstream when nothing listens behind it. */
function IdleNote({
  upstream,
  listeners,
  containers,
}: {
  upstream: string
  listeners: Listener[] | undefined
  containers: Container[] | undefined
}) {
  const idle = nothingListening(upstream, listeners, containers)
  return idle ? <FormNote tone="warning">{idle}</FormNote> : null
}

/** A rate as its number and its unit, the two halves nginx writes as 10r/s. */
function splitRate(rate: string): [string, "s" | "m"] {
  const m = /^(\d*)r\/([sm])$/.exec(rate)
  return m ? [m[1], m[2] as "s" | "m"] : [rate, "s"]
}

/** A request rate, its burst, and what it counts by. */
function RequestLimitFields({
  id,
  value,
  onChange,
}: {
  id: string
  value: RequestLimit
  onChange: (value: RequestLimit) => void
}) {
  const [count, unit] = splitRate(value.rate)
  const patch = (next: Partial<RequestLimit>) => onChange({ ...value, ...next })
  return (
    <div className="space-y-2">
      <FieldRow>
        <Field label="Requests" htmlFor={`${id}-rate`} hint="Per client, at a steady pace.">
          <div className="flex gap-2">
            <Input
              id={`${id}-rate`}
              value={count}
              inputMode="numeric"
              onChange={(e) => patch({ rate: `${e.target.value.trim()}r/${unit}` })}
              className="font-mono text-xs"
            />
            <Select value={unit} onValueChange={(u) => patch({ rate: `${count}r/${u}` })}>
              <SelectTrigger aria-label="Per" className="w-36 shrink-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="s">per second</SelectItem>
                <SelectItem value="m">per minute</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </Field>
        <Field
          label="Burst"
          htmlFor={`${id}-burst`}
          hint="Requests past the rate that wait their turn instead of getting a 429."
        >
          <Input
            id={`${id}-burst`}
            value={String(value.burst ?? 0)}
            inputMode="numeric"
            onChange={(e) => patch({ burst: Number(e.target.value) || 0 })}
            className="font-mono text-xs"
          />
        </Field>
      </FieldRow>
      <Field label="Counted" htmlFor={`${id}-key`}>
        <Select
          value={value.key ?? "ip"}
          onValueChange={(k) => patch({ key: k === "ip_path" ? "ip_path" : undefined })}
        >
          <SelectTrigger id={`${id}-key`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="ip">Per address</SelectItem>
            <SelectItem value="ip_path">Per address and path</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <label className="flex items-center gap-2 text-hint text-muted-foreground">
        <Checkbox
          checked={value.noDelay ?? false}
          onCheckedChange={(v) => patch({ noDelay: Boolean(v) })}
        />
        Answer the burst at once rather than spacing it out
      </label>
    </div>
  )
}

const REAL_IP_HEADERS = ["X-Forwarded-For", "X-Real-IP"]

/**
 * Where the visitor's address comes from behind Cloudflare or a load
 * balancer. Cloudflare's ranges are one shared file every such site
 * includes, so the refresh here updates all of them at once.
 */
function RealIPSection({
  spec,
  set,
  open,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
  open: boolean
}) {
  const realIp = spec.realIp
  const cloudflare = realIp?.source === "cloudflare"
  const ranges = usePoll<CloudflareRanges>(
    (signal) => get("/proxy/realip/cloudflare", undefined, signal),
    0,
    [],
    { enabled: open && cloudflare },
  )
  const [refreshing, setRefreshing] = useState(false)
  const refresh = async () => {
    setRefreshing(true)
    try {
      const res = await post<CloudflareRefresh>("/proxy/realip/cloudflare/refresh")
      if (res.reloadError) {
        notify.error("Ranges saved, but nginx did not reload", res.reloadError)
      } else {
        notify.success(`${plural(res.ranges.ranges.length, "Cloudflare range")} in use`)
      }
      ranges.refresh()
    } catch (err) {
      notify.error("Could not refresh Cloudflare's ranges", err)
    } finally {
      setRefreshing(false)
    }
  }
  const header = realIp?.header ?? "X-Forwarded-For"
  const rangeNote = (() => {
    const data = ranges.data
    if (!data) return null
    if (!data.source) return "Written from the built-in list on save, until the first refresh."
    const count = plural(data.ranges.length, "range")
    if (data.source === "built-in") {
      return `${count} from the built-in list, written ${relativeTime(data.fetched)}.`
    }
    return `${count} from cloudflare.com, fetched ${relativeTime(data.fetched)}.`
  })()

  return (
    <FormSection
      title="Visitor address"
      hint="Behind Cloudflare or a load balancer every request comes from the proxy. This puts the visitor's address back for the log, the address lists and the limits."
    >
      <OptionList>
        <OptionRow
          title="Behind a proxy"
          hint="The address is taken from a header, believed only from the proxy's own addresses."
          checked={Boolean(realIp)}
          onCheckedChange={(on) => set("realIp", on ? { source: "cloudflare" } : undefined)}
        >
          {realIp && (
            <ToggleGroup
              type="single"
              aria-label="Which proxy"
              value={realIp.source}
              onValueChange={(v) => {
                if (v === "cloudflare") set("realIp", { source: "cloudflare" })
                if (v === "proxies")
                  set("realIp", { source: "proxies", trusted: [], header: "X-Forwarded-For" })
              }}
              variant="outline"
              size="sm"
              className="w-full"
            >
              <ToggleGroupItem value="cloudflare" className="flex-1 text-hint">
                Cloudflare
              </ToggleGroupItem>
              <ToggleGroupItem value="proxies" className="flex-1 text-hint">
                My own proxies
              </ToggleGroupItem>
            </ToggleGroup>
          )}
        </OptionRow>
        {cloudflare && (
          <OptionRow
            title="Only Cloudflare may reach this site"
            hint="Closes every connection from outside Cloudflare's ranges without an answer. The domain has to be proxied through Cloudflare, certificate checks included."
            tone={realIp.cloudflareOnly ? "warning" : "default"}
            checked={realIp.cloudflareOnly ?? false}
            onCheckedChange={(cloudflareOnly) =>
              set("realIp", { source: "cloudflare", cloudflareOnly })
            }
          />
        )}
      </OptionList>
      {cloudflare && (
        <div className="flex items-center justify-between gap-3">
          <p className="text-hint text-muted-foreground">
            {rangeNote ?? "Reading Cloudflare's ranges…"}
          </p>
          <Button variant="outline" size="sm" disabled={refreshing} onClick={refresh}>
            {refreshing ? "Refreshing…" : "Refresh ranges"}
          </Button>
        </div>
      )}
      {realIp?.source === "proxies" && (
        <>
          <ListField
            id="site-realip-trusted"
            label="Believe the header from"
            placeholder="10.0.0.2"
            values={realIp.trusted ?? []}
            onChange={(trusted) => set("realIp", { ...realIp, trusted })}
            hint="The load balancer's addresses. The header from anyone else is ignored."
          />
          <Field
            label="Address header"
            htmlFor="site-realip-header"
            hint="With X-Forwarded-For the visitor is the last address that is not one of yours."
          >
            <Select value={header} onValueChange={(v) => set("realIp", { ...realIp, header: v })}>
              <SelectTrigger id="site-realip-header" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {[...new Set([...REAL_IP_HEADERS, header])].map((h) => (
                  <SelectItem key={h} value={h}>
                    {h}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </>
      )}
    </FormSection>
  )
}

/**
 * Request rates and a connection cap, counted per client address. The
 * exemptions and log-only mode apply to every limit of the site, a path's own
 * included, so they are drawn whenever any of them is on.
 */
function LimitsSection({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const limits = spec.limits
  const setLimits = (patch: Partial<SiteLimits>) =>
    set("limits", { exemptFrom: [], ...limits, ...patch })
  const pathLimited =
    spec.kind === "proxy" && spec.locations.some((loc) => loc.rateLimit !== undefined)
  const limited = Boolean(limits?.request) || (limits?.connPerIp ?? 0) > 0 || pathLimited

  return (
    <FormSection
      title="Limits"
      hint="How much one client address may ask of the site. Past a limit it gets a 429."
    >
      <OptionList>
        <OptionRow
          title="Limit the request rate"
          hint="Slows scrapers and password guessing. A path below can have a rate of its own."
          checked={Boolean(limits?.request)}
          onCheckedChange={(on) =>
            setLimits({ request: on ? { rate: "10r/s", burst: 20, noDelay: true } : undefined })
          }
        >
          {limits?.request && (
            <RequestLimitFields
              id="site-limit"
              value={limits.request}
              onChange={(request) => setLimits({ request })}
            />
          )}
        </OptionRow>
        <OptionRow
          title="Cap connections per address"
          hint="How many connections one address may hold open at once."
          checked={(limits?.connPerIp ?? 0) > 0}
          onCheckedChange={(on) => setLimits({ connPerIp: on ? 20 : 0 })}
        >
          {(limits?.connPerIp ?? 0) > 0 && (
            <Input
              aria-label="Connections per address"
              value={String(limits?.connPerIp ?? 0)}
              inputMode="numeric"
              onChange={(e) => setLimits({ connPerIp: Number(e.target.value) || 0 })}
              className="font-mono text-xs"
            />
          )}
        </OptionRow>
        {limited && (
          <OptionRow
            title="Log only"
            hint='Refuses nothing: what would have been refused is written to the error log as "dry run". For trying a limit on live traffic.'
            tone={limits?.dryRun ? "warning" : "default"}
            checked={limits?.dryRun ?? false}
            onCheckedChange={(dryRun) => setLimits({ dryRun })}
          />
        )}
      </OptionList>
      {limited && (
        <ListField
          id="site-limit-exempt"
          label="Never limit these addresses"
          placeholder="203.0.113.7"
          values={limits?.exemptFrom ?? []}
          onChange={(exemptFrom) => setLimits({ exemptFrom })}
          hint="A monitor, an office, another server of yours."
        />
      )}
    </FormSection>
  )
}

/**
 * What browsers and nginx keep. The proxy cache stores nothing nginx does not
 * buffer, so turning it on turns buffering on with it.
 */
function CachingSection({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const asset = spec.staticCache
  const proxy = spec.kind === "proxy" ? spec.proxyCache : undefined
  const setAsset = (patch: Partial<StaticCache>) =>
    set("staticCache", { maxAge: "30d", ...asset, ...patch })
  const setProxy = (patch: Partial<ProxyCache>) =>
    set("proxyCache", { maxSize: "1g", valid: "10m", ...proxy, ...patch })

  return (
    <FormSection
      title="Caching"
      hint="Browsers keep static files, and nginx can keep the application's answers."
    >
      <OptionList>
        <OptionRow
          title="Cache static files in browsers"
          hint="Styles, scripts, images and fonts, chosen by their Content-Type, so pages are never kept."
          checked={Boolean(asset)}
          onCheckedChange={(on) => set("staticCache", on ? { maxAge: "30d" } : undefined)}
        >
          {asset && (
            <div className="space-y-2">
              <Field
                label="Kept for"
                htmlFor="site-cache-age"
                hint="A time in nginx's units: 12h, 30d, 1y."
              >
                <Input
                  id="site-cache-age"
                  value={asset.maxAge}
                  onChange={(e) => setAsset({ maxAge: e.target.value.trim() })}
                  placeholder="30d"
                  className="font-mono text-xs"
                />
              </Field>
              <label className="flex items-center gap-2 text-hint text-muted-foreground">
                <Checkbox
                  checked={asset.immutable ?? false}
                  onCheckedChange={(v) => setAsset({ immutable: Boolean(v) })}
                />
                Immutable: browsers do not ask again even on a reload. Only for file names that
                change with their content.
              </label>
            </div>
          )}
        </OptionRow>
        {spec.kind === "proxy" && (
          <OptionRow
            title="Cache the application's responses"
            hint="nginx answers repeat requests from disk and adds an X-Cache-Status header. Requests with an Authorization header always reach the application."
            checked={Boolean(proxy)}
            onCheckedChange={(on) => {
              if (on) set("buffering", true)
              set("proxyCache", on ? { maxSize: "1g", valid: "10m", serveStale: true } : undefined)
            }}
          >
            {proxy && (
              <div className="space-y-2">
                <FieldRow>
                  <Field label="Size on disk" htmlFor="site-cache-size" hint="512m, 2g.">
                    <Input
                      id="site-cache-size"
                      value={proxy.maxSize}
                      onChange={(e) => setProxy({ maxSize: e.target.value.trim() })}
                      placeholder="1g"
                      className="font-mono text-xs"
                    />
                  </Field>
                  <Field
                    label="Kept for"
                    htmlFor="site-cache-valid"
                    hint="When the application's own Cache-Control does not say."
                  >
                    <Input
                      id="site-cache-valid"
                      value={proxy.valid}
                      onChange={(e) => setProxy({ valid: e.target.value.trim() })}
                      placeholder="10m"
                      className="font-mono text-xs"
                    />
                  </Field>
                </FieldRow>
                <label className="flex items-center gap-2 text-hint text-muted-foreground">
                  <Checkbox
                    checked={proxy.serveStale ?? false}
                    onCheckedChange={(v) => setProxy({ serveStale: Boolean(v) })}
                  />
                  Serve the last copy while the application is down
                </label>
                <label className="flex items-center gap-2 text-hint text-muted-foreground">
                  <Checkbox
                    checked={proxy.cacheCookies ?? false}
                    onCheckedChange={(v) => setProxy({ cacheCookies: Boolean(v) })}
                  />
                  Cache requests with cookies too
                </label>
                {!spec.buffering && (
                  <FormNote tone="warning">
                    The cache needs Buffer responses on, under Behaviour.
                  </FormNote>
                )}
              </div>
            )}
          </OptionRow>
        )}
      </OptionList>
    </FormSection>
  )
}

const CORS_METHODS: CORSMethod[] = ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"]

const CSP_KEYWORDS = [
  "self",
  "none",
  "unsafe-inline",
  "unsafe-eval",
  "unsafe-hashes",
  "strict-dynamic",
  "report-sample",
  "wasm-unsafe-eval",
  "inline-speculation-rules",
]

const CSP_PRESET = "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'"

/** The features the form offers; one read from the file is listed too. */
const PERMISSION_FEATURES = ["camera", "microphone", "geolocation", "payment", "usb", "fullscreen"]

const PERMISSION_LABEL: Record<PermissionRule["allow"], string> = {
  none: "Nobody",
  self: "This site",
  all: "Everyone",
}

function cspQuoted(source: string): boolean {
  return CSP_KEYWORDS.includes(source) || /^(nonce|sha256|sha384|sha512)-/.test(source)
}

/** The policy as it is written in the header, keywords in their quotes. */
function formatCSP(csp: SiteCSP): string {
  return csp.directives
    .map((d) => [d.name, ...(d.sources ?? []).map((s) => (cspQuoted(s) ? `'${s}'` : s))].join(" "))
    .join("; ")
}

/**
 * The typed policy as directives. A keyword's quotes come off, since the
 * renderer writes them; anything else quoted is kept as typed, so the save
 * says what is wrong with it.
 */
function parseCSP(text: string): CSPDirective[] {
  return text
    .split(";")
    .map((part) => part.trim().split(/\s+/).filter(Boolean))
    .filter((words) => words.length > 0)
    .map(([name, ...sources]) => ({
      name: name.toLowerCase(),
      sources: sources.map((s) => {
        const bare = s.replace(/^'(.*)'$/, "$1")
        return cspQuoted(bare) ? bare : s
      }),
    }))
}

/** Drops a headers section that sets nothing, so the spec says none. */
function emptyHeaders(h: SiteHeaders): boolean {
  return (
    !h.request?.length &&
    !h.response?.length &&
    !h.hide?.length &&
    !h.frameOptions &&
    !h.csp &&
    !h.permissions?.length &&
    !h.cors
  )
}

/**
 * Headers added to answers and to what the application is sent, and the
 * browser policies: CORS for an API, a CSP that can be tried report-only
 * first, Permissions-Policy and X-Frame-Options.
 */
function HeadersSection({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const h = spec.headers ?? {}
  const proxy = spec.kind === "proxy"
  const setHeaders = (patch: Partial<SiteHeaders>) => {
    const next = { ...h, ...patch }
    set("headers", emptyHeaders(next) ? undefined : next)
  }
  const cors = h.cors
  const setCors = (patch: Partial<SiteCORS>) =>
    setHeaders({ cors: { origins: [], methods: ["GET", "POST"], ...cors, ...patch } })
  const [cspText, setCspText] = useState(() => (h.csp ? formatCSP(h.csp) : ""))
  const permissions = h.permissions ?? []
  const features = [
    ...new Set([...PERMISSION_FEATURES, ...permissions.map((rule) => rule.feature)]),
  ]
  const setPermission = (feature: string, allow: PermissionRule["allow"] | "unset") => {
    const rest = permissions.filter((rule) => rule.feature !== feature)
    setHeaders({ permissions: allow === "unset" ? rest : [...rest, { feature, allow }] })
  }

  return (
    <FormSection
      title="Headers"
      hint="What every answer carries, what the application is sent, and the policies browsers follow."
    >
      <OptionList>
        {spec.kind !== "redirect" && (
          <OptionRow
            title="CORS for an API"
            hint="Pages on the origins below may call this site from a browser. A preflight is answered by nginx with 204; other origins get no Access-Control-Allow-Origin."
            checked={Boolean(cors)}
            onCheckedChange={(on) =>
              setHeaders({
                cors: on
                  ? { origins: [], methods: ["GET", "POST", "PUT", "PATCH", "DELETE"] }
                  : undefined,
              })
            }
          >
            {cors && (
              <div className="space-y-2">
                <ListField
                  id="site-cors-origins"
                  label="Allowed origins"
                  placeholder="https://app.example.com"
                  values={cors.origins}
                  onChange={(origins) => setCors({ origins })}
                  hint="Scheme and host, with a port if not the default, and no path. * alone allows any origin, without credentials."
                />
                <Field label="Methods">
                  <div className="flex flex-wrap gap-x-4 gap-y-2">
                    {CORS_METHODS.map((method) => (
                      <label
                        key={method}
                        className="flex items-center gap-2 font-mono text-hint text-muted-foreground"
                      >
                        <Checkbox
                          checked={cors.methods.includes(method)}
                          onCheckedChange={(v) =>
                            setCors({
                              methods: v
                                ? CORS_METHODS.filter(
                                    (m) => m === method || cors.methods.includes(m),
                                  )
                                : cors.methods.filter((m) => m !== method),
                            })
                          }
                        />
                        {method}
                      </label>
                    ))}
                  </div>
                </Field>
                <ListField
                  id="site-cors-headers"
                  label="Allowed request headers"
                  placeholder="Content-Type"
                  values={cors.headers ?? []}
                  onChange={(headers) => setCors({ headers })}
                  hint="Empty allows whichever headers the browser asks for."
                />
                <label className="flex items-center gap-2 text-hint text-muted-foreground">
                  <Checkbox
                    checked={cors.credentials ?? false}
                    onCheckedChange={(v) => setCors({ credentials: Boolean(v) })}
                  />
                  Allow credentials: the browser sends cookies and HTTP auth along
                </label>
              </div>
            )}
          </OptionRow>
        )}
        <OptionRow
          title="Content-Security-Policy"
          hint="Which scripts, styles, frames and connections the site's pages may use. Try it report-only first: browsers then report what it would block and block nothing."
          checked={Boolean(h.csp)}
          onCheckedChange={(on) => {
            setCspText(on ? CSP_PRESET : "")
            setHeaders({
              csp: on ? { directives: parseCSP(CSP_PRESET), reportOnly: true } : undefined,
            })
          }}
        >
          {h.csp && (
            <div className="space-y-2">
              <Textarea
                aria-label="Content-Security-Policy"
                value={cspText}
                onChange={(e) => {
                  setCspText(e.target.value)
                  setHeaders({ csp: { ...h.csp, directives: parseCSP(e.target.value) } })
                }}
                rows={3}
                className="font-mono text-xs"
              />
              <label className="flex items-center gap-2 text-hint text-muted-foreground">
                <Checkbox
                  checked={h.csp.reportOnly ?? false}
                  onCheckedChange={(v) =>
                    h.csp && setHeaders({ csp: { ...h.csp, reportOnly: Boolean(v) } })
                  }
                />
                Report only: send it as Content-Security-Policy-Report-Only
              </label>
            </div>
          )}
        </OptionRow>
        <OptionRow
          title="Permissions-Policy"
          hint="Which browser features the site's pages, and the frames in them, may ask for."
          checked={permissions.length > 0}
          onCheckedChange={(on) =>
            setHeaders({
              permissions: on
                ? ["camera", "microphone", "geolocation", "payment"].map((feature) => ({
                    feature,
                    allow: "none" as const,
                  }))
                : [],
            })
          }
        >
          {permissions.length > 0 && (
            <div className="grid gap-2 sm:grid-cols-2">
              {features.map((feature) => (
                <Field key={feature} label={feature} htmlFor={`site-permission-${feature}`}>
                  <Select
                    value={permissions.find((rule) => rule.feature === feature)?.allow ?? "unset"}
                    onValueChange={(v) =>
                      setPermission(feature, v as PermissionRule["allow"] | "unset")
                    }
                  >
                    <SelectTrigger id={`site-permission-${feature}`} className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="unset">Browser default</SelectItem>
                      {(["none", "self", "all"] as const).map((allow) => (
                        <SelectItem key={allow} value={allow}>
                          {PERMISSION_LABEL[allow]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              ))}
            </div>
          )}
        </OptionRow>
      </OptionList>
      <Field
        label="X-Frame-Options"
        htmlFor="site-frame-options"
        hint="Whether other sites may show this one in a frame. A CSP's frame-ancestors, where set, is what current browsers follow."
      >
        <Select
          value={h.frameOptions || "unset"}
          onValueChange={(v) =>
            setHeaders({ frameOptions: v === "unset" ? undefined : (v as "deny" | "sameorigin") })
          }
        >
          <SelectTrigger id="site-frame-options" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="unset">
              {spec.securityHeaders ? "SAMEORIGIN, from Security headers" : "Not sent"}
            </SelectItem>
            <SelectItem value="sameorigin">SAMEORIGIN: only this site</SelectItem>
            <SelectItem value="deny">DENY: never in a frame</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <HeaderListField
        id="site-response-headers"
        label="Response headers"
        values={h.response ?? []}
        onChange={(response) => setHeaders({ response })}
        hint={
          proxy
            ? "Added to every answer. nginx adds a header beside the application's own, so hide the application's below if it sends the same one."
            : "Added to every answer, errors included."
        }
      />
      {proxy && (
        <>
          <HeaderListField
            id="site-request-headers"
            label="Request headers to the application"
            values={h.request ?? []}
            onChange={(request) => setHeaders({ request })}
            hint="Plain text, or one nginx variable such as $ssl_client_s_dn. An empty value stops the visitor's header of that name reaching the application."
          />
          <ListField
            id="site-hide-headers"
            label="Hide from the application's responses"
            placeholder="X-Powered-By"
            values={h.hide ?? []}
            onChange={(hide) => setHeaders({ hide })}
            hint="nginx already drops Server and Date. With CORS on, the application's own CORS headers are hidden too."
          />
        </>
      )}
    </FormSection>
  )
}

/** Name and value pairs, added one at a time. */
function HeaderListField({
  id,
  label,
  values,
  onChange,
  hint,
}: {
  id: string
  label: string
  values: HeaderValue[]
  onChange: (values: HeaderValue[]) => void
  hint?: string
}) {
  const [name, setName] = useState("")
  const [value, setValue] = useState("")
  const add = () => {
    if (!name.trim()) return
    onChange([...values, { name: name.trim(), value: value.trim() }])
    setName("")
    setValue("")
  }
  return (
    <Field label={label} htmlFor={id} hint={hint}>
      <div className="space-y-2">
        {values.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            {values.map((header, i) => (
              <Tag key={`${header.name}-${i}`} mono className="gap-1.5 pr-0.5">
                {header.name}: {header.value || "(empty)"}
                <IconAction
                  label={`Remove ${header.name}`}
                  size="icon-xs"
                  className="size-4 text-muted-foreground hover:text-destructive [&_svg:not([class*='size-'])]:size-2.5"
                  onClick={() => onChange(values.filter((_, j) => j !== i))}
                >
                  <Trash />
                </IconAction>
              </Tag>
            ))}
          </div>
        )}
        <div className="flex gap-2">
          <Input
            id={id}
            aria-label={`${label}: name`}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="X-Robots-Tag"
            className="font-mono text-xs"
          />
          <Input
            aria-label={`${label}: value`}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault()
                add()
              }
            }}
            placeholder="noindex"
            className="font-mono text-xs"
          />
          <Button
            size="sm"
            variant="outline"
            onClick={add}
            disabled={!name.trim()}
            aria-label={`Add to ${label.toLowerCase()}`}
          >
            <Plus className="size-3.5" />
          </Button>
        </div>
      </div>
    </Field>
  )
}

const ERROR_CODES: ErrorPageCode[] = [404, 502, 503, 504]

const ERROR_HINT: Record<ErrorPageCode, string> = {
  404: "A path with nothing behind it.",
  502: "The application is down or starting. The page shipped for it retries every 5 seconds.",
  503: "The application says it cannot take requests.",
  504: "The application took longer than the timeout to answer.",
}

/**
 * The maintenance switch and the error pages. The pages themselves are files
 * beside the site's configuration, edited once the site exists; until then a
 * save gives it the ones the dashboard ships.
 */
function PagesSection({
  spec,
  set,
  site,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
  /** The saved site's name, when there is one to hold page files. */
  site: string | null
}) {
  const [editingPage, setEditingPage] = useState<SitePageName | null>(null)
  const [page, setPage] = useState<SitePageName>("maintenance")
  const [finding, setFinding] = useState(false)
  const maintenance = spec.maintenance
  const codes = spec.errorPages ?? []
  const setMaintenance = (patch: Partial<SiteMaintenance>) =>
    set("maintenance", { on: false, retryAfter: 300, bypassFrom: [], ...maintenance, ...patch })
  const addMyAddress = async () => {
    setFinding(true)
    try {
      const { client } = await get<Exposure>("/exposure")
      if (!client) {
        notify.error("The dashboard could not tell which address you come from")
        return
      }
      const bypass = maintenance?.bypassFrom ?? []
      if (!bypass.includes(client)) setMaintenance({ bypassFrom: [...bypass, client] })
    } catch (err) {
      notify.error("Could not read your address", err)
    } finally {
      setFinding(false)
    }
  }
  const pages: SitePageName[] = [
    ...(maintenance ? (["maintenance"] as const) : []),
    ...codes.map((code) => String(code) as SitePageName),
  ]
  const edit = (name: SitePageName) => {
    setPage(name)
    setEditingPage(name)
  }

  return (
    <FormSection title="Maintenance & error pages">
      <OptionList>
        <OptionRow
          title="Maintenance"
          hint="Answers every visitor with the maintenance page and a 503, except the addresses below. Certificate renewals keep working."
          tone={maintenance?.on ? "warning" : "default"}
          checked={maintenance?.on ?? false}
          onCheckedChange={(on) => setMaintenance({ on })}
        />
      </OptionList>
      {maintenance && (
        <>
          <Field
            label="Retry after"
            htmlFor="site-retry-after"
            hint="Seconds clients and crawlers are told to wait. 0 sends no Retry-After."
          >
            <Input
              id="site-retry-after"
              value={String(maintenance.retryAfter ?? 0)}
              inputMode="numeric"
              onChange={(e) => setMaintenance({ retryAfter: Number(e.target.value) || 0 })}
              className="font-mono text-xs"
            />
          </Field>
          <ListField
            id="site-bypass"
            label="Let these addresses past"
            placeholder="203.0.113.7"
            values={maintenance.bypassFrom}
            onChange={(bypassFrom) => setMaintenance({ bypassFrom })}
            hint="They reach the site as usual while maintenance is on."
          />
          <div>
            <Button size="sm" variant="outline" onClick={addMyAddress} pending={finding}>
              Add my address
            </Button>
            <FormNote className="mt-1.5">
              The address this browser reaches the dashboard from, which is the one the site sees
              only when both are reached the same way.
            </FormNote>
          </div>
        </>
      )}
      <OptionList>
        {ERROR_CODES.map((code) => (
          <OptionRow
            key={code}
            title={`Own ${PAGE_LABEL[String(code) as SitePageName]} page`}
            hint={ERROR_HINT[code]}
            checked={codes.includes(code)}
            onCheckedChange={(on) =>
              set(
                "errorPages",
                ERROR_CODES.filter((c) => (c === code ? on : codes.includes(c))),
              )
            }
          />
        ))}
        {spec.kind === "proxy" && codes.length > 0 && (
          <OptionRow
            title="Replace the application's own error pages"
            hint="Without it only the errors nginx produces itself get these pages; with it, the application's responses with those codes do too."
            checked={spec.interceptErrors ?? false}
            onCheckedChange={(v) => set("interceptErrors", v)}
          />
        )}
      </OptionList>
      {pages.length > 0 &&
        (site ? (
          <div className="flex flex-wrap gap-2">
            {pages.map((name) => (
              <Button key={name} size="sm" variant="outline" onClick={() => edit(name)}>
                Edit {PAGE_LABEL[name]}
              </Button>
            ))}
          </div>
        ) : (
          <FormNote>
            A save gives the site the pages the dashboard ships. Edit them here once it is saved.
          </FormNote>
        ))}
      {site && (
        <PageEditor
          key={`${site}:${page}`}
          open={editingPage !== null}
          onOpenChange={(open) => !open && setEditingPage(null)}
          site={site}
          page={page}
        />
      )}
    </FormSection>
  )
}
