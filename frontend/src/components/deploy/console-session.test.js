import { describe, expect, test } from "bun:test"
import {
  DEFAULT_CHOICE,
  execQuery,
  healthReading,
  readChoice,
  restartsLabel,
  runsAsRoot,
} from "./console-session"

describe("execQuery", () => {
  test("the default session asks for nothing but its size", () => {
    expect(execQuery(DEFAULT_CHOICE)).toEqual({ rows: 30, cols: 100 })
  })

  test("a chosen shell sends its absolute path as cmd", () => {
    expect(execQuery({ shell: "bash", runAs: "default" })).toEqual({
      rows: 30,
      cols: 100,
      cmd: "/bin/bash",
    })
    expect(execQuery({ shell: "ash", runAs: "default" }).cmd).toBe("/bin/ash")
    expect(execQuery({ shell: "sh", runAs: "default" }).cmd).toBe("/bin/sh")
  })

  test("root sends user, and only root does", () => {
    expect(execQuery({ shell: "auto", runAs: "root" }).user).toBe("root")
    expect("user" in execQuery({ shell: "bash", runAs: "default" })).toBe(false)
  })
})

describe("readChoice", () => {
  test("falls back to the default for anything it does not offer", () => {
    expect(readChoice(undefined)).toEqual(DEFAULT_CHOICE)
    expect(readChoice({ shell: "zsh", runAs: "www-data" })).toEqual(DEFAULT_CHOICE)
    expect(readChoice({ shell: "ash", runAs: "root" })).toEqual({ shell: "ash", runAs: "root" })
  })
})

describe("runsAsRoot", () => {
  test("explicit root is root whatever the image says", () => {
    expect(runsAsRoot("root", "node")).toBe(true)
    expect(runsAsRoot("root", undefined)).toBe(true)
  })

  test("the image's user decides when none is chosen", () => {
    expect(runsAsRoot("default", "")).toBe(true)
    expect(runsAsRoot("default", "root")).toBe(true)
    expect(runsAsRoot("default", "0:0")).toBe(true)
    expect(runsAsRoot("default", "root:staff")).toBe(true)
    expect(runsAsRoot("default", "node")).toBe(false)
    expect(runsAsRoot("default", "1000:1000")).toBe(false)
  })

  test("an image whose detail has not arrived is not called root", () => {
    expect(runsAsRoot("default", undefined)).toBe(false)
  })
})

describe("healthReading", () => {
  test("names the three states a health check has", () => {
    expect(healthReading("healthy")).toEqual({ tone: "running", label: "Healthy" })
    expect(healthReading("unhealthy")?.tone).toBe("danger")
    expect(healthReading("starting")?.tone).toBe("warning")
  })

  test("says nothing where there is no check or it was not observed", () => {
    expect(healthReading("none")).toBeNull()
    expect(healthReading("unavailable")).toBeNull()
    expect(healthReading("")).toBeNull()
    expect(healthReading(undefined)).toBeNull()
  })
})

describe("restartsLabel", () => {
  test("agrees in number", () => {
    expect(restartsLabel(0)).toBe("0 restarts")
    expect(restartsLabel(1)).toBe("1 restart")
    expect(restartsLabel(4)).toBe("4 restarts")
  })
})
