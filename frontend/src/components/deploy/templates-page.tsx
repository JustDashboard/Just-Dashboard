"use client"

import { useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { CloudUpload, Download, FileText, Plus, Rocket, Trash } from "@/components/icons"
import { ApiError, del, get, getText, post } from "@/lib/api"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { downloadText } from "@/lib/metrics-export"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { TemplatePreview, UserTemplate, UserTemplateDetail } from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormNote } from "@/components/form"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { VerbActions, type Verb } from "@/components/verbs"
import { TemplatePreviewFacts } from "@/components/deploy/template-preview"
import {
  templateDeployHref,
  templateFilename,
  templateId,
  templateOrigin,
  templateRuns,
  templateUse,
} from "@/components/deploy/templates"

/**
 * Templates: the catalogue's own half — the templates added on this
 * dashboard, which the new-project picker offers under Your templates beside
 * the reviewed built-ins. A row is a way into the picker with the template
 * chosen; its verbs export the definition as a file and remove it. A
 * template is added from a pasted Compose file here, from a running
 * deployment's Settings → General (Save as template…), or by importing a
 * definition exported from this or another dashboard.
 *
 * A reading page (§16): the rows are destinations, so they take the lit edge;
 * the page's commands are the panel head's, and the one sheet is the Compose
 * file's form.
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
  const [composeOpen, setComposeOpen] = useState(false)
  const [importing, setImporting] = useState(false)
  const file = useRef<HTMLInputElement>(null)
  const { confirm, dialog } = useConfirm()
  const list = templates.data

  const exportTemplate = async (template: UserTemplate) => {
    try {
      const text = await getText(`/deploy/templates/${encodeURIComponent(template.id)}/export`)
      downloadText(templateFilename(template.id), text, "application/json")
    } catch (err) {
      notify.error(`Could not export ${template.name}`, err)
    }
  }

  const importFile = async (chosen: File) => {
    setImporting(true)
    try {
      let definition: unknown
      try {
        definition = JSON.parse(await chosen.text())
      } catch {
        throw new Error(`${chosen.name} is not a JSON file.`)
      }
      const created = await post<UserTemplateDetail>("/deploy/templates/import", definition)
      notify.success(`${created.name} imported`, { description: `Saved as ${created.id}.` })
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
    const verbs: Verb[] = [
      {
        key: "export",
        label: "Export as JSON",
        icon: Download,
        inline: true,
        run: () => void exportTemplate(template),
      },
    ]
    if (admin) {
      verbs.unshift({
        key: "deploy",
        label: "Deploy",
        icon: Rocket,
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
          const chosen = event.target.files?.[0]
          if (chosen) void importFile(chosen)
        }}
      />
      <Panel plain>
        <PanelHeader
          title="Your templates"
          actions={
            <span className="flex flex-wrap items-center gap-3">
              {list && list.length > 0 && (
                <span className="numeric text-hint text-muted-foreground">
                  {plural(list.length, "template")}
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
          {templates.error ? (
            <ErrorState error={templates.error} onRetry={templates.refresh} />
          ) : templates.loading && !list ? (
            <LoadingRows rows={2} />
          ) : (list?.length ?? 0) === 0 ? (
            admin ? (
              // The empty page is the first question: where it comes from.
              <ChoiceGrid columns={3} className="animate-rise">
                <ChoiceCard
                  index={0}
                  mark={FileText}
                  verb="Add a template from a Compose file"
                  title="From a Compose file"
                  description="Paste a stack: its passwords become secrets each deployment generates."
                  onClick={() => setComposeOpen(true)}
                />
                <ChoiceCard
                  index={1}
                  mark={Rocket}
                  verb="Save a deployment as a template"
                  title="From a running deployment"
                  description="Open its Settings → General and press Save as template…"
                  href="/deploy"
                />
                <ChoiceCard
                  index={2}
                  mark={CloudUpload}
                  verb="Import a template file"
                  title="Import a file"
                  description="A definition exported from this or another dashboard."
                  onClick={chooseFile}
                />
              </ChoiceGrid>
            ) : (
              <EmptyNote>No template has been added on this dashboard yet.</EmptyNote>
            )
          ) : (
            <ChoiceList aria-label="Your templates" className="animate-rise">
              {list?.map((template) => (
                <TemplateRow
                  key={template.id}
                  template={template}
                  verbs={verbsFor(template)}
                  deployable={admin}
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <ComposeTemplateSheet
        open={composeOpen}
        onOpenChange={setComposeOpen}
        onSaved={(created) => {
          setComposeOpen(false)
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
 * One template: the product its public image is, its name, and under it how
 * it was made and by whom, what it runs, and how many deployments use it —
 * said again beside Remove, which stops none of them.
 */
function TemplateRow({
  template,
  verbs,
  deployable,
}: {
  template: UserTemplate
  verbs: Verb[]
  /** Whether the reader may make a deployment from it, which is a draft's capability. */
  deployable: boolean
}) {
  const unusable = template.summary.deploymentSupported === false
  return (
    <ChoiceRow
      leading={<ProductLogo id={imageProduct(template.summary.image)} size="sm" />}
      title={template.name}
      verb={`Deploy ${template.name}`}
      href={deployable && !unusable ? templateDeployHref(template.id) : undefined}
      disabled={!deployable || unusable}
      description={
        <span className="flex min-w-0 flex-col gap-0.5">
          <span>
            <span className="font-mono">{template.id}</span> · v{template.version} ·{" "}
            {templateOrigin(template)} by {template.createdBy || "this dashboard"},{" "}
            <time dateTime={template.createdAt} title={timestamp(template.createdAt)}>
              {relativeTime(template.createdAt)}
            </time>
          </span>
          <span className="min-w-0 truncate font-mono">{templateRuns(template.summary)}</span>
          {unusable ? (
            <span className="text-warning">{template.summary.unavailableReason}</span>
          ) : (
            <span>{templateUse(template.deployments)}</span>
          )}
        </span>
      }
      trailing={
        <Tag className="max-sm:hidden">
          {template.summary.sourceKind === "compose" ? "Compose stack" : "Image"}
        </Tag>
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${template.name}`} />}
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
