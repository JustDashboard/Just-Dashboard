import type { ContainerSpec, DockerFinding } from "./types"

/** A local, closed rule catalogue. Preparing a change never applies it. */
export const DOCKER_REMEDIES = {
  nohealthcheck: {
    label: "Add a health check",
    advice:
      "Choose a readiness command that exists in this image and exits 0 only when the application is ready. Docker runs it inside the container. A failed check alone does not restart it.",
  },
  latest: {
    label: "Pin the image",
    advice:
      "Choose an explicit version or repository digest. Recreating from a moving tag can change the software; restarting an existing container does not pull an image.",
  },
  exposed: {
    label: "Review port bindings",
    advice:
      "Bind only the selected ports to loopback if remote clients should no longer reach them. Confirm the reverse proxy can still reach this address; a proxy in another container may need a shared Docker network.",
  },
  privileged: {
    label: "Review privileges",
    advice:
      "Disable privileged mode only after identifying the devices and capabilities this application needs. The Just Dashboard backend deliberately controls the host; removing its required access can break the panel.",
  },
  dockersock: {
    label: "Review socket access",
    advice:
      "Remove the Docker socket only if the application does not manage containers. Just Dashboard needs this access for its Docker controls. A read-only socket mount still permits Docker API mutations.",
  },
} as const
export type DockerRemedyKind = keyof typeof DOCKER_REMEDIES

export function dockerRemedyKind(finding: Pick<DockerFinding, "id">): DockerRemedyKind | undefined {
  const kind = finding.id.split(".")[1]
  return Object.hasOwn(DOCKER_REMEDIES, kind) ? (kind as DockerRemedyKind) : undefined
}

export function prepareDockerRemedy(
  spec: ContainerSpec,
  kind: DockerRemedyKind,
  value?: string,
): ContainerSpec {
  switch (kind) {
    case "nohealthcheck":
      if (!value?.trim()) throw new Error("Enter an application readiness command")
      return {
        ...spec,
        health: {
          test: ["CMD-SHELL", value.trim()],
          intervalSeconds: 30,
          timeoutSeconds: 5,
          startPeriodSeconds: 30,
          retries: 3,
        },
      }
    case "latest":
      if (
        !value?.trim() ||
        value.trim() === spec.image ||
        (!value.includes("@sha256:") &&
          (!value.split("/").at(-1)?.includes(":") || value.endsWith(":latest")))
      )
        throw new Error("Choose a different explicit version or repository digest")
      return { ...spec, image: value.trim() }
    case "exposed":
      return {
        ...spec,
        ports: spec.ports?.map((port) =>
          !port.hostIp || port.hostIp === "0.0.0.0" || port.hostIp === "::"
            ? { ...port, hostIp: "127.0.0.1" }
            : port,
        ),
      }
    case "privileged":
      return { ...spec, privileged: false }
    case "dockersock":
      return {
        ...spec,
        mounts: spec.mounts?.filter(
          (mount) =>
            !["/run/docker.sock", "/var/run/docker.sock"].includes(
              mount.source?.replace(/\/+$/, "") ?? "",
            ),
        ),
      }
  }
}

export function changedSpecFields(before: ContainerSpec, after: ContainerSpec): string[] {
  return [...new Set([...Object.keys(before), ...Object.keys(after)])].filter(
    (key) =>
      JSON.stringify(before[key as keyof ContainerSpec]) !==
      JSON.stringify(after[key as keyof ContainerSpec]),
  )
}

export function composeRemedy(kind: string): string {
  switch (kind) {
    case "nohealthcheck":
      return "healthcheck:\n  test: [CMD-SHELL, '<readiness command in this image>']\n  interval: 30s\n  timeout: 5s\n  retries: 3\n  start_period: 30s"
    case "latest":
      return "image: <repository>:<chosen version> # or repository@sha256:<digest>"
    case "exposed":
      return 'ports:\n  - "127.0.0.1:<host port>:<container port>"'
    case "privileged":
      return "privileged: false\n# Keep only the capabilities/devices this service requires."
    case "dockersock":
      return "# Remove the /run/docker.sock or /var/run/docker.sock bind\n# only if this service does not need to manage Docker."
    case "cap-logs":
      return 'logging:\n  driver: json-file\n  options:\n    max-size: "10m"\n    max-file: "3"'
    case "set-restart":
      return "restart: unless-stopped"
    case "nomemorylimit":
      return "mem_limit: <reviewed limit, e.g. 512m>\ncpus: <reviewed CPU quota, e.g. 1.0>"
    default:
      return "# Review the finding in this service's configuration."
  }
}
