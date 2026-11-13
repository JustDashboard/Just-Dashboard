"use client"

import { useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { CloudUpload, Download, Plus, Rocket, Trash } from "@/components/icons"
import { ApiError, del, get, getText, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { downloadText } from "@/lib/metrics-export"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  BlueprintServiceSummary,
  DeploymentSummary,
  TemplatePreview,
  UserTemplate,
  UserTemplateDetail,
} from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, Notice } from "@/components/state"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Field, FormNote } from "@/components/form"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph, ProductLogo, imageProduct } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { VerbActions, type Verb } from "@/components/verbs"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import { ProjectMark } from "@/components/deploy/project-mark"
import { useFleet } from "@/components/deploy/section-picture"
import { TemplatePreviewFacts } from "@/components/deploy/template-preview"
import { WireMark } from "@/components/deploy/wire"
import {
  templateDeployHref,
  templateFilename,
  templateId,
  templateRuns,
  templateUse,
} from "@/components/deploy/templates"

/**
 * Templates: the catalogue's own half, the templates added on this dashboard,
 * which the new-project picker offers under Your templates beside the
 * reviewed built-ins. They are drawn as that picker draws them: a card each,
 * with the product its image is, what it runs and how many deployments use
 * it. Choosing a card draws that template over the grid: what it was made
 * from, the template, and each service it runs. A card's own verbs deploy
 * it, export its definition as a file and remove it. A template is added
 * from a pasted Compose file here, from a running deployment's Settings →
 * General (Save as template…), or by importing a definition exported from
 * this or another dashboard.
 */
export function TemplatesPage() {
  const router = useRouter()
  const { can } = useAuth()
  const admin = can("system.admin")
  const templates = usePoll(
    (signal) => get<UserTemplate[]>("/deploy/templates", undefined, signal),
    15000,
    [],
  )
  const fleet = useFleet()
  const [composeOpen, setComposeOpen] = useState(false)
  const [importing, setImporting] = useState(false)
  const [chosen, setChosen] = useState<string>()
  const file = useRef<HTMLInputElement>(null)
  const { confirm, dialog } = useConfirm()
  const list = templates.data
  // The one drawn over the grid: the card last pressed, else the first, so
  // the picture is there from the first paint and survives a removal.
  const selected = list?.find((template) => template.id === chosen) ?? list?.[0]

  const exportTemplate = async (template: UserTemplate) => {
    try {
      const text = await getText(`/deploy/templates/${encodeURIComponent(template.id)}/export`)
      downloadText(templateFilename(template.id), text, "application/json")
    } catch (err) {
      notify.error(`Could not export ${template.name}`, err)
    }
  }

  const importFile = async (picked: File) => {
    setImporting(true)
    try {
      let definition: unknown
      try {
        definition = JSON.parse(await picked.text())
      } catch {
        throw new Error(`${picked.name} is not a JSON file.`)
      }
      const created = await post<UserTemplateDetail>("/deploy/templates/import", definition)
      notify.success(`${created.name} imported`, { description: `Saved as ${created.id}.` })
      setChosen(created.id)
      templates.refresh()
    } catch (err) {
      notify.error("Could not import the template", err)
    } finally {
      setImporting(false)
      if (file.current) file.current.value = ""
    }
  }

  const remove = (template: UserTemplate) =>
    confirm({
      title: `Remove ${template.name}`,
      confirmLabel: "Remove template",
      description: (
        <p>
          It leaves the catalogue, so nothing new can be made from it.{" "}
          {template.deployments > 0
            ? `${templateUse(template.deployments)}, and each keeps running and redeploying from its own copy.`
            : "No deployment uses it."}{" "}
          Export it first to keep the file.
        </p>
      ),
      action: async () => {
        await del(`/deploy/templates/${encodeURIComponent(template.id)}`)
      },
      onDone: () => templates.refresh(),
    })

  const verbsFor = (template: UserTemplate): Verb[] => {
    const usable = template.summary.deploymentSupported !== false
    const verbs: Verb[] = [
      {
        key: "export",
        label: "Export as JSON",
        icon: Download,
        // The one verb a reader who cannot deploy has, so it needs no menu.
        inline: !admin,
        run: () => void exportTemplate(template),
      },
    ]
    if (admin) {
      // A template this dashboard cannot deploy offers no way to try.
      if (usable)
        verbs.unshift({
          key: "deploy",
          label: `Deploy ${template.name}`,
          icon: Rocket,
          inline: true,
          run: () => router.push(templateDeployHref(template.id)),
        })
      verbs.push({
        key: "remove",
        label: "Remove template",
        icon: Trash,
        danger: true,
        run: () => remove(template),
      })
    }
    return verbs
  }

  const chooseFile = () => file.current?.click()
  const inUse = list?.filter((template) => template.deployments > 0).length ?? 0

  return (
    <Page>
      <PageContext
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Templates"
      />
      <input
        ref={file}
        type="file"
        accept="application/json,.json"
        className="hidden"
        aria-label="Template file to import"
        data-testid="template-import-file"
        onChange={(event) => {
          const picked = event.target.files?.[0]
          if (picked) void importFile(picked)
        }}
      />
      {templates.error && !list ? (
        <ErrorState error={templates.error} onRetry={templates.refresh} />
      ) : (
        <>
          {/* No template, no picture: the three cards under it are the page. */}
          {(!list || selected) && (
            <div className="mx-auto w-full max-w-5xl lg:py-2">
              <TemplatePicture template={selected} projects={fleet.projects} />
            </div>
          )}
          <Panel plain>
            <PanelHeader
              title="Your templates"
              actions={
                <span className="flex flex-wrap items-center gap-3">
                  {list && list.length > 0 && (
                    <span className="numeric text-hint text-muted-foreground">
                      {plural(list.length, "template")}
                      {inUse > 0 && ` · ${inUse} in use`}
                    </span>
                  )}
                  {admin && list && list.length > 0 && (
                    <>
                      <Button size="sm" variant="outline" pending={importing} onClick={chooseFile}>
                        <CloudUpload className="size-3.5" />
                        Import…
                      </Button>
                      <Button size="sm" onClick={() => setComposeOpen(true)}>
                        <Plus className="size-3.5" />
                        Add from Compose file
                      </Button>
                    </>
                  )}
                </span>
              }
            />
            <PanelBody>
              {!list ? (
                <ChoiceGrid columns="fill" aria-hidden>
                  {[0, 1, 2].map((card) => (
                    <Skeleton key={card} className="h-[5.75rem] rounded-xl" />
                  ))}
                </ChoiceGrid>
              ) : list.length === 0 ? (
                admin ? (
                  // The empty page is the first question: where it comes from.
                  <ChoiceGrid columns={3} className="animate-rise">
                    <ChoiceCard
                      index={0}
                      logo={<ProductLogo id="docker-compose" />}
                      verb="Add a template from a Compose file"
                      title="From a Compose file"
                      description="Paste a stack: its passwords become secrets each deployment generates."
                      onClick={() => setComposeOpen(true)}
                    />
                    <ChoiceCard
                      index={1}
                      logo={
                        // A project of this server where there is one: it is
                        // what would be saved.
                        fleet.projects[0] ? (
                          <ProjectMark deployment={fleet.projects[0]} />
                        ) : (
                          <ProductLogo fallback={Rocket} />
                        )
                      }
                      verb="Save a deployment as a template"
                      title="From a running deployment"
                      description="Open its Settings → General and press Save as template…"
                      href="/deploy"
                    />
                    <ChoiceCard
                      index={2}
                      logo={
                        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background text-brand">
                          <LogoGlyph className="h-5 w-auto" />
                        </span>
                      }
                      verb="Import a template file"
                      title="Import a file"
                      description="A definition exported from this or another dashboard."
                      onClick={chooseFile}
                    />
                  </ChoiceGrid>
                ) : (
                  <EmptyNote>No templates</EmptyNote>
                )
              ) : (
                <ChoiceGrid
                  columns="fill"
                  role="group"
                  aria-label="Your templates"
                  className="animate-rise"
                >
                  {list.map((template, index) => (
                    <TemplateCard
                      key={template.id}
                      index={index}
                      template={template}
                      selected={template.id === selected?.id}
                      verbs={verbsFor(template)}
                      onSelect={() => setChosen(template.id)}
                    />
                  ))}
                </ChoiceGrid>
              )}
            </PanelBody>
          </Panel>
        </>
      )}

      <ComposeTemplateSheet
        open={composeOpen}
        onOpenChange={setComposeOpen}
        onSaved={(created) => {
          setComposeOpen(false)
          setChosen(created.id)
          templates.refresh()
          notify.success(`${created.name} added`, {
            description: "Choose it under Your templates when you deploy something new.",
            action: {
              label: "Deploy",
              onClick: () => router.push(templateDeployHref(created.id)),
            },
          })
        }}
      />
      {dialog}
    </Page>
  )
}

/**
 * One template, as the new-project picker draws one: the product its public
 * image is, its name, what it runs, and in the line under them what kind it
 * is and how many deployments use it. Pressing it draws it over the grid; its
 * verbs are at the end of that line, so deploying is one press from the card.
 * Every card is one height, used or not, deployable or not.
 */
function TemplateCard({
  template,
  index,
  selected,
  verbs,
  onSelect,
}: {
  template: UserTemplate
  index: number
  selected: boolean
  verbs: Verb[]
  onSelect: () => void
}) {
  const { summary } = template
  const usable = summary.deploymentSupported !== false
  return (
    <ChoiceCard
      index={index}
      // The unnamed group as well, so the dimmed verbs brighten with the
      // card the way they do with a row.
      className="group"
      logo={<ProductLogo id={imageProduct(summary.image)} />}
      title={template.name}
      verb={`Show ${template.name}`}
      description={
        <span className="block truncate font-mono" title={templateRuns(summary)}>
          {templateRuns(summary)}
        </span>
      }
      trailing={
        <span className="mt-auto flex w-full min-w-0 items-center gap-2">
          <Tag>{summary.sourceKind === "compose" ? "Compose stack" : "Image"}</Tag>
          {usable ? (
            template.deployments > 0 && <Tag>Used by {template.deployments}</Tag>
          ) : (
            <Tag tone="warning">Preview only</Tag>
          )}
          <span className="ml-auto flex shrink-0">
            <VerbActions dim verbs={verbs} menuLabel={`Actions for ${template.name}`} />
          </span>
        </span>
      }
      selected={selected}
      onClick={onSelect}
    />
  )
}

const ROLE_WORD: Record<BlueprintServiceSummary["role"], string> = {
  app: "Application",
  worker: "Worker",
  database: "Database",
  cache: "Cache",
}

/**
 * The chosen template, drawn: what it was made from on one side (a Compose
 * file, a deployment of this server, an imported file, with who added it and
 * when), the template in the middle under its id and version with how many
 * deployments use it, and on the other side each service it runs as the
 * product its image is, the one the domain goes to marked. Nothing in it
 * moves: a template is a definition, and nothing travels along its lines
 * until something is deployed from it.
 *
 * It is the only place a template's services are named, so on a phone the
 * three parts stack with every word kept. With no template yet it is the
 * picture's final shape in grey, held until the list answers.
 */
function TemplatePicture({
  template,
  projects,
}: {
  template: UserTemplate | undefined
  projects: DeploymentSummary[]
}) {
  if (!template)
    return (
      <WireFlow
        loading
        start={{ title: "Made from", nodes: [], loadingRows: 1 }}
        hub={{ mark: <Skeleton className="size-12 rounded-xl" />, eyebrow: "Template" }}
        end={{ title: "Runs", nodes: [], loadingRows: 2 }}
      />
    )
  const { summary } = template
  const usable = summary.deploymentSupported !== false
  const added = `${template.createdBy || "this dashboard"} · ${relativeTime(template.createdAt)}`
  const from = projects.find((project) => project.id === template.fromDeploymentId)
  const origin: WireFlowNode =
    template.source === "deployment" && from
      ? {
          id: "origin",
          mark: (size) => <ProjectMark deployment={from} size={size === "sm" ? "sm" : "md"} />,
          eyebrow: "Saved from",
          title: from.name,
          name: from.name,
          hint: added,
          href: `/deploy/${from.id}`,
          label: `Open ${from.name}`,
        }
      : {
          id: "origin",
          mark: (size) =>
            template.source === "compose" ? (
              <WireMark tone="logo" size={size}>
                <ProductGlyph id="docker-compose" />
              </WireMark>
            ) : (
              <WireMark tone="logo" size={size}>
                {template.source === "deployment" ? <Rocket /> : <CloudUpload />}
              </WireMark>
            ),
          eyebrow: "Made from",
          title:
            template.source === "compose"
              ? "A Compose file"
              : template.source === "deployment"
                ? "A deployment"
                : "An imported file",
          hint: added,
        }
  const services = summary.services ?? []
  const runs: WireFlowNode[] =
    services.length > 0
      ? services.map((service) => ({
          id: `service:${service.name}`,
          mark: (size) => (
            <WireMark tone="logo" size={size}>
              <ProductGlyph id={imageProduct(service.image)} />
            </WireMark>
          ),
          eyebrow: service.public ? "Public" : ROLE_WORD[service.role],
          title: <span className="font-mono">{service.name}</span>,
          name: service.name,
          hint: <span className="font-mono break-all">{service.image}</span>,
        }))
      : [
          {
            id: "image",
            mark: (size) => (
              <WireMark tone="logo" size={size}>
                <ProductGlyph id={imageProduct(summary.image)} />
              </WireMark>
            ),
            eyebrow: "Image",
            title: <span className="font-mono break-all">{summary.image}</span>,
            name: summary.image,
            hint: `${summary.memoryMb} MB`,
          },
        ]
  return (
    <WireFlow
      // Keyed by the template, so choosing another one rises in rather than
      // swapping words under the reader.
      key={template.id}
      className="animate-rise"
      hubShare={0.9}
      start={{ title: "Made from", nodes: [origin] }}
      hub={{
        mark: <ProductLogo id={imageProduct(summary.image)} size="lg" />,
        eyebrow: "Template",
        title: template.name,
        hint: (
          <>
            <span className="block font-mono">
              {template.id} · v{template.version}
            </span>
            <span className={cn("block", !usable && "text-warning")}>
              {usable
                ? templateUse(template.deployments)
                : summary.unavailableReason || "This dashboard cannot deploy it"}
            </span>
          </>
        ),
        className: "lg:pb-16",
      }}
      end={{ title: "Runs", nodes: runs, max: 4 }}
    />
  )
}

/**
 * The Compose file's form: a name, the file, and the two things the file may
 * not make plain — the service the domain goes to, and the path that says it
 * is up. Check shows what the template will ask before it is added; a
 * refusal is said under the form, in the catalogue's own words.
 */
function ComposeTemplateSheet({
  open,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: (created: UserTemplateDetail) => void
}) {
  const [name, setName] = useState("")
  const [compose, setCompose] = useState("")
  const [publicService, setPublicService] = useState("")
  const [healthPath, setHealthPath] = useState("")
  const [preview, setPreview] = useState<TemplatePreview>()
  const [problem, setProblem] = useState<string>()
  const [busy, setBusy] = useState<"check" | "save">()
  const id = templateId(name)
  const ready = Boolean(id) && compose.trim().length > 0

  const body = (dryRun: boolean) => ({
    name: name.trim(),
    compose,
    publicService: publicService.trim() || undefined,
    healthPath: healthPath.trim() || undefined,
    dryRun: dryRun || undefined,
  })
  const failed = (err: unknown, title: string) => {
    if (err instanceof ApiError && err.status < 500) setProblem(err.message)
    else notify.error(title, err)
  }
  const check = async () => {
    setBusy("check")
    setProblem(undefined)
    try {
      setPreview(await post<TemplatePreview>("/deploy/templates", body(true)))
    } catch (err) {
      setPreview(undefined)
      failed(err, "Could not read the Compose file")
    } finally {
      setBusy(undefined)
    }
  }
  const save = async () => {
    setBusy("save")
    setProblem(undefined)
    try {
      const created = await post<UserTemplateDetail>("/deploy/templates", body(false))
      setName("")
      setCompose("")
      setPublicService("")
      setHealthPath("")
      setPreview(undefined)
      onSaved(created)
    } catch (err) {
      failed(err, "Could not add the template")
    } finally {
      setBusy(undefined)
    }
  }
  // An edit makes the last check's answer stale.
  const edit = (apply: () => void) => {
    apply()
    setPreview(undefined)
    setProblem(undefined)
  }

  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title="Add from Compose file"
      description="A template made from a pasted Compose file, validated as a built-in template is."
      width="lg"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={Boolean(busy)}>
            Cancel
          </Button>
          <Button
            variant="outline"
            pending={busy === "check"}
            disabled={!ready || Boolean(busy)}
            onClick={() => void check()}
          >
            Check
          </Button>
          <Button
            pending={busy === "save"}
            disabled={!ready || Boolean(busy) || preview?.exists}
            onClick={() => void save()}
          >
            Add template
          </Button>
        </>
      }
    >
      <div className="min-w-0 space-y-4">
        <Field
          label="Name"
          htmlFor="template-name"
          hint={id ? `Saved as ${id}` : "Letters or digits; the id is made from it."}
        >
          <Input
            id="template-name"
            value={name}
            onChange={(event) => edit(() => setName(event.target.value))}
            autoComplete="off"
            maxLength={80}
          />
        </Field>
        <Field
          label="Compose file"
          htmlFor="template-compose"
          hint="Every service needs an image and a healthcheck; one publishes the port the domain is routed to."
        >
          <Textarea
            id="template-compose"
            value={compose}
            onChange={(event) => edit(() => setCompose(event.target.value))}
            rows={14}
            spellCheck={false}
            className="font-mono text-body"
            placeholder={'services:\n  web:\n    image: nginx:1.27-alpine\n    ports: ["80:80"]'}
          />
        </Field>
        <div className="grid min-w-0 gap-4 sm:grid-cols-2">
          <Field
            label="Public service"
            htmlFor="template-public"
            hint="When more than one service publishes a port."
          >
            <Input
              id="template-public"
              value={publicService}
              onChange={(event) => edit(() => setPublicService(event.target.value))}
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
          </Field>
          <Field
            label="Health path"
            htmlFor="template-health"
            hint="An HTTP path that answers once it is up; empty checks the port."
          >
            <Input
              id="template-health"
              value={healthPath}
              onChange={(event) => edit(() => setHealthPath(event.target.value))}
              autoComplete="off"
              spellCheck={false}
              placeholder="/"
              className="font-mono"
            />
          </Field>
        </div>
        {problem && (
          <Notice tone="danger" title="The catalogue refuses this file">
            {problem}
          </Notice>
        )}
        {preview?.exists && (
          <Notice tone="warning" title={`${preview.definition.id} already exists`}>
            Give this template another name, or remove the one that has it.
          </Notice>
        )}
        {preview && <TemplatePreviewFacts definition={preview.definition} />}
        {!preview && !problem && (
          <FormNote>
            Check reads the file as the template it would make, and stores nothing.
          </FormNote>
        )}
      </div>
    </SidePanel>
  )
}
