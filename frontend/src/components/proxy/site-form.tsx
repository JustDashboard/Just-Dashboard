"use client"

import { useCallback, useEffect, useEffectEvent, useMemo, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { ArrowRight, Code, FolderOpen, Globe, Warning, type Icon } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type {
  AuthFile,
  Certificate,
  Container,
  DroppedLine,
  Listener,
  SitePreflight,
  SitePreview,
  SiteRead,
  SiteResult,
  SiteSpec,
} from "@/lib/types"
import { plural } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceCard, ChoiceGrid, ProductCard } from "@/components/choice-card"
import { ProductLogo } from "@/components/product-logo"
import { CodeEditor } from "@/components/code-editor"
import { DiffView } from "@/components/diff-view"
import { FieldRow, FormNote, FormSections, OptionList, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { Pane, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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
import { changedFields, changedViews } from "@/components/proxy/site-sections"
import {
  DiscardDialog,
  SettingsParts,
  SiteSaveBar,
  UnsavedStrip,
  useLeaveGuard,
  type PartMark,
} from "@/components/proxy/site-settings"
import { siteSettingsHref, type SettingsView } from "@/components/proxy/site-tabs"
import { SettingSection } from "@/components/settings/setting-card"
import {
  applyPreset,
  BLANK,
  leavePreset,
  presetById,
  PRESETS,
} from "@/components/proxy/site-presets"
import { saveOutcome, saveRequest, sendableSpec } from "@/components/proxy/site-save"
import { SiteCertificate } from "@/components/proxy/site-certificate"
import { nothingListening, upstreamOptions } from "@/components/proxy/upstream-options"
import { UpstreamPicker } from "@/components/proxy/upstream-picker"
import { PageLayout, Field, Part } from "@/components/proxy/site-form/fields"
import { LocationsField } from "@/components/proxy/site-form/locations"
import { UpstreamAdvanced, poolFrom, PoolField } from "@/components/proxy/site-form/upstream"
import { RealIPSection, LimitsSection, CachingSection } from "@/components/proxy/site-form/limits"
import { HeadersSection } from "@/components/proxy/site-form/headers"
import { AccessSection } from "@/components/proxy/site-form/access"
import { CrawlerSection, PagesSection } from "@/components/proxy/site-form/pages"
import { HSTSOptions } from "@/components/proxy/site-form/encryption"
import {
  REDIRECT_CODES,
  redirectCode,
  IndexFilesField,
  RoutesTable,
} from "@/components/proxy/site-form/serving"
import { CHECK_TONE, DomainChecks } from "@/components/proxy/site-form/review"

// What other files import from the form, kept importable from here.
export {
  HSTS_MAX_AGES,
  HSTS_DEFAULT_MAX_AGE,
  HSTS_PRELOAD_MAX_AGE,
} from "@/components/proxy/site-form/hsts"
export { HeaderListField } from "@/components/proxy/site-form/headers"

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
 *
 * It is drawn in two places. A new site, or a copy of one, is made in a
 * sheet over the Sites list (`SiteForm`): a creation is one task read top to
 * bottom with the file it will write beside it. An existing site is edited
 * on its own page, as its Settings view (`SiteSettings`): the same parts in
 * one centred column, split across the views a deployment's settings are
 * split across, with one floating bar that holds the saves. Both are
 * `SiteFormBody`, so a field, a check and the request a save sends are
 * written once.
 *
 * Its parts are in `site-form/`, a file each, so one section can be changed
 * and reviewed without the whole form: the fields every part draws through
 * (`fields.tsx`), the sections (paths, upstream, limits, headers, access,
 * pages, encryption, serving) and the checks (`review.tsx`). This file keeps
 * the body, the two places it is drawn, and the names other pages import.
 */

/**
 * Asks before a site's unsaved Settings are left. The page that holds the
 * form calls it with what leaving is — another view, another site — and that
 * runs at once when nothing is unsaved, or once the reader lets the edits go.
 */
export type LeaveGuard = (proceed: () => void) => void

/**
 * An existing site's Settings, as a view of its page: the form's parts in
 * one centred column, the part on screen named in the address, and the saves
 * on one floating bar.
 */
export function SiteSettings({
  name,
  view,
  guardRef,
  onSaved,
}: {
  name: string
  /** The part of the form on screen. */
  view: SettingsView
  /** Holds the form's question about unsaved changes while it is on screen. */
  guardRef?: React.RefObject<LeaveGuard | null>
  /** Told whether nginx was reloaded with the save. */
  onSaved: (reloaded: boolean) => void
}) {
  return (
    <SiteFormBody
      key={name}
      layout="page"
      view={view}
      guardRef={guardRef}
      open
      editing={name}
      copyFrom={null}
      onOpenChange={noop}
      onSaved={onSaved}
    />
  )
}

const noop = () => {}

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
  /** Told whether nginx was reloaded with the save. */
  onSaved: (reloaded: boolean) => void
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
  php: Code,
}

function SiteFormBody({
  layout = "sheet",
  view = "general",
  guardRef,
  open,
  editing,
  copyFrom,
  onOpenChange,
  onSaved,
}: {
  /** The sheet a new site is made in, or the Settings view of a site's page. */
  layout?: "sheet" | "page"
  /** The part of Settings on screen; the sheet draws every part. */
  view?: SettingsView
  guardRef?: React.RefObject<LeaveGuard | null>
  open: boolean
  editing: string | null
  copyFrom: string | null
  onOpenChange: (open: boolean) => void
  /** Told whether nginx was reloaded with the save. */
  onSaved: (reloaded: boolean) => void
}) {
  const page = layout === "page"
  const router = useRouter()
  const source = editing ?? copyFrom
  // Kept for the tab while the form is open (the panel forgets it on close):
  // a site is a long form, and a look at a port or a certificate half-way
  // through it should not mean typing it again. An existing site's draft
  // outlives a trip away only while its file is still the version the draft
  // started from; the server's copy used to replace it on every open.
  // A site's Settings keep theirs under a key of their own: the sheet forgets
  // every sheet draft when it closes, and a copy of a site starts from that
  // site's key, neither of which may take an edit under way on its page.
  const draft = page
    ? `proxy.site.settings.${editing}`
    : source
      ? `proxy.site.form.${source}`
      : NEW_SITE_DRAFT
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
  // Where the reader was going when Settings held unsaved changes: asked
  // about first, and gone to once they let the edits go.
  const [leaving, setLeaving] = useState<{ go: () => void } | null>(null)
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
  const phpSockets = usePoll<string[]>(
    (signal) => get("/proxy/php-sockets", undefined, signal),
    30_000,
    [],
    { enabled: open && spec.kind === "php" },
  )
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

  // What follows a save: the sheet closes; Settings stay where they are and
  // read the file again, so the fields are what is on disk now and nothing
  // is counted unsaved against the version before.
  const settle = () => {
    if (!page) {
      onOpenChange(false)
      return
    }
    setBase(null)
    setConflict(null)
    setReads((n) => n + 1)
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
      onSaved(res.reloaded)
      settle()
    } catch (err) {
      if (err instanceof ApiError && err.code === "name_conflict") {
        setConflict({ message: err.message, mode, spec })
      } else if (err instanceof ApiError && err.code === "site_changed") {
        // Somebody saved the file after the form read it. Nothing was
        // written: the file is read again and the operator asked which of
        // the two wins, rather than their change being written over.
        refused.current = true
        setReads((n) => n + 1)
      } else if (err instanceof ApiError && err.code === "load_refused") {
        // nginx's master refused the file at the reload — a port another
        // program holds — and the site was put back; the draft stays here.
        notify.error("Not applied: nginx could not take it up", err)
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
  // The fields as the file has them again: what Discard means on a page,
  // where there is no sheet to close.
  const resetDraft = () => {
    if (!base) return
    setSpec(base.spec)
    setDomainText(base.spec.domains.join(" "))
    setFixed(fixedFor(base.spec, true))
    setConflict(null)
    setOverridden(null)
  }
  // Settings are left by a link, by another view of the site's page or by
  // the next site in its rail. Each is asked about while there are unsaved
  // changes, which is what closing the sheet asked. A trip the form itself
  // sends the reader on keeps the draft for the tab, as it always did.
  const askToLeave: LeaveGuard = (go) => {
    if (busy) return
    if (dirty) setLeaving({ go })
    else go()
  }
  useLeaveGuard(page && dirty, (href) => askToLeave(() => router.push(href)))
  useEffect(() => {
    if (!guardRef || !page) return
    guardRef.current = askToLeave
    return () => {
      guardRef.current = null
    }
  })

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
  const heldNote = !held ? null : page && editing ? (
    <>
      {plural(failed, "check")} failed. Save waits until they pass, or until you{" "}
      <Link
        href={siteSettingsHref(editing, "file")}
        replace
        scroll={false}
        className="underline underline-offset-2"
      >
        allow it under Checks
      </Link>
      .
    </>
  ) : (
    `${plural(failed, "check")} failed. Save waits until they pass, or until you allow it under Checks.`
  )

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
    // Where they went, in view, rather than a notice that shrinks: on a
    // page that is the part the extra configuration is in.
    if (page && editing && view !== "paths") {
      router.replace(siteSettingsHref(editing, "paths"), { scroll: false })
      return
    }
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
  // The saves, and the line beside them that says what one does.
  const saves: {
    note: React.ReactNode
    primary: { mode: SaveMode; label: string }
    secondary?: { mode: SaveMode; label: string }
  } = servedCopy
    ? {
        note: heldNote ?? (
          <>
            nginx serves <code className="font-mono">sites-enabled/{spec.name}</code>, a file of its
            own, not this one. Saving here changes nothing it serves until that file is replaced by
            a link to this one.
          </>
        ),
        primary: { mode: "keep", label: "Save" },
      }
    : disabled
      ? {
          note:
            heldNote ??
            (file?.enabledElsewhere
              ? `${file.enabledElsewhere}, so this site can be neither enabled nor tested under its name.`
              : confd
                ? "Disabled: nginx reads only the conf.d files ending in .conf. nginx tests it as if it did, and it stays off until it is renamed."
                : "Disabled. nginx tests it as if enabled, and it stays off until you enable it."),
          primary: { mode: "keep", label: "Save (stays disabled)" },
          secondary:
            !file?.enabledElsewhere && !confd
              ? { mode: "enable", label: "Save and enable" }
              : undefined,
        }
      : {
          note:
            heldNote ??
            (droppedLines > 0
              ? `Saving drops ${plural(droppedLines, "line")} of the file, listed ${page ? "under General" : "above"}.`
              : "Validated with nginx\u2019s own parser before it takes effect, and rolled back if the test fails."),
          primary: { mode: "reload", label: "Save and reload" },
          secondary: { mode: "save", label: "Save only" },
        }
  // What Settings have to save: an edit, or a file the form would write
  // differently from how it stands — one written by hand, which a save puts
  // in the form's own layout. A site whose file is as the form writes it has
  // nothing to save, and no bar.
  const unsaved = start ? changedFields(spec, start).length : 0
  // Neither is said before the preview has answered for this draft: until
  // then there is no telling whether a save would change the file, and a bar
  // that came up offering one command and then made room for a second moved
  // its buttons under the pointer.
  const measured = base !== null && file !== null
  const rewrites = !dirty && measured && changed > 0
  const saveable = dirty || rewrites
  // A disabled site with nothing to save still has one thing a save can do,
  // which is enable it: the bar stays for that, with that command alone.
  const enableOnly = !saveable && measured && saves.secondary?.mode === "enable"
  const onSaveKey = useEffectEvent((event: KeyboardEvent) => {
    const key = event.key.toLowerCase() === "s" && (event.ctrlKey || event.metaKey)
    if (!key || event.altKey || event.shiftKey || rawOpen || discarding || leaving) return
    event.preventDefault()
    if (page && !saveable) return
    if (ready && !held && busy === null) void save(primary)
  })
  useEffect(() => {
    if (!open) return
    const listener = (event: KeyboardEvent) => onSaveKey(event)
    window.addEventListener("keydown", listener)
    return () => window.removeEventListener("keydown", listener)
  }, [open])

  // The form's parts, each drawn once and stood in either layout: the sheet a
  // new site is made in, and the Settings view of an existing site's page.
  const conflictNotice = (
    <>
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
    </>
  )
  const staleNotice = (
    <>
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
    </>
  )
  const fileNotice = (
    <>
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
                    The current file is kept as <code className="font-mono">{fileName}.bak</code>.
                  </>
                )}
              </p>
              <Well className="mt-2 overflow-hidden p-0">
                <DiffView body={droppedDiff(dropped)} singleFile lineNumbers className="max-h-56" />
              </Well>
              {movable.length < dropped.length && (
                <p className="text-hint text-muted-foreground">
                  Only lines directly in the server block can move as they are. The others stay only
                  if the file is edited by hand.
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
    </>
  )
  const identity = (
    <>
      <Field
        label="Domains"
        htmlFor="site-domains"
        tip={
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
    </>
  )
  const startFrom = (
    <>
      {!source && (
        <Part title="Start from">
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
        </Part>
      )}
    </>
  )
  const kindChoice = (
    <ChoiceGrid columns={2} className="grid-cols-2 sm:grid-cols-4">
      {(
        [
          { kind: "proxy", label: "An app" },
          { kind: "static", label: "Files" },
          { kind: "php", label: "PHP" },
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
  )
  const kindFields = (
    <>
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
          <Field label="Directory" htmlFor="site-root" tip="The folder holding index.html.">
            <Input
              id="site-root"
              value={spec.root ?? ""}
              onChange={(e) => set("root", e.target.value)}
              placeholder="/var/www/site"
              className="font-mono text-xs"
            />
          </Field>
          <IndexFilesField spec={spec} onChange={(v) => set("indexFiles", v)} />
          <OptionList>
            <OptionRow
              title="Single-page app"
              hint="A path with no file of its own gets index.html, so the app's router answers deep links and reloads."
              checked={!!spec.spa}
              onCheckedChange={(v) => set("spa", v)}
            />
            <OptionRow
              title="List folders"
              hint="A folder without an index is answered with a list of its files, visible to anyone who can reach the site."
              checked={!!spec.autoindex}
              onCheckedChange={(v) => set("autoindex", v)}
            />
            <OptionRow
              title="Serve precompressed files"
              hint="A file.gz beside a file is sent in its place to browsers that accept gzip. Compress at build time."
              checked={!!spec.gzipStatic}
              onCheckedChange={(v) => set("gzipStatic", v)}
            />
          </OptionList>
        </>
      )}
      {spec.kind === "php" && (
        <>
          <Field
            label="Directory"
            htmlFor="site-root"
            tip="The folder holding index.php: a Laravel app's public folder."
          >
            <Input
              id="site-root"
              value={spec.root ?? ""}
              onChange={(e) => set("root", e.target.value)}
              placeholder="/var/www/site"
              className="font-mono text-xs"
            />
          </Field>
          <Field
            label="PHP-FPM socket"
            htmlFor="site-php-socket"
            hint={
              phpSockets.data?.length
                ? `Found in /run/php: ${phpSockets.data.join(", ")}`
                : phpSockets.data
                  ? "No socket in /run/php. Install php-fpm, or give the socket its pool listens on."
                  : "Where php-fpm listens."
            }
          >
            {phpSockets.data?.length ? (
              <Select value={spec.phpSocket ?? ""} onValueChange={(v) => set("phpSocket", v)}>
                <SelectTrigger id="site-php-socket" className="w-full font-mono text-xs">
                  <SelectValue placeholder="Choose a socket" />
                </SelectTrigger>
                <SelectContent>
                  {[
                    ...phpSockets.data,
                    ...(spec.phpSocket && !phpSockets.data.includes(spec.phpSocket)
                      ? [spec.phpSocket]
                      : []),
                  ].map((socket) => (
                    <SelectItem key={socket} value={socket} className="font-mono text-xs">
                      {socket}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <Input
                id="site-php-socket"
                value={spec.phpSocket ?? ""}
                onChange={(e) => set("phpSocket", e.target.value)}
                placeholder="/run/php/php8.3-fpm.sock"
                className="font-mono text-xs"
              />
            )}
          </Field>
          <IndexFilesField spec={spec} onChange={(v) => set("indexFiles", v)} />
          <OptionList>
            <OptionRow
              title="Front controller"
              hint="A path with no file of its own goes to index.php, which is how WordPress and Laravel route every page."
              checked={!!spec.phpFrontController}
              onCheckedChange={(v) => set("phpFrontController", v)}
            />
          </OptionList>
        </>
      )}
      {spec.kind === "redirect" && (
        <>
          <Field
            label="Redirect to"
            htmlFor="site-redirect"
            hint={
              spec.redirectDropPath
                ? "Every path lands on this address."
                : "The path and query are carried across."
            }
          >
            <Input
              id="site-redirect"
              value={spec.redirectTo ?? ""}
              onChange={(e) => set("redirectTo", e.target.value)}
              placeholder="https://new.example.com"
              className="font-mono text-xs"
            />
          </Field>
          <Field
            label="Status"
            hint={REDIRECT_CODES.find((c) => c.code === redirectCode(spec))?.hint}
          >
            <ToggleGroup
              type="single"
              aria-label="Redirect status"
              value={String(redirectCode(spec))}
              onValueChange={(v) => {
                if (!v) return
                const code = Number(v) as NonNullable<SiteSpec["redirectCode"]>
                setSpec((s) => ({
                  ...s,
                  redirectCode: code,
                  permanent: code === 301 || code === 308,
                }))
              }}
              variant="outline"
              size="sm"
              className="w-full"
            >
              {REDIRECT_CODES.map(({ code }) => (
                <ToggleGroupItem
                  key={code}
                  value={String(code)}
                  className="flex-1 font-mono text-hint"
                >
                  {code}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </Field>
          <OptionList>
            <OptionRow
              title="Keep the path and query"
              hint="/docs?page=2 lands on the same path at the new address. Off, every path lands on the address itself."
              checked={!spec.redirectDropPath}
              onCheckedChange={(v) => set("redirectDropPath", !v)}
            />
          </OptionList>
        </>
      )}
    </>
  )
  const canonical = (
    <>
      {spec.domains.some((d) => !d.startsWith("*.")) && (
        <Field
          label="Canonical name"
          hint={
            spec.canonical === "www"
              ? "example.com is sent to www.example.com for each www. domain above."
              : spec.canonical === "apex"
                ? "www.example.com is sent to example.com for each domain above without www."
                : "Only the domains above are answered."
          }
        >
          <ToggleGroup
            type="single"
            aria-label="Canonical name"
            value={spec.canonical || "none"}
            onValueChange={(v) => {
              if (v) set("canonical", v === "none" ? undefined : (v as "www" | "apex"))
            }}
            variant="outline"
            size="sm"
            className="w-full"
          >
            <ToggleGroupItem value="none" className="flex-1 text-hint">
              As listed
            </ToggleGroupItem>
            <ToggleGroupItem value="apex" className="flex-1 text-hint">
              Without www
            </ToggleGroupItem>
            <ToggleGroupItem value="www" className="flex-1 text-hint">
              With www
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>
      )}
    </>
  )
  const encryption = (
    <>
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
                No certificate is listed at this path. If it does not exist yet, nginx refuses the
                site at reload —{" "}
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
              hint="Tells the browser never to use plain HTTP for this name again. Hard to undo — a mistake sticks for as long as the policy says."
              checked={spec.hsts}
              onCheckedChange={(v) => set("hsts", v)}
            >
              <HSTSOptions spec={spec} set={set} />
            </OptionRow>
            <OptionRow
              title="HTTP/2"
              hint="Faster for pages with many small assets."
              checked={spec.http2}
              onCheckedChange={(v) => set("http2", v)}
            />
          </>
        )}
      </OptionList>
      {spec.tls && (
        <Field
          label="TLS versions"
          htmlFor="site-tls-profile"
          hint={
            spec.tlsProfile === "modern"
              ? "Clients without TLS 1.3 cannot connect: Android before 10, Java 8, older curl and OpenSSL, some monitoring and payment callbacks."
              : "What every current browser and client speaks. 1.0 and 1.1 stay off."
          }
        >
          <Select
            value={spec.tlsProfile || "compatible"}
            onValueChange={(v) => set("tlsProfile", v === "modern" ? "modern" : undefined)}
          >
            <SelectTrigger id="site-tls-profile" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="compatible">TLS 1.2 and 1.3</SelectItem>
              <SelectItem value="modern">TLS 1.3 only (modern)</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      )}
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
            // The certificate flow saves the plain-HTTP site with a reload.
            onSaved(true)
          }}
          onDone={(res) => {
            const outcome = saveOutcome(res, { existing: true })
            notify[outcome.tone](outcome.title, { description: outcome.description })
            onSaved(res.reloaded)
            settle()
          }}
        />
      )}
    </>
  )
  const behaviour = (
    <>
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
          tip="nginx refuses a larger request body with a 413."
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
          tip="Seconds nginx waits for the application to answer."
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
    </>
  )
  const hardening = (
    <>
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
          tip="Timed is what the traffic pages read latency from."
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
    </>
  )
  const access = (
    <>
      <AccessSection spec={spec} set={set} open={open} authFiles={authFiles.data} />

      <CrawlerSection spec={spec} set={set} />

      <RealIPSection spec={spec} set={set} open={open} />
    </>
  )
  const limits = (
    <>
      {spec.kind !== "redirect" && <LimitsSection spec={spec} set={set} />}

      {spec.kind !== "redirect" && <CachingSection spec={spec} set={set} />}
    </>
  )
  const headers = (
    <>
      <HeadersSection spec={spec} set={set} />

      <PagesSection spec={spec} set={set} site={editing} />
    </>
  )
  const paths = (
    <>
      {spec.kind === "proxy" && (
        <Part
          title="Paths that go somewhere else"
          hint="Everything not matched by one of these goes to the site's main upstream."
        >
          <LocationsField
            locations={spec.locations}
            onChange={(v) => set("locations", v)}
            picker={picker}
            sso={spec.forwardAuth && (spec.forwardAuth.pathsOnly ? "paths" : "site")}
          />
          {spec.locations.length > 0 && <RoutesTable spec={spec} />}
        </Part>
      )}
    </>
  )
  const anythingElse = (
    <Part title="Anything else">
      <Field
        label="Extra configuration"
        htmlFor="site-custom"
        tip="Added verbatim inside the server block."
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
    </Part>
  )
  const review = (
    <Tabs
      // Settings send a reader here from "allow it under Checks".
      defaultValue={page && held ? "checks" : "preview"}
      className="flex min-h-0 flex-1 flex-col gap-3"
    >
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
              Enter a domain and the config appears here, rendered by the server that will write it.
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
                Too many changes to line up. The nginx config tab has the whole file a save writes.
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
              Each failed check below is a site that will not work as saved. Fix them, or save it as
              it is.
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
  )
  const rawEditor = (
    <>
      {editing && filePath && (
        <ConfigEditor
          open={rawOpen}
          onOpenChange={setRawOpen}
          path={filePath}
          kind="nginx"
          title={fileName}
          initialLine={dropped?.[0]?.line}
          siteDisabled={disabled}
          onSaved={(reloaded) => {
            setReads((n) => n + 1)
            onSaved(reloaded)
          }}
        />
      )}
    </>
  )

  if (page) {
    const name = editing ?? ""
    // The File part carries what would hold or qualify a save: the checks
    // that failed, or the ones that caution, or the preview's notes.
    const fileMark: PartMark | undefined =
      failed > 0
        ? { count: failed, tone: "danger" }
        : cautions + warnings.length > 0
          ? { count: cautions + warnings.length, tone: "warning" }
          : changed > 0
            ? { count: changed, tone: "muted" }
            : undefined
    return (
      <PageLayout.Provider value>
        <div data-slot="site-settings" className="mx-auto w-full max-w-3xl min-w-0 space-y-6 pb-24">
          <SettingsParts
            name={name}
            view={view}
            unsaved={start ? changedViews(spec, start) : []}
            marks={{ file: fileMark }}
          />
          {dirty && start && (
            <UnsavedStrip
              name={name}
              view={view}
              fields={Math.max(unsaved, 1)}
              lines={changed}
              parts={changedViews(spec, start)}
            />
          )}
          {conflictNotice}
          {staleNotice}
          {(view === "general" || view === "file") && fileNotice}
          {!loaded ? (
            // The column's own silhouette while the file is read, so the
            // fields do not arrive holding a new site's defaults.
            <LoadingRows rows={6} />
          ) : (
            <FormSections>
              {view === "general" && (
                <>
                  <Part title="Names">{identity}</Part>
                  <Part title="What it serves">
                    {kindChoice}
                    {kindFields}
                    {canonical}
                  </Part>
                  {spec.kind === "proxy" && <Part title="Behaviour">{behaviour}</Part>}
                </>
              )}
              {view === "https" && <Part title="Encryption">{encryption}</Part>}
              {view === "access" && access}
              {view === "hardening" && (
                <>
                  <Part title="Hardening">{hardening}</Part>
                  {limits}
                </>
              )}
              {view === "headers" && headers}
              {view === "paths" && (
                <>
                  {paths}
                  {anythingElse}
                </>
              )}
              {view === "file" && (
                <SettingSection
                  title="The file"
                  state={filePath && <span className="font-mono">{filePath}</span>}
                >
                  {/* A pane's height of its own: beside the sheet's fields it
                      took what the sheet had left, and a column has no such
                      remainder to give it. */}
                  <div className="flex h-[32rem] min-h-0 flex-col">{review}</div>
                </SettingSection>
              )}
            </FormSections>
          )}
        </div>
        {loaded && (saveable || enableOnly) && (
          <SiteSaveBar
            headline={
              dirty
                ? unsaved <= 1
                  ? "1 unsaved change"
                  : `${unsaved} unsaved changes`
                : rewrites
                  ? `Saving rewrites ${plural(changed, "line")} of the file`
                  : "Disabled"
            }
            note={saves.note}
            primary={
              enableOnly && saves.secondary
                ? {
                    label: saves.secondary.label,
                    run: () => void save(saves.secondary!.mode),
                    pending: busy === saves.secondary.mode,
                  }
                : {
                    label: saves.primary.label,
                    run: () => void save(saves.primary.mode),
                    pending: busy === saves.primary.mode,
                  }
            }
            secondary={
              !enableOnly && saves.secondary
                ? {
                    label: saves.secondary.label,
                    run: () => void save(saves.secondary!.mode),
                    pending: busy === saves.secondary.mode,
                  }
                : undefined
            }
            onDiscard={dirty ? resetDraft : undefined}
            disabled={!ready || held}
            saving={busy !== null}
          />
        )}
        <DiscardDialog
          open={leaving !== null}
          onStay={() => setLeaving(null)}
          onDiscard={() => {
            const go = leaving?.go
            setLeaving(null)
            resetDraft()
            go?.()
          }}
        >
          What you changed in {name} is not saved. Discarding it leaves the file as it is.
        </DiscardDialog>
        {rawEditor}
      </PageLayout.Provider>
    )
  }

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
        <>
          <span className="mr-auto text-hint text-muted-foreground">{saves.note}</span>
          {saves.secondary && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => save(saves.secondary!.mode)}
              disabled={!ready || held || busy !== null}
              pending={busy === saves.secondary.mode}
            >
              {saves.secondary.label}
            </Button>
          )}
          <Button
            size="sm"
            onClick={() => save(saves.primary.mode)}
            disabled={!ready || held || busy !== null}
            pending={busy === saves.primary.mode}
            aria-keyshortcuts="Control+S Meta+S"
          >
            {saves.primary.label}
          </Button>
        </>
      }
    >
      <div className="min-h-0 flex-1 overflow-y-auto p-4 lg:w-[26rem] lg:shrink-0 lg:border-r lg:border-hairline">
        <div className="space-y-6">
          {conflictNotice}
          {staleNotice}
          {fileNotice}
          {identity}
          {startFrom}
          <Part title="What it serves">{kindChoice}</Part>
          {kindFields}
          {canonical}
          <Part title="Encryption">{encryption}</Part>
          {spec.kind === "proxy" && <Part title="Behaviour">{behaviour}</Part>}
          <Part title="Hardening">{hardening}</Part>
          {access}
          {limits}
          {headers}
          {paths}
          {anythingElse}
        </div>
      </div>

      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 p-4">{review}</div>
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
      {rawEditor}
    </SidePanel>
  )
}
