"use client"

import { useEffect, useRef } from "react"
import type {
  DeploymentBuildMethod,
  DeploymentDraftSource,
  DeploymentRecipe,
  GitHubBranch,
  NodePackageManager,
  WorkloadProfile,
} from "@/lib/types"
import { Disclosure, Field, FieldRow, FormFact, FormFacts, FormSection } from "@/components/form"
import { OptionList, OptionRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { ProductGlyph, frameworkProduct } from "@/components/product-logo"
import { Well } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  DEPLOYMENT_NAME,
  RECIPE_LABELS,
  SOURCE_KIND_LABELS,
  frameworkLabel,
  humanize,
} from "@/components/deploy/vocabulary"
import {
  DOTNET_VERSIONS,
  JAVA_VERSIONS,
  automaticPackageManagerHint,
  candidateBlocker,
  commandsForPackageManager,
  composeSourceForCandidate,
  dockerfileStageHint,
  dotnetVersionReading,
  GO_VERSIONS,
  goMainPackageList,
  javaVersionReading,
  packageManagerOptions,
  packageManagerReading,
} from "@/components/deploy/deployment-defaults"
import type { WizardErrors } from "@/components/deploy/deployment-defaults"
import { BuildExtras } from "@/components/deploy/new-project/configure-advanced"
import {
  candidateStanding,
  lfsFilesForRoot,
  rootEditCandidate,
  submodulesForRoot,
} from "@/components/deploy/new-project/draft"
import type { ConfigureFlow, FlowUpdate } from "@/components/deploy/new-project/draft"
import { SECTION_IDS } from "@/components/deploy/new-project/plan-sections"

/** The three ways a repository becomes an image, in the order they are tried. */
const BUILD_METHODS: [DeploymentBuildMethod, string][] = [
  ["recipe", "Automatic recipe"],
  ["dockerfile", "Dockerfile"],
  ["static", "Static files"],
]

/**
 * Step one of the four Configure was split into: **what is this project, and
 * what is it built from.**
 *
 * The source controls and the name used to open a screen that went on for
 * another eight sections, so the two answers only this reader can give — what
 * to call it and what kind of thing it is — sat at the top of forty controls
 * they had to scroll past to reach the button. Here they are the screen.
 *
 * The build settings stay a fold, and for the same reason they always were:
 * detection answers them, and it is right often enough that opening them by
 * default would be asking a question that has already been answered. It opens
 * itself when detection could *not* answer — no candidate, unreadable
 * evidence, a static site with no output directory — which is also when this
 * is the step the flow lands on.
 */
export function StepProject({
  flow,
  onFlowChange,
  onChangeProfile,
  branch,
  branchBusy,
  branches,
  onChangeBranch,
  onEditBranch,
  onChangeCloneOptions,
  onPickCandidate,
  onDeployAsCompose,
  busy,
  nameTouched,
  onNameTouched,
  nameCollides,
  errors,
}: {
  flow: ConfigureFlow
  onFlowChange: (next: FlowUpdate) => void
  onChangeProfile: (profile: WorkloadProfile) => void
  branch: string
  branchBusy: boolean
  branches: GitHubBranch[]
  onChangeBranch: (ref: string) => void
  onEditBranch: (ref: string) => void
  /** Re-saves the source with submodules or LFS switched, and detects again. */
  onChangeCloneOptions: (
    options: Pick<DeploymentDraftSource, "includeSubmodules" | "includeLfs">,
  ) => void
  onPickCandidate: (id: string) => void
  /** Re-reads the repository as a Compose source, the only way its services build. */
  onDeployAsCompose: () => void
  busy: string
  nameTouched: boolean
  onNameTouched: () => void
  nameCollides: boolean
  errors: WizardErrors
}) {
  const configuration = flow.configuration
  const setConfiguration = (next: typeof configuration) =>
    onFlowChange({ ...flow, configuration: next })
  const updateBuild = (patch: Partial<typeof configuration.build>) =>
    setConfiguration({ ...configuration, build: { ...configuration.build, ...patch } })

  // A root typed after detection is judged by what detection found there: the
  // candidate at that root, building the plan's way, is picked the way one is
  // picked from the list, so its facts — not another directory's — are what
  // preflight reads. Settled on a pause in the typing, not every keystroke,
  // and tried once per candidate: a pick that fails is not retried in a loop.
  const rootPickId = rootEditCandidate(flow.detection, flow.candidate, configuration.build)?.id
  const pickCandidate = useRef(onPickCandidate)
  useEffect(() => {
    pickCandidate.current = onPickCandidate
  })
  const rootPickTried = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (!rootPickId) rootPickTried.current = undefined
    if (!rootPickId || rootPickTried.current === rootPickId || busy) return
    const timer = setTimeout(() => {
      rootPickTried.current = rootPickId
      pickCandidate.current(rootPickId)
    }, 600)
    return () => clearTimeout(timer)
  }, [rootPickId, busy])

  const isGitSource = flow.source.kind === "git" || flow.source.kind === "local"
  const isImageSource = flow.source.kind === "image"
  const nameInvalid = nameTouched && !DEPLOYMENT_NAME.test(flow.name)
  /**
   * The profile is plan-time intent — preflight reads it to decide whether
   * blue/green is eligible and whether a readiness check is required, and
   * nothing in the executor reads it at all. It used to be offered for a Git
   * source only, which is why an HTTP application shipped as a container
   * could not be told it was one: it deployed stop-first, with no health
   * gate and no finding to say so.
   */
  const profileOptions = isGitSource
    ? [
        { value: "web", label: "Web application" },
        { value: "static", label: "Static website" },
        { value: "worker", label: "Worker or bot" },
        { value: "service", label: "Service" },
      ]
    : isImageSource
      ? [
          { value: "image", label: "Container, as published" },
          { value: "web", label: "Web application" },
          { value: "worker", label: "Worker or bot" },
        ]
      : []

  const candidates = flow.detection?.candidates ?? []
  const ambiguous = candidates.length > 1
  const requirements = flow.detection?.gitRequirements
  const candidateRoot = flow.candidate?.root ?? ""
  const submodules = submodulesForRoot(requirements, candidateRoot)
  const lfsFiles = lfsFilesForRoot(requirements, candidateRoot)
  const alternative = flow.detection?.alternatives?.[0]
  const setAside = flow.detection?.setAside ?? []
  // A release command is not a project of its own: it runs as a release task
  // in the image (Advanced), and preflight names one nothing runs.
  const processes = (flow.candidate?.processes ?? []).filter(
    (process) => process.kind !== "release",
  )

  /**
   * What the build fold holds, said while it is shut.
   *
   * Deliberately not the method and the framework: the plan drawing beside
   * the form already reads those back, and a fold whose summary repeats the
   * panel next to it has told the reader nothing. What is only inside here is
   * the commands and the directories, which is also the pair most likely to
   * be wrong on a monorepo.
   */
  const buildFacts =
    configuration.build.method === "dockerfile"
      ? `${configuration.build.dockerfile || "Dockerfile"}${
          configuration.build.target ? ` · stage ${configuration.build.target}` : ""
        }`
      : [
          configuration.build.buildCommand,
          configuration.build.startCommand,
          configuration.build.outputDirectory && `serves ${configuration.build.outputDirectory}`,
          configuration.build.rootDirectory && `in ${configuration.build.rootDirectory}`,
        ]
          .filter(Boolean)
          .join(" · ") || "Detected defaults"

  const extraFacts =
    [
      (configuration.build.releaseTasks?.length ?? 0) > 0 &&
        `${configuration.build.releaseTasks!.length} release tasks`,
      (configuration.build.secrets?.length ?? 0) > 0 &&
        `${configuration.build.secrets!.length} build secrets`,
      configuration.build.targetPlatform,
      configuration.build.noCache && "without the cache",
    ]
      .filter(Boolean)
      .join(" · ") || "Platform, cache, release tasks, build secrets"
  const frameworkMark = frameworkProduct(flow.candidate?.framework)

  return (
    <>
      <FormSection
        id={SECTION_IDS.source}
        title="Source"
        /* What detection made of it, at the section's edge rather than
           floating beside the branch control — a tag annotates the thing it
           sits at the end of (§4). Untinted: a framework is a fixed property,
           and green is a reading of state (§3). Its own mark goes before the
           word, the one the plan drawing's Build node carries. */
        actions={
          flow.candidate?.framework && (
            <span className="inline-flex items-center gap-1.5">
              {frameworkMark && <ProductGlyph id={frameworkMark} />}
              <Tag>{frameworkLabel(flow.candidate.framework)}</Tag>
            </span>
          )
        }
      >
        {/* The source's own name and mark are the first node of the plan
            drawing, so neither is repeated here: this section is the controls
            that change it. */}
        {isGitSource && (
          <Field
            label="Branch"
            htmlFor="source-branch"
            hint="The commit at its head is what this first release builds."
            className="sm:max-w-xs"
          >
            {flow.githubRepo ? (
              <Select value={branch} onValueChange={onChangeBranch}>
                <SelectTrigger id="source-branch" className="w-full font-mono">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {branches.map((entry) => (
                    <SelectItem key={entry.name} value={entry.name}>
                      {entry.name}
                      {entry.default ? " (default)" : ""}
                    </SelectItem>
                  ))}
                  {!branches.some((entry) => entry.name === branch) && (
                    <SelectItem value={branch}>{branch}</SelectItem>
                  )}
                </SelectContent>
              </Select>
            ) : (
              <Input
                id="source-branch"
                value={branch}
                disabled={branchBusy}
                className="font-mono"
                onChange={(event) => onEditBranch(event.target.value)}
                onBlur={(event) => onChangeBranch(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key !== "Enter") return
                  event.preventDefault()
                  onChangeBranch(event.currentTarget.value)
                }}
              />
            )}
          </Field>
        )}
        {!isGitSource && (
          /* Data rather than a caption (§5): a non-git source has no control
             on this screen — changing it is the way out at the foot — so what
             the section holds is what the source *is*. */
          <FormFacts>
            <FormFact label="From" mono>
              {flow.sourceLabel}
            </FormFact>
            <FormFact label="Kind">{SOURCE_KIND_LABELS[flow.source.kind]}</FormFact>
          </FormFacts>
        )}
        {/* Submodules and LFS objects are part of what gets built, so they
            are switched here, beside the branch, rather than only on the
            source picker the reader has already left. Detection turns them on
            by itself when the build root needs them and nothing else does. */}
        {isGitSource && (requirements?.submodules || requirements?.lfs) && (
          <OptionList>
            {requirements.submodules && (
              <OptionRow
                title="Clone the submodules listed in .gitmodules"
                hint={
                  submodules.length
                    ? `${submodules.map((submodule) => submodule.path).join(", ")}${
                        submodules.some((submodule) => !submodule.sameSource)
                          ? " — one is on another host; the source's credential must reach it"
                          : ""
                      }`
                    : "None of them is inside the directory this project builds."
                }
                checked={Boolean(flow.source.includeSubmodules)}
                disabled={branchBusy}
                onCheckedChange={(includeSubmodules) => onChangeCloneOptions({ includeSubmodules })}
              />
            )}
            {requirements.lfs && (
              <OptionRow
                title="Download Git LFS objects, not their pointer files"
                hint={
                  lfsFiles === undefined
                    ? `The repository keeps ${requirements.lfsFiles} files in LFS, more than detection lists; some may be under the build root. The server needs git-lfs installed.`
                    : lfsFiles > 0
                      ? `${lfsFiles} file${lfsFiles === 1 ? "" : "s"} under the build root ${
                          lfsFiles === 1 ? "is" : "are"
                        } stored in LFS. The server needs git-lfs installed.`
                      : "No file under the build root is stored in LFS."
                }
                checked={Boolean(flow.source.includeLfs)}
                disabled={branchBusy}
                onCheckedChange={(includeLfs) => onChangeCloneOptions({ includeLfs })}
              />
            )}
          </OptionList>
        )}
        {alternative && (
          <Notice
            title={
              alternative.kind === "template"
                ? `${alternative.label} has a reviewed template`
                : `The project publishes ${alternative.label}`
            }
          >
            {alternative.evidence}. Building this repository runs its development tree instead.{" "}
            {/* A full navigation, not a client route: the page takes its
                source tab and selection from the address only on arrival. */}
            <a
              href={`/deploy/new?source=${alternative.kind === "template" ? "template" : "image"}&${
                alternative.kind === "template" ? "template" : "image"
              }=${encodeURIComponent(alternative.ref)}`}
              className="rounded-sm underline underline-offset-2 focus-ring"
            >
              {alternative.kind === "template" ? "Deploy the template" : "Deploy the image"}
            </a>
          </Notice>
        )}
        {flow.detection?.unavailable && (
          <Notice tone="warning" title="Some evidence is unavailable">
            {flow.detection.unavailable}
          </Notice>
        )}
        {/* What detection could not settle for itself. An image carries this
            for the command, the storage and the readiness it cannot read from
            a registry manifest — and a source carrying one of these is a
            source that lands the reader on this step. */}
        {(flow.candidate?.needsDecision?.length ?? 0) > 0 && (
          <Notice title="Detection could not answer everything">
            <ul className="list-disc space-y-1 pl-4">
              {flow.candidate!.needsDecision.map((decision) => (
                <li key={decision}>{humanize(decision)}</li>
              ))}
            </ul>
          </Notice>
        )}
        {/* A Compose file found in a repository is only named here; nothing
            analyses its services until the repository is read as a Compose
            source, and a plan built from it cannot build. */}
        {composeSourceForCandidate(flow.source, flow.candidate) && (
          <Notice tone="warning" title="This Compose file has not been analysed">
            <p>
              Its services, images and builds are read when the repository is deployed as a Compose
              stack.
            </p>
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="mt-2"
              disabled={busy === "detect"}
              onClick={onDeployAsCompose}
            >
              Deploy as a Compose stack
            </Button>
          </Notice>
        )}
        {processes.length > 0 && (
          <Disclosure quiet summary={`Other processes this source runs · ${processes.length}`}>
            <ul className="space-y-1.5 text-hint">
              {processes.map((process) => (
                <li key={`${process.kind}-${process.name}`}>
                  <span className="font-medium text-foreground">{process.name}</span>
                  {process.command && (
                    <>
                      {" "}
                      <code className="font-mono">{process.command}</code>
                    </>
                  )}
                  {" — "}
                  {process.reason}
                </li>
              ))}
            </ul>
            <p className="pt-2 text-hint">
              A project runs one process. Each of these needs a project of its own from this
              repository, with its command as the start command.
            </p>
          </Disclosure>
        )}
        {setAside.length > 0 && (
          <Disclosure quiet summary={`Not offered · ${setAside.length}`}>
            <ul className="space-y-1.5 text-hint">
              {setAside.map((item) => (
                <li key={`${item.kind}-${item.path}`}>
                  <span className="font-mono">{item.path}</span> — {item.reason}
                </li>
              ))}
            </ul>
          </Disclosure>
        )}
        {/* The files behind every default above, so a proposal can be
            checked rather than taken on trust: which lockfile matched, which
            script starts the server. */}
        {(flow.candidate?.evidence?.length ?? 0) > 0 && (
          <Disclosure quiet summary="What detection read">
            <ul className="space-y-1 text-hint">
              {flow.candidate!.evidence.map((item, index) => (
                <li key={`${item.path}-${index}`} className="min-w-0 break-words">
                  <span className="font-mono">{item.path}</span>
                  <span className="text-muted-foreground"> — {item.reason}</span>
                </li>
              ))}
            </ul>
          </Disclosure>
        )}
        {flow.detection?.compose && (
          <div className="space-y-2 pt-1">
            <div className="flex flex-wrap gap-1.5">
              {flow.detection.compose.services.map((service) => (
                <Tag key={service.name} mono>
                  {service.name}
                </Tag>
              ))}
            </div>
            {flow.detection.compose.services.length > 1 && (
              <Field
                label="Primary service"
                htmlFor="compose-primary-service"
                hint="Readiness and the release's container follow it. Detection picks one that builds or publishes a port, never a database."
              >
                <Select
                  value={
                    configuration.build.primaryService ??
                    flow.detection.compose.primaryService ??
                    flow.detection.compose.services[0].name
                  }
                  onValueChange={(value) =>
                    updateBuild({
                      primaryService:
                        value === flow.detection?.compose?.primaryService ? undefined : value,
                    })
                  }
                >
                  <SelectTrigger id="compose-primary-service" className="w-full font-mono">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {flow.detection.compose.services.map((service) => (
                      <SelectItem key={service.name} value={service.name} className="font-mono">
                        {service.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
            {flow.detection.compose.unsupported.map((item) => (
              <Notice key={item} tone="warning" title="Needs explicit review">
                {item}
              </Notice>
            ))}
            <Disclosure quiet summary="Effective Compose plan">
              <Well className="max-h-72 text-hint whitespace-pre-wrap">
                {flow.detection.compose.preview}
              </Well>
            </Disclosure>
          </div>
        )}
      </FormSection>

      {ambiguous && (
        <FormSection
          title="Choose the detected candidate"
          hint={
            flow.detection?.selectionReason ??
            (flow.detection?.selectedId
              ? "Ranked, with the application first. Pick another if the checked one is not it."
              : "Multiple equally strong roots or build methods were found.")
          }
        >
          <OptionList role="group" aria-label="Detected candidates">
            {candidates.map((item) => {
              // What stops a candidate building is the reason to pick another
              // one, so it is said in the row instead of after Deploy.
              const blocker = candidateBlocker(item)
              return (
                <OptionRow
                  key={item.id}
                  title={item.name}
                  tone={blocker ? "warning" : "default"}
                  hint={[
                    `${humanize(item.confidence)} confidence · ${
                      item.framework ? `${item.framework} via ` : ""
                    }${humanize(item.buildMethod)}${
                      item.root && item.root !== "." ? ` in ${item.root}` : ""
                    }`,
                    candidateStanding(item),
                    blocker && `cannot build as detected: ${blocker}`,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                  checked={item.id === flow.detection?.selectedId}
                  onCheckedChange={(checked) => checked && onPickCandidate(item.id)}
                  disabled={busy === "detect"}
                />
              )
            })}
          </OptionList>
        </FormSection>
      )}

      <FormSection title="Project">
        <FieldRow columns={2}>
          <Field
            label="Project name"
            htmlFor="deployment-name"
            hint="Used in URLs, container labels and release history."
            error={
              nameInvalid
                ? "Start with a letter or number. Use up to 64 letters, numbers, dots, dashes, or underscores."
                : nameCollides
                  ? "A project already has this name. Choose another before deploying."
                  : undefined
            }
          >
            <Input
              id="deployment-name"
              required
              maxLength={64}
              autoComplete="off"
              aria-invalid={nameInvalid || nameCollides}
              value={flow.name}
              onBlur={onNameTouched}
              onChange={(event) => onFlowChange({ ...flow, name: event.target.value })}
            />
          </Field>
          {profileOptions.length > 0 && (
            <Field
              label="Project type"
              htmlFor="workload-type"
              hint={
                isImageSource
                  ? "Calling an image a web application is what earns it a health gate and a release with no downtime."
                  : undefined
              }
            >
              <Select
                value={flow.profile}
                onValueChange={(value) => onChangeProfile(value as WorkloadProfile)}
              >
                <SelectTrigger id="workload-type" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {profileOptions.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )}
        </FieldRow>
      </FormSection>

      {isGitSource && (
        <Disclosure
          id={SECTION_IDS.build}
          open={
            !flow.candidate ||
            Boolean(flow.detection?.unavailable) ||
            (flow.profile === "static" &&
              configuration.build.method === "recipe" &&
              !configuration.build.outputDirectory)
          }
          facts={buildFacts}
          // No framework in the head: the Source section's tag and the plan's
          // Build node already name it, and this made it four times on screen.
          summary="Build & output settings"
        >
          {/* Three groups, not one auto-flowing grid. With every field in one
              `grid-cols-2` the pairs that land side by side are whichever ones
              the conditionals happen to leave adjacent — add a package manager
              and "Build command" slides under "Output directory", so the same
              fold reads as a different form on two repositories. Grouped, the
              reader gets the pipeline in the order it runs: what builds it,
              what it runs, where the files are. */}
          <div className="space-y-5">
            <FieldRow columns={2}>
              <Field label="Build method" htmlFor="build-method" error={errors.buildMethod}>
                <Select
                  value={configuration.build.method}
                  onValueChange={(value) => updateBuild({ method: value as DeploymentBuildMethod })}
                >
                  <SelectTrigger id="build-method" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {BUILD_METHODS.map(([method, label]) => (
                      <SelectItem key={method} value={method}>
                        {label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              {configuration.build.method === "recipe" && (
                <Field label="Language" htmlFor="recipe">
                  <Select
                    value={configuration.build.recipe ?? "node"}
                    onValueChange={(value) => {
                      const recipe = value as DeploymentRecipe
                      updateBuild({
                        recipe,
                        goVersion: recipe === "go" ? configuration.build.goVersion : undefined,
                        goPackage: recipe === "go" ? configuration.build.goPackage : undefined,
                        cargoBin: recipe === "rust" ? configuration.build.cargoBin : undefined,
                        pythonVersion:
                          recipe === "python" ? configuration.build.pythonVersion : undefined,
                        javaVersion:
                          recipe === "java" ? configuration.build.javaVersion : undefined,
                        dotnetVersion:
                          recipe === "dotnet" ? configuration.build.dotnetVersion : undefined,
                        // The PHP recipe's asset stage installs through the
                        // same Node install, so the choice survives the move.
                        packageManager:
                          recipe === "node" || recipe === "php"
                            ? configuration.build.packageManager
                            : undefined,
                      })
                    }}
                  >
                    <SelectTrigger id="recipe" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {/* Every recipe the builder actually has a toolchain
                          for, from the one list the product keeps. Three of
                          the eight were offered here, so a Rust or PHP
                          repository detection read correctly could not be
                          corrected when it read wrongly. */}
                      {RECIPE_LABELS.map(([recipe, label]) => (
                        <SelectItem key={recipe} value={recipe}>
                          {label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              )}
              {configuration.build.method === "recipe" &&
                (configuration.build.recipe === "node" ||
                  (configuration.build.recipe === "php" &&
                    (flow.candidate?.nodeInstalls?.length ?? 0) > 0)) && (
                  <Field
                    label="Package manager"
                    htmlFor="package-manager"
                    hint={
                      packageManagerReading(flow.candidate, configuration.build.packageManager) ??
                      ((flow.candidate?.packageManagers?.length ?? 0) > 1 &&
                      !flow.candidate?.packageManager
                        ? `This repository has lockfiles for ${flow.candidate?.packageManagers?.join(" and ")}. Choose the one it uses.`
                        : "Leave on the lockfile unless the repository has more than one.")
                    }
                  >
                    <Select
                      value={configuration.build.packageManager ?? "lockfile"}
                      onValueChange={(value) => {
                        const packageManager =
                          value === "lockfile" ? undefined : (value as NodePackageManager)
                        // The PHP recipe's commands are PHP's; only the asset
                        // stage follows the manager, and it names its own.
                        updateBuild(
                          configuration.build.recipe === "php"
                            ? { packageManager }
                            : {
                                packageManager,
                                ...commandsForPackageManager(
                                  flow.candidate,
                                  configuration.build,
                                  packageManager,
                                ),
                              },
                        )
                      }}
                    >
                      <SelectTrigger id="package-manager" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem
                          value="lockfile"
                          hint={automaticPackageManagerHint(flow.candidate)}
                        >
                          From the lockfile
                        </SelectItem>
                        {packageManagerOptions(flow.candidate).map((option) => (
                          <SelectItem
                            key={option.value}
                            value={option.value}
                            hint={option.hint}
                            disabled={option.disabled}
                          >
                            {option.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                )}
              {configuration.build.method === "recipe" && configuration.build.recipe === "go" && (
                <Field
                  label="Go main package"
                  htmlFor="go-package"
                  hint={
                    (flow.candidate?.goMainPackages?.length ?? 0) > 1
                      ? `This module has several commands: ${goMainPackageList(flow.candidate)}. Choose the one to build.`
                      : "Leave empty to build the module's only command."
                  }
                >
                  <Input
                    id="go-package"
                    value={configuration.build.goPackage ?? ""}
                    onChange={(event) =>
                      updateBuild({
                        goPackage: event.target.value.replace(/^\.\//, "") || undefined,
                      })
                    }
                    placeholder={flow.candidate?.goPackage ?? "cmd/api"}
                    className="font-mono"
                  />
                </Field>
              )}
              {configuration.build.method === "recipe" &&
                configuration.build.recipe === "rust" &&
                (flow.candidate?.rust?.binaries?.length ?? 0) > 1 && (
                  <Field
                    label="Rust binary"
                    htmlFor="cargo-bin"
                    hint={
                      flow.candidate?.rust?.binary
                        ? `This crate builds ${flow.candidate.rust.binaries?.join(", ")}; ${flow.candidate.rust.binary} is served unless you choose another.`
                        : `This crate builds ${flow.candidate?.rust?.binaries?.join(", ")}. Choose the one to serve.`
                    }
                  >
                    <Input
                      id="cargo-bin"
                      value={configuration.build.cargoBin ?? ""}
                      onChange={(event) =>
                        updateBuild({ cargoBin: event.target.value.trim() || undefined })
                      }
                      placeholder={flow.candidate?.rust?.binary || "server"}
                      className="font-mono"
                    />
                  </Field>
                )}
              {configuration.build.method === "recipe" && configuration.build.recipe === "go" && (
                <Field
                  label="Go version"
                  htmlFor="go-version"
                  hint="Leave empty to use .go-version or go.mod."
                >
                  <Input
                    id="go-version"
                    value={configuration.build.goVersion ?? ""}
                    onChange={(event) => updateBuild({ goVersion: event.target.value })}
                    placeholder={GO_VERSIONS.at(-1)}
                  />
                </Field>
              )}
              {configuration.build.method === "recipe" &&
                configuration.build.recipe === "python" && (
                  <Field
                    label="Python version"
                    htmlFor="python-version"
                    hint="Leave empty to use .python-version, runtime.txt, .tool-versions, Pipfile or pyproject.toml."
                    error={errors.pythonVersion}
                  >
                    <Input
                      id="python-version"
                      value={configuration.build.pythonVersion ?? ""}
                      onChange={(event) => updateBuild({ pythonVersion: event.target.value })}
                      placeholder="3.13"
                    />
                  </Field>
                )}
              {configuration.build.method === "recipe" && configuration.build.recipe === "java" && (
                <Field
                  label="Java version"
                  htmlFor="java-version"
                  hint={
                    javaVersionReading(flow.candidate) ??
                    "Leave on the build files to use what pom.xml, the Gradle scripts or a version file declare."
                  }
                  error={errors.javaVersion}
                >
                  <Select
                    value={configuration.build.javaVersion ?? "auto"}
                    onValueChange={(value) =>
                      updateBuild({ javaVersion: value === "auto" ? undefined : value })
                    }
                  >
                    <SelectTrigger id="java-version" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="auto">From the build files</SelectItem>
                      {JAVA_VERSIONS.map((version) => (
                        <SelectItem key={version} value={version}>
                          Java {version}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              )}
              {configuration.build.method === "recipe" &&
                configuration.build.recipe === "dotnet" && (
                  <Field
                    label=".NET version"
                    htmlFor="dotnet-version"
                    hint={
                      dotnetVersionReading(flow.candidate) ??
                      "Leave on the project to use its target framework and global.json."
                    }
                    error={errors.dotnetVersion}
                  >
                    <Select
                      value={configuration.build.dotnetVersion ?? "auto"}
                      onValueChange={(value) =>
                        updateBuild({ dotnetVersion: value === "auto" ? undefined : value })
                      }
                    >
                      <SelectTrigger id="dotnet-version" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="auto">From the project</SelectItem>
                        {DOTNET_VERSIONS.map((version) => (
                          <SelectItem key={version} value={version}>
                            .NET {version}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                )}
              {configuration.build.method === "dockerfile" && (
                <Field label="Dockerfile path" htmlFor="dockerfile">
                  <Input
                    id="dockerfile"
                    className="font-mono"
                    value={configuration.build.dockerfile ?? "Dockerfile"}
                    // A stage belongs to the file it was read from; another
                    // file keeping it would fail with "not a stage".
                    onChange={(event) =>
                      updateBuild({ dockerfile: event.target.value, target: undefined })
                    }
                  />
                </Field>
              )}
              {configuration.build.method === "dockerfile" && (
                <Field
                  label="Stage"
                  htmlFor="dockerfile-target"
                  hint={dockerfileStageHint(flow.candidate?.dockerfileStages)}
                  error={errors.target}
                >
                  <Input
                    id="dockerfile-target"
                    className="font-mono"
                    value={configuration.build.target ?? ""}
                    onChange={(event) =>
                      updateBuild({ target: event.target.value.trim() || undefined })
                    }
                    placeholder="the last stage"
                  />
                </Field>
              )}
            </FieldRow>

            {configuration.build.method !== "dockerfile" && (
              <FieldRow columns={2}>
                <Field label="Build command" htmlFor="build-command">
                  <Input
                    id="build-command"
                    value={configuration.build.buildCommand ?? ""}
                    onChange={(event) => updateBuild({ buildCommand: event.target.value })}
                    placeholder="npm run build"
                    className="font-mono"
                  />
                </Field>
                <Field
                  label="Start command"
                  htmlFor="start-command"
                  hint="Leave empty and set an output directory to serve static files instead."
                >
                  <Input
                    id="start-command"
                    value={configuration.build.startCommand ?? ""}
                    onChange={(event) => updateBuild({ startCommand: event.target.value })}
                    placeholder="npm run start"
                    className="font-mono"
                  />
                </Field>
              </FieldRow>
            )}

            <FieldRow columns={2}>
              <Field label="Root directory" htmlFor="root-directory" hint="For a monorepo.">
                <Input
                  id="root-directory"
                  value={configuration.build.rootDirectory ?? ""}
                  onChange={(event) => updateBuild({ rootDirectory: event.target.value })}
                  placeholder="apps/web"
                  className="font-mono"
                />
              </Field>
              <Field
                label="Output directory"
                htmlFor="output-directory"
                hint="Set only for a site with no server process."
              >
                <Input
                  id="output-directory"
                  value={configuration.build.outputDirectory ?? ""}
                  onChange={(event) => updateBuild({ outputDirectory: event.target.value })}
                  placeholder="dist"
                  className="font-mono"
                />
              </Field>
            </FieldRow>

            {(configuration.build.method === "static" ||
              (configuration.build.method === "recipe" &&
                Boolean(configuration.build.outputDirectory))) && (
              <OptionRow
                title="Single-page application"
                checked={configuration.build.spaFallback ?? false}
                onCheckedChange={(spaFallback) =>
                  updateBuild({ spaFallback: spaFallback || undefined })
                }
              />
            )}
          </div>
        </Disclosure>
      )}

      {isGitSource && (
        <Disclosure summary="Build secrets & release tasks" facts={extraFacts}>
          <BuildExtras configuration={configuration} onChange={setConfiguration} errors={errors} />
        </Disclosure>
      )}
    </>
  )
}
