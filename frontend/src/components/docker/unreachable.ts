/**
 * Why Docker did not answer, as far as two readings can tell.
 *
 * The Docker pages said "Docker is not reachable" over the client's own
 * sentence — "Cannot connect to the Docker daemon at
 * unix:///var/run/docker.sock. Is the docker daemon running?" — which asks
 * the reader the question the page was opened to answer, and reads the same
 * for three faults that are fixed in three places: a socket the dashboard is
 * not allowed to open, a daemon that is stopped, and a daemon that runs but
 * not at the address the dashboard asks.
 *
 * The client's error tells the first from the other two. Whether
 * `docker.service` is running tells the second from the third, where systemd
 * can be asked. A systemd that answers and has no such unit is the fourth
 * reading: Docker was never installed here, which is fixed by installing it.
 */
export type DockerFault =
  /** systemd has no unit for the daemon and nothing answers: it is not installed. */
  | "absent"
  /** The socket is there and refused the dashboard. */
  | "refused"
  /** systemd says the daemon is not running. */
  | "stopped"
  /** The daemon runs, and nothing answers where the dashboard asks. */
  | "elsewhere"
  /** Nothing answered, and systemd has no word on why. */
  | "silent"
  /** The daemon answered with something other than a connection failure. */
  | "other"

export type DockerUnreachable = {
  fault: DockerFault
  title: string
  /** One sentence on what was found, then one on where it is fixed. */
  description: string
  /** Starting the unit would fix it. */
  startable: boolean
  /** The client's own words, for the disclosure. */
  raw?: string
}

/** The unit behind the daemon on every distribution's own Docker package. */
export const DOCKER_UNIT = "docker.service"

/** As `GET /docker/ping` answers when the daemon cannot be asked. */
export type DockerPing = {
  available: boolean
  host?: string
  error?: string
  serverVersion?: string
}

/** What systemd said of the daemon's unit: nothing while it has not been read. */
export type DockerUnitReading = { loadState: string; activeState: string } | undefined

const DEFAULT_SOCKET = "/var/run/docker.sock"

/** The address as a person writes it: the path of a unix socket, else the whole of it. */
function address(host: string | undefined): string {
  if (!host) return DEFAULT_SOCKET
  return host.startsWith("unix://") ? host.slice("unix://".length) : host
}

export function dockerUnreachable(ping: DockerPing, unit: DockerUnitReading): DockerUnreachable {
  const raw = ping.error?.trim() || undefined
  const at = address(ping.host)
  const text = (raw ?? "").toLowerCase()

  if (text.includes("permission denied")) {
    return {
      fault: "refused",
      title: "The dashboard may not open Docker's socket",
      description: `${at} is there and refused the dashboard. The backend has to run as root, as the shipped Compose file runs it, or as an account in the socket's group, usually docker.`,
      startable: false,
      raw,
    }
  }

  // The client says "Cannot connect" for a refused connection and for a
  // socket that is not there, and a daemon that hangs runs into the deadline.
  const noAnswer =
    !raw ||
    text.includes("cannot connect to the docker daemon") ||
    text.includes("connection refused") ||
    text.includes("no such file or directory") ||
    text.includes("deadline exceeded") ||
    text.includes("i/o timeout") ||
    text.includes("not available on this host")
  if (!noAnswer) {
    return {
      fault: "other",
      title: "Docker did not answer as expected",
      description: `Something answered at ${at}, and not with what the dashboard asked for. What it said is below.`,
      startable: false,
      raw,
    }
  }

  // systemd answered and knows no such unit. That is its word on the matter,
  // and a different one from having none (`unit` undefined): a daemon that
  // listens somewhere else is the second sentence's.
  if (unit && unit.loadState === "not-found") {
    return {
      fault: "absent",
      title: "Docker is not installed",
      description: `systemd has no ${DOCKER_UNIT} on this server and nothing answers at ${at}. A daemon that listens somewhere else is named with JD_DOCKER_HOST.`,
      startable: false,
      raw,
    }
  }

  const known = unit && unit.loadState !== "not-found"
  if (known && unit.activeState === "active") {
    return {
      fault: "elsewhere",
      title: "Docker is running, but not where the dashboard asks",
      description: `${DOCKER_UNIT} is active and nothing answers at ${at}. The backend needs the daemon's socket: the Compose file mounts /var/run/docker.sock into it, and JD_DOCKER_HOST names the address where the daemon listens somewhere else.`,
      startable: false,
      raw,
    }
  }
  if (known) {
    return {
      fault: "stopped",
      title: "Docker is stopped",
      description: `${DOCKER_UNIT} is ${unit.activeState} on this server, so nothing answers at ${at}.`,
      startable: unit.activeState !== "activating",
      raw,
    }
  }
  return {
    fault: "silent",
    title: "Docker is not answering",
    description: `Nothing answered at ${at}. Either the daemon is stopped, or its socket is not where the dashboard looks: JD_DOCKER_HOST names the address.`,
    startable: false,
    raw,
  }
}
