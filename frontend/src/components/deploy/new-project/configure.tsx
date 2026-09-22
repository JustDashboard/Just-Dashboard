"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import type {
  DeploymentDraft,
  DeploymentPreflight,
  DeploymentPreflightFinding,
  WorkloadProfile,
  GitHubBranch,
} from "@/lib/types"
import { Disclosure } from "@/components/form"
import { FlowActions, FlowPanel, FlowPanelBody } from "@/components/flow"
import { BorderBeam } from "@/components/ui/border-beam"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { DEPLOYMENT_NAME } from "@/components/deploy/vocabulary"
import { blockingFindings, warningFindings } from "@/components/deploy/deployment-findings"
import {
  checksForRuntime,
  discoveredEnvironmentRows,
  mergeDiscoveredRows,
  validateConfiguration,
} from "@/components/deploy/deployment-defaults"
import { PlanWiring } from "@/components/deploy/new-project/plan-wiring"
import { StepProject } from "@/components/deploy/new-project/step-project"
import { StepRuntime } from "@/components/deploy/new-project/step-runtime"
import { StepVariables } from "@/components/deploy/new-project/step-variables"
import { StepReview } from "@/components/deploy/new-project/step-review"
import {
  SECTION_FOLDS,
  SECTION_IDS,
  SECTION_STEPS,
  sectionForField,
  type PlanSection,
} from "@/components/deploy/new-project/plan-sections"
import {
  adoptImport,
  commitDraft,
  declaredVariablesNeedReview,
  enqueueDeploy,
  environmentText,
  importEnvironment,
  loadDraft,
  preflightDraft,
  reinspect,
  saveConfiguration,
  selectCandidate,
  stepAfter,
  stepBefore,
  fetchHostnameSuggestion,
  forgetNewProject,
  type ConfigureFlow,
  type ConfigureStepKey,
  type DraftGitPolicy,
  type EnvironmentDraft,
  type EnvironmentRow,
  type FlowUpdate,
} from "@/components/deploy/new-project/draft"

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * The configure half of `/deploy/new`, for every source from a picked
 * repository to an adopted container — and, since this pass, four screens
 * rather than one.
 *
 * One screen carried the source controls, the name, the type, a ten-field
 * build fold, the environment editor, the public address, two
 * automatic-deployment switches, an "Advanced" fold holding seven more
 * sections, and the findings: past forty controls for a Git repository, every
 * one of them in the document before the reader had answered the first. The
 * fix is not fewer settings — each of them is there because a deployment went
 * wrong without it — it is asking for them four at a time, in the order a
 * deployment actually decides them.
 *
 * This file is the sequence and everything the sequence owns: the draft's
 * lifecycle, the environment held in memory, preflight and its
 * acknowledgements, and the one command at the end. Each screen is a file of
 * its own beside it. What differs between sources is still which parts show —
 * the build fold is Git-only, an import ends in "Adopt workload" — not how
 * any of it is built.
 */
export function Configure({
  flow,
  onFlowChange,
  onChangeSource,
  initialAdvanced,
  step,
  onStepChange,
}: {
  flow: ConfigureFlow
  // Accepts the functional updater form too: the two calls in `submit` below
  // run after an `await`, and merging onto the closed-over `flow` there would
  // revert anything the operator typed while the request was in flight.
  // `new-project.tsx` wires this straight to the remembered flow's setter,
  // which is why the type carries `| null` here even though this component
  // only ever runs once `flow` itself is set — every functional update below
  // guards it back out.
  onFlowChange: (next: FlowUpdate) => void
  onChangeSource: () => void
  initialAdvanced: boolean
  step: ConfigureStepKey
  onStepChange: (step: ConfigureStepKey) => void
}) {
  const router = useRouter()
  const [nameTouched, setNameTouched] = useState(false)
  /**
   * The name a live project already holds, when it is the one being typed.
   *
   * The schema's UNIQUE constraint is the authority and it refuses at commit —
   * after the source, the detection, the configuration and the preflight have
   * all been filled in. Asking the hostname suggestion, which this page calls
   * for the name anyway, moves that refusal to the moment the name is typed.
   */
  const [nameTaken, setNameTaken] = useState<string>()
  // The environment is remembered in memory only — never Web Storage, never
  // the URL — and belongs to this draft: a different source starts from what
  // its own detection found rather than from the last one's secrets.
  const [environment, setEnvironment] = useMemoryState<EnvironmentDraft | null>(
    "deploy.new.configure.environment",
    null,
  )
  const draftId = flow.draft.id
  const own = environment?.draftId === draftId ? environment : null
  const discovered = useMemo(() => discoveredEnvironmentRows(flow.candidate), [flow.candidate])
  const envRows = own?.rows ?? discovered
  const dotenv = own?.dotenv ?? ""
  const setEnvRows = (next: EnvironmentRow[] | ((rows: EnvironmentRow[]) => EnvironmentRow[])) =>
    setEnvironment((current) => {
      const mine = current?.draftId === draftId ? current : null
      const rows = typeof next === "function" ? next(mine?.rows ?? discovered) : next
      return { draftId, rows, dotenv: mine?.dotenv ?? "" }
    })
  const setDotenv = (value: string) =>
    setEnvironment((current) => {
      const mine = current?.draftId === draftId ? current : null
      return { draftId, rows: mine?.rows ?? discovered, dotenv: value }
    })
  // The detected rows are written down as soon as they exist, so a generated
  // value (a Laravel key) is the same one on every visit rather than minted
  // again each time the page mounts.
  useEffect(() => {
    if (own) return
    setEnvironment({ draftId, rows: discovered, dotenv: "" })
  }, [own, draftId, discovered, setEnvironment])
  // A branch change re-detects, and the new candidate may read variables the
  // old one did not; they join the rows without touching anything typed.
  const rowsCandidate = useRef(flow.candidate)
  useEffect(() => {
    const previous = rowsCandidate.current
    if (previous === flow.candidate) return
    rowsCandidate.current = flow.candidate
    const found = discoveredEnvironmentRows(flow.candidate)
    setEnvironment((current) => {
      const mine = current?.draftId === draftId ? current : null
      const rows = mine?.rows ?? discoveredEnvironmentRows(previous)
      return { draftId, rows: mergeDiscoveredRows(rows, found), dotenv: mine?.dotenv ?? "" }
    })
  }, [flow.candidate, draftId, setEnvironment])
  const [branch, setBranch] = useState(flow.source.ref ?? "main")
  const [branchBusy, setBranchBusy] = useState(false)
  /**
   * The plan as it stands, as one comparable value.
   *
   * `flow.configuration` is replaced wholesale by every edit, so its identity
   * cannot say whether anything actually changed — a re-render that rebuilt
   * the same plan would read as a change, and re-checking on each of those is
   * a request per keystroke.
   */
  const planSignature = useMemo(() => JSON.stringify(flow.configuration), [flow.configuration])
  // Preflight's findings and which warnings were acknowledged belong to the
  // draft they were computed for, so a fresh source never inherits them — and
  // to the *plan* they were computed for, which is what `checked` records.
  // Review draws its passes now, not only its refusals, so a reader who goes
  // back from it to change the port and returns would otherwise be reading a
  // list of things this server agreed to about a plan that no longer exists.
  const [review, setReview] = useSessionState<{
    draftId: string
    checked?: string
    preflight?: DeploymentPreflight
    acknowledged: string[]
  } | null>("deploy.new.configure.preflight", null)
  // A result computed for a different plan reads as no result: the sections
  // it feeds empty, and the effect below asks again.
  const preflight =
    review?.draftId === draftId && review.checked === planSignature ? review.preflight : undefined
  const acknowledged = review?.draftId === draftId ? review.acknowledged : []
  const setPreflight = (next: DeploymentPreflight, checked: string) =>
    setReview((current) => ({
      draftId,
      checked,
      preflight: next,
      acknowledged: current?.draftId === draftId ? current.acknowledged : [],
    }))
  const setAcknowledged = (next: string[]) =>
    setReview((current) => ({
      draftId,
      checked: current?.draftId === draftId ? current.checked : undefined,
      preflight: current?.draftId === draftId ? current.preflight : undefined,
      acknowledged: next,
    }))
  const [busy, setBusy] = useState("")
  const [failure, setFailure] = useState<Error>()
  // The server's own defaults, shown rather than assumed: this is the decision
  // that used to be reachable only after the first push had already deployed.
  const [gitPolicy, setGitPolicy] = useSessionState<DraftGitPolicy>(
    "deploy.new.configure.gitPolicy",
    { automatic: true, watchInclude: [], watchExclude: [], commitStatuses: true },
  )
  const [created, setCreated] = useState<{
    projectId: number
    environmentId: number
    ready: boolean
  }>()
  // Once the project exists nothing about this setup is unfinished, and the
  // next "New project" starts blank. Forgotten on the way out rather than at
  // commit, so the "Deployment created" panel is not swapped for the source
  // chooser in the frame before the project page opens.
  const done = Boolean(created)
  useEffect(() => {
    if (!done) return
    return () => forgetNewProject()
  }, [done])

  // Debounced, because it runs on every keystroke of the name field, and
  // never fatal: an unanswered question leaves the commit to refuse as before.
  const typedName = flow.name.trim()
  useEffect(() => {
    if (!typedName || !DEPLOYMENT_NAME.test(typedName)) return
    let cancelled = false
    const timer = setTimeout(() => {
      void fetchHostnameSuggestion(typedName).then((suggestion) => {
        if (cancelled) return
        setNameTaken(suggestion?.nameTaken ? typedName : undefined)
      })
    }, 400)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [typedName])

  const branches = usePoll(
    (signal) =>
      get<GitHubBranch[]>(
        `/git/github/branches?repo=${encodeURIComponent(flow.githubRepo ?? "")}`,
        undefined,
        signal,
      ),
    0,
    [flow.githubRepo],
    { enabled: Boolean(flow.githubRepo) },
  )

  const configuration = flow.configuration
  const isGitSource = flow.source.kind === "git" || flow.source.kind === "local"
  const isImport = flow.source.kind === "import"
  const nameCollides = nameTaken === flow.name.trim() && flow.name.trim() !== ""
  // A detected row the operator left empty is skipped, not set to nothing:
  // the application may have a default for it, and an empty secret is a
  // value that fails somewhere far from here. Review counts these same rows:
  // the environment lives in memory and the step in the session store, so a
  // reload used to land back on Review reading "3 values" with none to send.
  const sentRows = envRows.filter((row) => row.name.trim() && (!row.detected || row.value))
  const text = environmentText(sentRows, dotenv)

  const changeBranch = async (nextRef: string) => {
    setBranch(nextRef)
    if (!nextRef.trim() || nextRef === flow.source.ref) return
    setBranchBusy(true)
    setFailure(undefined)
    try {
      const nextSource = { ...flow.source, ref: nextRef }
      const result = await reinspect(flow.draft, flow.profile, nextSource)
      onFlowChange({
        ...flow,
        source: nextSource,
        draft: result.draft,
        candidate: result.candidate,
        detection: result.detection,
        configuration: result.configuration,
      })
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBranchBusy(false)
    }
  }

  const changeProfile = (profile: WorkloadProfile) => {
    const internalPort =
      profile === "worker"
        ? 0
        : profile === "static" && configuration.build.method !== "dockerfile"
          ? 80
          : configuration.runtime.internalPort || flow.candidate?.port || 0
    // Saying "this is a web application" is what makes the release safe:
    // preflight only allows candidate-first activation, and only requires a
    // readiness gate, for a web or static profile. Choosing one and leaving
    // the plan stop-first with nothing verifying it would answer half the
    // question the operator just answered.
    const gated = profile === "web" || profile === "static"
    const checks = checksForRuntime(configuration.checks, profile, internalPort)
    return onFlowChange({
      ...flow,
      profile,
      configuration: {
        ...configuration,
        domains: profile === "worker" ? [] : configuration.domains,
        checks,
        build: {
          ...configuration.build,
          startCommand:
            profile === "static" && configuration.build.method === "recipe"
              ? ""
              : configuration.build.startCommand || flow.candidate?.startCommand || "",
        },
        runtime: {
          ...configuration.runtime,
          internalPort,
          // Blue/green needs a candidate to stand beside the live one, which
          // preflight refuses for anything but a web or static profile.
          strategy: gated ? "blue_green" : "stop_first",
        },
      },
    })
  }

  // `detection_ambiguous`'s only remedy — /detect has always accepted a
  // `selectedId`, but nothing sent one, so the finding blocked every ambiguous
  // plan with no way to resolve it from this screen.
  const pickCandidate = async (id: string) => {
    setFailure(undefined)
    setBusy("detect")
    try {
      const result = await selectCandidate(flow.draft, flow.profile, flow.source, id)
      onFlowChange((current) =>
        current
          ? {
              ...current,
              draft: result.draft,
              candidate: result.candidate,
              detection: result.detection,
              configuration: result.configuration,
            }
          : current,
      )
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy("")
    }
  }

  const current: ConfigureStepKey = created ? "done" : step === "done" ? "review" : step

  /** The head of the screen, so a step change starts where the question is. */
  const toTop = () =>
    requestAnimationFrame(() =>
      document
        .querySelector("[data-slot='flow-header']")
        ?.scrollIntoView({ block: "start", behavior: "smooth" }),
    )

  const goto = (next: ConfigureStepKey) => {
    setFailure(undefined)
    onStepChange(next)
    toTop()
  }

  /**
   * Sends the reader to the fields that decide one part of the plan: a node
   * of the drawing beside the form, or a preflight finding's "Open it".
   *
   * It used to be a scroll and a fold forced open, because every field was on
   * one screen and half of them were hidden. It is a step first now, and the
   * fold only matters for the few sections that are still one.
   */
  const openSection = (section: PlanSection) => {
    onStepChange(SECTION_STEPS[section])
    // Two frames: one for the step being opened to render, one for the fold
    // inside it to lay out before anything is scrolled to it.
    requestAnimationFrame(() =>
      requestAnimationFrame(() => {
        const fold = SECTION_FOLDS[section]
        if (fold) {
          const element = document.getElementById(fold)
          if (element instanceof HTMLDetailsElement) element.open = true
        }
        document
          .getElementById(SECTION_IDS[section])
          ?.scrollIntoView({ block: "start", behavior: "smooth" })
      }),
    )
  }

  const openRemedyField = (finding: DeploymentPreflightFinding) => {
    const section = sectionForField(finding.fieldId)
    if (section) openSection(section)
  }

  /**
   * What stops this step from being finished, said in the words the reader
   * can act on.
   *
   * The whole list still runs at Deploy — a step is a way of asking, not a
   * new authority — but a name that cannot be a name, or an environment key
   * with a space in it, is refused where it was typed rather than four
   * screens later under a button.
   */
  const stepError = (target: ConfigureStepKey): string | undefined => {
    const errors = validateConfiguration(configuration, flow.profile)
    if (target === "project") {
      if (!DEPLOYMENT_NAME.test(flow.name))
        return "Use 1–64 letters, numbers, dots, dashes, or underscores for the name."
      if (nameCollides) return `A project is already called ${flow.name}. Choose another name.`
      if (
        flow.profile === "static" &&
        configuration.build.method === "recipe" &&
        !configuration.build.outputDirectory?.trim()
      )
        return "Set the output directory for your static website, such as dist or out."
      return (
        errors.buildMethod ?? errors.pythonVersion ?? errors.buildSecrets ?? errors.releaseTasks
      )
    }
    if (target === "runtime") {
      // Only the profiles preflight demands a readiness gate from: an image or
      // a service may legitimately publish nothing.
      if (
        (flow.profile === "web" || flow.profile === "static") &&
        (configuration.runtime.internalPort ?? 0) === 0
      )
        return "Set the port your application listens on inside the container."
      return errors.internalPort ?? errors.hostPort
    }
    if (target === "variables") {
      if (
        envRows.some((row) => (row.name || row.value) && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(row.name))
      )
        return "Give each environment variable a valid key, using letters, numbers and underscores."
      const names = envRows.map((row) => row.name).filter(Boolean)
      if (new Set(names).size !== names.length)
        return "Each environment variable needs a unique key."
      return errors.variables ?? errors.eula
    }
    return undefined
  }

  const advance = () => {
    const problem = stepError(current)
    if (problem) {
      if (current === "project") setNameTouched(true)
      setFailure(new Error(problem))
      return
    }
    const next = stepAfter(current)
    if (next) goto(next)
  }

  const back = () => {
    const previous = stepBefore(current)
    if (previous) goto(previous)
    else onChangeSource()
  }

  const submit = async (operation: "save" | "deploy" | "check") => {
    for (const target of ["project", "runtime", "variables"] as const) {
      const problem = stepError(target)
      if (!problem) continue
      // Refused on the screen that owns the answer, not on Review: a blocked
      // Deploy that names a field the reader cannot see is the defect the
      // steps were drawn to remove.
      if (target === "project") setNameTouched(true)
      onStepChange(target)
      setFailure(new Error(problem))
      toTop()
      return
    }

    setBusy(operation)
    setFailure(undefined)
    try {
      // Readiness follows the runtime publication, which may differ from the
      // container port when Docker allocates a free host port.
      const toSave = {
        ...configuration,
        // An "Add another hostname" row left blank is a change of mind, not a
        // request to publish nothing: the plan validator refuses an empty
        // hostname as `domains[1] invalid or duplicate planned domain`, which
        // is a refusal about a field the operator never filled in.
        domains: configuration.domains.filter((domain) => domain.hostname.trim()),
        checks: configuration.checks.map((check) =>
          check.phase === "readiness" && check.kind === "http"
            ? { ...check, config: { ...(check.config ?? {}), port: undefined } }
            : check,
        ),
      }
      let saved: DeploymentDraft
      try {
        saved = await saveConfiguration(flow.draft, toSave)
      } catch (error) {
        // saveConfiguration bumps the revision on the server whether or not
        // what follows succeeds; without this, a failure past this point
        // left `flow.draft` on the old revision and every later save 409'd
        // forever. Re-loading the draft recovers the revision this save
        // itself just produced (or another tab's, either way the current one).
        if (!(error instanceof ApiError) || error.code !== "draft_revision_conflict") throw error
        const fresh = await loadDraft(flow.draft.id)
        saved = await saveConfiguration(fresh, toSave)
      }
      onFlowChange((current) =>
        current ? { ...current, draft: saved, configuration: toSave } : current,
      )
      const checkedDraft = await preflightDraft(saved)
      onFlowChange((current) =>
        current ? { ...current, draft: checkedDraft.draft, configuration: toSave } : current,
      )
      setPreflight(checkedDraft.preflight, planSignature)
      // Review's own arrival runs this far and no further: the screen asks
      // "is this right", and it cannot answer without having asked the server.
      if (operation === "check") return

      const blockers = blockingFindings(checkedDraft.preflight.findings)
      const warnings = warningFindings(checkedDraft.preflight.findings)
      if (blockers.length) return
      const outstanding = warnings.filter((finding) => !acknowledged.includes(finding.code))
      if (outstanding.length) return

      const commit = isImport
        ? await adoptImport(checkedDraft.draft, acknowledged, flow.importPreview?.unsupported ?? [])
        : await commitDraft(
            checkedDraft.draft,
            acknowledged,
            // Only a Git source polls a branch, so only a Git source has a
            // policy to record; anything else keeps the server's defaults.
            isGitSource ? gitPolicy : undefined,
          )
      onStepChange("done")
      setCreated({
        projectId: commit.projectId,
        environmentId: commit.environmentId,
        ready: !text.trim(),
      })

      if (text.trim()) {
        await importEnvironment(commit.projectId, commit.environmentId, commit.planRevision, text)
      }
      setCreated({ projectId: commit.projectId, environmentId: commit.environmentId, ready: true })

      if (operation === "deploy" && !isImport) {
        const run = await enqueueDeploy(commit.projectId, commit.environmentId)
        router.push(`/deploy/${commit.projectId}/runs/${run.id}`)
        return
      }
      router.push(`/deploy/${commit.projectId}`)
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy("")
    }
  }

  /**
   * Preflight, when Review is reached rather than when Deploy is pressed.
   *
   * It ran inside the press, so the screen titled "Ready to deploy?" had
   * checked nothing by the time it was read: with no finding to draw, a plan
   * with nothing wrong with it showed one section of four facts, and the
   * first press was a check whose findings appeared under a button the reader
   * had already pressed. Running it on arrival is what makes the step the
   * last look it is named for, and what makes Deploy one press.
   *
   * Guarded on there being no result *for this plan* — `preflight` reads as
   * undefined once `planSignature` moves — so arriving asks once, re-reading
   * the same screen asks nothing, and going back to change the port and
   * returning asks again rather than showing what the server agreed to about
   * the previous plan. `submit` runs the three step gates first, so an arrival
   * carrying an unanswered field is still sent to the screen that owns it —
   * and that changes `current`, which is what stops this from firing again.
   */
  useEffect(() => {
    if (current !== "review" || preflight || busy || created) return
    // Scheduled rather than called: `submit` raises the busy flag as its first
    // act, and a state write in an effect's own body is a cascading render.
    // The cleanup drops a check nobody is waiting for any more.
    const timer = setTimeout(() => void submit("check"), 0)
    return () => clearTimeout(timer)
    // `submit` closes over the whole form; re-running this for each of those
    // is what the `preflight` guard is there to make unnecessary.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current, preflight, busy, created])

  if (created)
    return (
      /* The outcome lands on the same focused surface the plan was decided on
         (§17), not on a framed `Panel` borrowed from the reading register —
         the sequence ends where it was being worked. Its name is the page's
         `h1`, which is where the question was: a panel header repeating it
         would be the same sentence twice. */
      <FlowPanel>
        <FlowPanelBody className="space-y-4">
          {failure ? (
            <ErrorState error={failure} />
          ) : (
            <p className="text-body">Starting the first release…</p>
          )}
          <p className="text-body text-muted-foreground">
            Your project and configuration are saved.{" "}
            {created.ready
              ? "Open the deployment to check its run history and continue."
              : "Environment setup did not finish. Review the Variables settings before starting the first release."}
          </p>
          {!created.ready && text && (
            <Disclosure quiet summary="Keep a copy of your environment variables">
              <Textarea
                className="font-mono text-xs"
                aria-label="Unsaved environment variables"
                readOnly
                value={text}
              />
            </Disclosure>
          )}
        </FlowPanelBody>
        <FlowActions>
          <Button asChild className="h-11 sm:h-9">
            <Link
              href={
                created.ready
                  ? `/deploy/${created.projectId}/deployments`
                  : `/deploy/${created.projectId}/settings/variables`
              }
            >
              {created.ready ? "Open deployment" : "Finish environment setup"}
              <ArrowRight className="size-4" />
            </Link>
          </Button>
        </FlowActions>
      </FlowPanel>
    )

  const errors = validateConfiguration(configuration, flow.profile)
  const blockers = preflight ? blockingFindings(preflight.findings) : []
  const warnings = preflight ? warningFindings(preflight.findings) : []
  const outstanding = warnings.filter((finding) => !acknowledged.includes(finding.code))
  const last = current === "review"

  return (
    /* The form on the left and the plan it is building on the right: every
       field changes the drawing beside it, so what a project *is* — a source,
       a build, a container, a name — is on screen while it is being decided
       rather than discovered afterwards on the overview. It is also how a
       four-step sequence stays one thing: the drawing does not change when
       the step does, and each of its nodes goes to the step that decides it. */
    <div className="grid min-w-0 gap-x-6 gap-y-6 xl:grid-cols-[minmax(0,1fr)_17rem]">
      {/* Disabled while a submit is in flight: inputs left editable during the
          async save/preflight round trip could be typed into and then
          silently reverted once the response handler lands (§14). */}
      <fieldset disabled={Boolean(busy)} className="contents">
        <aside className="min-w-0 xl:col-start-2 xl:row-start-1">
          <div className="xl:sticky xl:top-6">
            <PlanWiring
              profile={flow.profile}
              source={flow.source}
              sourceLabel={flow.sourceLabel}
              branch={isGitSource ? branch : undefined}
              framework={flow.candidate?.framework}
              configuration={configuration}
              onOpenSection={openSection}
            />
          </div>
        </aside>

        {/* The one surface on this screen that carries depth (§16): the fields
            are what the reader is deciding, and the drawing beside them is a
            reading of what they already say. Giving the drawing an edge too
            would be two foregrounds, which is none. */}
        <FlowPanel className="relative min-w-0 xl:col-start-1 xl:row-start-1">
          {/* §17 pass 7: the surface says its own work is in flight. A save,
              a re-detect and a preflight all disable the fieldset, and a form
              that greys out with no other answer reads as one that stopped
              responding. */}
          {busy && <BorderBeam duration={3} />}
          {/* Keyed by step, so each screen rises the way a block that has just
              arrived does (§11) rather than swapping in place. */}
          <FlowPanelBody key={current} className="animate-rise space-y-6">
            {failure && <ErrorState error={failure} />}

            {current === "project" && (
              <StepProject
                flow={flow}
                onFlowChange={onFlowChange}
                onChangeProfile={changeProfile}
                branch={branch}
                branchBusy={branchBusy}
                branches={branches.data ?? []}
                onChangeBranch={(ref) => void changeBranch(ref)}
                onPickCandidate={(id) => void pickCandidate(id)}
                busy={busy}
                nameTouched={nameTouched}
                onNameTouched={() => setNameTouched(true)}
                nameCollides={nameCollides}
                errors={errors}
              />
            )}

            {current === "runtime" && (
              <StepRuntime
                flow={flow}
                onFlowChange={onFlowChange}
                errors={errors}
                foldsOpen={initialAdvanced}
              />
            )}

            {current === "variables" && (
              <StepVariables
                flow={flow}
                onFlowChange={onFlowChange}
                rows={envRows}
                onRowsChange={setEnvRows}
                dotenv={dotenv}
                onDotenvChange={setDotenv}
                referencesOpen={declaredVariablesNeedReview(flow)}
              />
            )}

            {current === "review" && (
              <StepReview
                flow={flow}
                branch={isGitSource ? branch : undefined}
                gitPolicy={gitPolicy}
                onGitPolicyChange={setGitPolicy}
                variableCount={sentRows.length}
                findings={preflight?.findings ?? []}
                checking={busy === "check"}
                blockers={blockers}
                warnings={warnings}
                acknowledged={acknowledged}
                onAcknowledgedChange={setAcknowledged}
                onOpenRemedy={openRemedyField}
                canOpenRemedy={(finding) => Boolean(sectionForField(finding.fieldId))}
              />
            )}
          </FlowPanelBody>
          {/* The one gesture that advances, on the foot of the surface holding
              what it is about to change (§16). On the first step there is no
              step behind it, so the way back out of the sequence is the way
              back to the chooser — one control, not two words for it. */}
          <FlowActions
            note={
              !last
                ? undefined
                : isImport
                  ? "Adopting records this workload as a deployment without starting, stopping, or changing it."
                  : "Deploy saves the plan, applies the environment, and starts the release."
            }
            secondary={
              <>
                <Button
                  variant="ghost"
                  className="h-11 text-muted-foreground sm:h-9"
                  onClick={back}
                  disabled={Boolean(busy)}
                >
                  {current === "project" ? "Change source" : "Back"}
                </Button>
                {last && !isImport && (
                  <Button
                    variant="ghost"
                    className="h-11 sm:h-9"
                    onClick={() => void submit("save")}
                    pending={busy === "save"}
                    disabled={Boolean(busy)}
                  >
                    Save only
                  </Button>
                )}
              </>
            }
          >
            {last ? (
              <Button
                className="h-11 sm:h-9"
                onClick={() => void submit("deploy")}
                pending={busy === "deploy"}
                disabled={Boolean(busy)}
              >
                <ArrowRight className="size-4" />
                {isImport
                  ? "Adopt workload"
                  : blockers.length
                    ? "Re-check and deploy"
                    : outstanding.length
                      ? "Acknowledge, then deploy"
                      : "Deploy"}
              </Button>
            ) : (
              <Button className="h-11 sm:h-9" onClick={advance} disabled={Boolean(busy)}>
                <ArrowRight className="size-4" />
                Continue
              </Button>
            )}
          </FlowActions>
        </FlowPanel>
      </fieldset>
    </div>
  )
}
