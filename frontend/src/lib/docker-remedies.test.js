import { expect, test } from "bun:test"
import {
  changedSpecFields,
  composeRemedy,
  dockerRemedyKind,
  prepareDockerRemedy,
} from "./docker-remedies"

const spec = {
  name: "service",
  image: "registry.example:5000/team/app:latest",
  limits: { memoryMb: 512 },
  env: [{ name: "TOKEN", value: "keep-me" }],
  privileged: true,
  ports: [
    { hostIp: "0.0.0.0", hostPort: 5432, containerPort: 5432 },
    { hostIp: "192.168.1.2", hostPort: 8000, containerPort: 8000 },
  ],
  mounts: [
    { type: "bind", source: "/run/docker.sock", target: "/var/run/docker.sock" },
    { type: "volume", source: "data", target: "/data" },
  ],
}

test("remedies preserve credentials, limits and data mounts while changing the reviewed field", () => {
  const restricted = prepareDockerRemedy(spec, "privileged")
  expect(changedSpecFields(spec, restricted)).toEqual(["privileged"])
  expect(restricted.env).toEqual(spec.env)
  const socket = prepareDockerRemedy(spec, "dockersock")
  expect(socket.mounts).toEqual([spec.mounts[1]])
  const ports = prepareDockerRemedy(spec, "exposed")
  expect(ports.ports[0].hostIp).toBe("127.0.0.1")
  expect(ports.ports[1]).toEqual(spec.ports[1])
  expect(spec.ports[0].hostIp).toBe("0.0.0.0")
})

test("readiness requires an operator's real command and image pinning rejects a registry-port-only tag", () => {
  expect(() => prepareDockerRemedy(spec, "nohealthcheck", "")).toThrow()
  expect(prepareDockerRemedy(spec, "nohealthcheck", "app-ready --local").health.test).toEqual([
    "CMD-SHELL",
    "app-ready --local",
  ])
  expect(() => prepareDockerRemedy(spec, "latest", "team/app:latest")).toThrow()
  expect(() => prepareDockerRemedy(spec, "latest", "registry.example:5000/team/app")).toThrow()
  expect(prepareDockerRemedy(spec, "latest", "registry.example:5000/team/app:v3").image).toEndWith(
    ":v3",
  )
})

test("all configuration findings open an explicit remedy and Compose snippets name persistence", () => {
  for (const kind of ["nohealthcheck", "latest", "exposed", "privileged", "dockersock"]) {
    expect(dockerRemedyKind({ id: `container.${kind}.id` })).toBe(kind)
    expect(composeRemedy(kind)).not.toBe("")
  }
  expect(dockerRemedyKind({ id: "container.unhealthy.id" })).toBeUndefined()
})
