import type { ComposeStack, Container, DeploymentDraftSource } from "@/lib/types"
import { imageName } from "@/components/deploy/new-project/draft"

/** An image tag on this server, with the containers it runs as. */
export type ServerImage = { tag: string; containers: Container[] }

/**
 * The containers each image runs as, by the image id Docker records on the
 * container — the tag a container was started from may since have moved.
 */
export function containersByImage(containers: Container[]) {
  const byImage = new Map<string, Container[]>()
  for (const container of containers) {
    if (container.labels["io.just-dashboard.managed"] === "true") continue
    byImage.set(container.imageId, [...(byImage.get(container.imageId) ?? []), container])
  }
  return byImage
}

/**
 * What a row says about who runs an image: its containers by name, the
 * running ones first, because that is the name the reader knows it by.
 */
export function usedBy(containers: Container[]) {
  if (containers.length === 0) return undefined
  const names = [...containers]
    .sort((a, b) => Number(b.state === "running") - Number(a.state === "running"))
    .map((container) => container.name)
  return names.length <= 2
    ? `Used by ${names.join(" and ")}`
    : `Used by ${names[0]}, ${names[1]} and ${names.length - 2} more`
}

/**
 * The Compose stacks this server can deploy from their own files.
 *
 * A stack's file is what carries what its containers need — the variables it
 * reads, its volumes, its network — so deploying the file is the whole stack,
 * where its images alone would be its containers stripped of all of that.
 * Only a stack whose files are still on disk can be deployed that way; one a
 * deployment of this dashboard already runs is that deployment, and the
 * dashboard's own stack is not something it deploys.
 */
export function deployableStacks(stacks: ComposeStack[], containers: Container[]) {
  const owned = new Set(
    containers
      .filter((container) => container.labels["io.just-dashboard.managed"] === "true")
      .map((container) => container.composeStack)
      .filter(Boolean),
  )
  return stacks
    .filter(
      (stack) =>
        stack.managed &&
        stack.name !== "just-dashboard" &&
        !owned.has(stack.name) &&
        stackSource(stack) !== undefined,
    )
    .sort((a, b) => Number(b.running > 0) - Number(a.running > 0) || a.name.localeCompare(b.name))
}

/**
 * A stack as the `compose_local` source the server reads it from: its
 * directory, and its files named inside it in the order Compose merges them.
 * A file outside the directory cannot be named that way, so such a stack is
 * not offered rather than offered without it.
 */
export function stackSource(stack: ComposeStack): DeploymentDraftSource | undefined {
  const root = stack.workingDir.replace(/\/+$/, "")
  if (!root || stack.configFiles.length === 0) return undefined
  const files: string[] = []
  for (const file of stack.configFiles) {
    if (!file.startsWith(`${root}/`)) return undefined
    files.push(file.slice(root.length + 1))
  }
  return {
    kind: "compose",
    mode: "compose_local",
    localPath: root,
    composeFiles: files.map((path, order) => ({ path, content: "", order })),
  }
}

/** A Compose service name from a container's own, else from the image's. */
function serviceName(image: ServerImage) {
  const named =
    image.containers.find((container) => container.composeService)?.composeService ??
    image.containers[0]?.name ??
    imageName(image.tag)
  return (
    named
      .toLowerCase()
      .replace(/[^a-z0-9._-]+/g, "-")
      .replace(/^[^a-z0-9]+/, "") || "service"
  )
}

/**
 * Several images chosen together, as one Compose stack with a service for
 * each: one project, released and rolled back as one. Each service restarts
 * unless stopped, as a single image's container does by default, because a
 * stack's restart policy is the file's and nothing else would set it.
 */
export function imageStack(images: ServerImage[]): DeploymentDraftSource {
  const used = new Set<string>()
  const lines = ["services:"]
  for (const image of images) {
    const base = serviceName(image)
    let name = base
    for (let suffix = 2; used.has(name); suffix += 1) name = `${base}-${suffix}`
    used.add(name)
    lines.push(
      `  ${name}:`,
      `    image: ${JSON.stringify(image.tag)}`,
      "    restart: unless-stopped",
    )
  }
  return {
    kind: "compose",
    mode: "compose_paste",
    composeFiles: [{ path: "compose.yaml", content: `${lines.join("\n")}\n`, order: 0 }],
  }
}

/**
 * What a stack of images is called: the Compose project they all came from,
 * when they did, else the first of them.
 */
export function imageStackName(images: ServerImage[]) {
  const projects = new Set(
    images.map((image) => image.containers.find((c) => c.composeStack)?.composeStack),
  )
  const [project] = projects
  return projects.size === 1 && project ? project : imageName(images[0]?.tag ?? "")
}
