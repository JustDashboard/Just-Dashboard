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
  Container,
  DomainCheck,
  DroppedLine,
  ErrorPageCode,
  Exposure,
  Listener,
  SiteLocation,
  SiteMaintenance,
  SitePageName,
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
import { DiffView } from "@/components/files/diff-view"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Group, Pane, Well } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, Notice } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
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
import {
  nothingListening,
  upstreamOptions,
  type UpstreamOption,
} from "@/components/proxy/upstream-options"
import { UpstreamPicker } from "@/components/proxy/upstream-picker"
import { PAGE_LABEL, PageEditor } from "@/components/proxy/page-editor"

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
  /** Told whether nginx was reloaded with the save. */
  onSaved: (reloaded: boolean) => void
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
      onSaved(res.reloaded)
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
    spec.kind === "proxy" && (source !== null || upstreamSet)
      ? nothingListening(spec.upstream ?? "", picker.listeners, picker.containers)
      : undefined
  const chosenPreset = !source ? presetById(preset) : undefined
  const warnings = preview?.warnings ?? []

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
    if (ready && busy === null) void save(primary)
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
              nginx serves <code className="font-mono">sites-enabled/{spec.name}</code>, a file of
              its own, not this one. Saving here changes nothing it serves until that file is
              replaced by a link to this one.
            </span>
            <Button
              size="sm"
              onClick={() => save("keep")}
              disabled={!ready || busy !== null}
              pending={busy === "keep"}
              aria-keyshortcuts="Control+S Meta+S"
            >
              Save
            </Button>
          </>
        ) : disabled ? (
          <>
            <span className="mr-auto text-hint text-muted-foreground">
              {file?.enabledElsewhere
                ? `${file.enabledElsewhere}, so this site can be neither enabled nor tested under its name.`
                : confd
                  ? "Disabled: nginx reads only the conf.d files ending in .conf. nginx tests it as if it did, and it stays off until it is renamed."
                  : "Disabled. nginx tests it as if enabled, and it stays off until you enable it."}
            </span>
            {!file?.enabledElsewhere && !confd && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => save("enable")}
                disabled={!ready || busy !== null}
                pending={busy === "enable"}
              >
                Save and enable
              </Button>
            )}
            <Button
              size="sm"
              onClick={() => save("keep")}
              disabled={!ready || busy !== null}
              pending={busy === "keep"}
              aria-keyshortcuts="Control+S Meta+S"
            >
              Save (stays disabled)
            </Button>
          </>
        ) : (
          <>
            <span className="mr-auto text-hint text-muted-foreground">
              {droppedLines > 0
                ? `Saving drops ${plural(droppedLines, "line")} of the file, listed above.`
                : "Validated with nginx\u2019s own parser before it takes effect, and rolled back if the test fails."}
            </span>
            <Button
              size="sm"
              variant="outline"
              onClick={() => save("save")}
              disabled={!ready || busy !== null}
              pending={busy === "save"}
            >
              Save only
            </Button>
            <Button
              size="sm"
              onClick={() => save("reload")}
              disabled={!ready || busy !== null}
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
          {spec.domains[0] && <DNSCheck domain={spec.domains[0]} />}

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
                hint="Needs a certificate on disk. Issue one from the Certificates tab first."
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
          onSaved={(reloaded) => {
            setReads((n) => n + 1)
            onSaved(reloaded)
          }}
        />
      )}
    </SidePanel>
  )
}

/**
 * Does the domain point here yet?
 *
 * The first question of every reverse-proxy setup and the cause of most of the
 * failures: certbot cannot prove control of a name that resolves somewhere
 * else, and the error it gives says "challenge failed" rather than "your DNS
 * is not updated yet".
 */
function DNSCheck({ domain }: { domain: string }) {
  const [check, setCheck] = useState<DomainCheck | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    const timer = setTimeout(() => {
      setLoading(true)
      get<DomainCheck>("/certificates/dns", { domain }, controller.signal)
        .then(setCheck)
        .catch(() => setCheck(null))
        .finally(() => !controller.signal.aborted && setLoading(false))
    }, 600)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [domain])

  if (loading && !check) {
    return <FormNote className="-mt-3">Checking where {domain} points…</FormNote>
  }
  if (!check) return null
  return (
    <FormNote
      className="-mt-3"
      tone={
        check.pointsHere
          ? "success"
          : // A host behind provider NAT has no address of its own to compare
            // against, so "cannot tell" is muted like the CDN case rather than
            // warned about — the domain is very probably fine.
            check.behindProxy || !check.hostAddressesKnown
            ? "default"
            : "warning"
      }
    >
      {check.summary}
    </FormNote>
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
        <div className="flex items-center gap-2">
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
        <Group key={i} className={cn("space-y-2")}>
          <div className="flex items-center gap-2">
            <Input
              value={loc.path}
              onChange={(e) => update(i, { path: e.target.value })}
              placeholder="/api"
              aria-label="Path"
              className="font-mono text-xs"
            />
            <IconAction
              label={`Remove ${loc.path || "location"}`}
              className="text-destructive"
              onClick={() => onChange(locations.filter((_, j) => j !== i))}
            >
              <Trash />
            </IconAction>
          </div>
          <UpstreamPicker
            value={loc.upstream ?? ""}
            onChange={(upstream) => update(i, { upstream, root: "" })}
            options={picker.options}
            loading={picker.loading}
            failed={picker.failed}
            onRetry={picker.onRetry}
            placeholder="http://127.0.0.1:4000 — or leave empty and give a folder"
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
              onChange={(e) => update(i, { root: e.target.value })}
              placeholder="/var/www/assets"
              aria-label="Folder"
              className="font-mono text-xs"
            />
          )}
          {!loc.upstream && loc.root && loc.rootMode === "root" && (
            <FormNote>
              Files come from{" "}
              <code className="font-mono">
                {loc.root.replace(/\/$/, "")}
                {loc.path}
              </code>
              , as this file was written: nginx adds the path to the folder.
            </FormNote>
          )}
          <label className="flex items-center gap-2 text-hint text-muted-foreground">
            <Checkbox
              checked={loc.webSockets}
              onCheckedChange={(v) => update(i, { webSockets: Boolean(v) })}
            />
            WebSockets on this path
          </label>
        </Group>
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
