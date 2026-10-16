import { describe, expect, it } from "vitest"
import { DesktopDataError } from "@/app/data-error"
import { remoteNodeFixture, remoteRunnerFixture, remoteUpgradeFixture } from "@/test/remote-upgrade-fixture"
import { parseNullableRemoteNodeUpgrade, parseRemoteNodeUpgrade, parseRemoteNodes, REMOTE_UPGRADE_ERRORS, REMOTE_UPGRADE_STAGES } from "./data"

describe("current remote upgrade boundary", () => {
  it("accepts explicit null only for an absent upgrade record", () => {
    expect(parseNullableRemoteNodeUpgrade(null)).toBeNull()
    expect(() => parseNullableRemoteNodeUpgrade(undefined)).toThrow(DesktopDataError)
    expect(() => parseRemoteNodeUpgrade(null)).toThrow(DesktopDataError)
  })

  it.each(REMOTE_UPGRADE_STAGES)("keeps the authoritative %s stage and executor identity", stage => {
    const source = remoteUpgradeFixture({ stage })
    const parsed = parseRemoteNodeUpgrade(source)
    expect(parsed).toEqual(source)
    expect(parsed.configuration_id).not.toBe(parsed.node_id)
    parsed.actual_runner!.version = "changed"
    expect(source.actual_runner!.version).toBe("runner-old-compatible")
  })

  it.each(REMOTE_UPGRADE_ERRORS)("preserves the fixed %s public error", error_code => {
    expect(parseRemoteNodeUpgrade(remoteUpgradeFixture({ error_code })).error_code).toBe(error_code)
  })

  it("accepts observed old/new builds without comparing them to a client build", () => {
    expect(parseRemoteNodeUpgrade(remoteUpgradeFixture()).actual_runner?.build_id).toBe("a".repeat(64))
    const value = remoteUpgradeFixture({ stage: "completed", actual_runner: remoteRunnerFixture({ version: "runner-new", build_id: "b".repeat(64) }) })
    expect(parseRemoteNodeUpgrade(value).actual_runner?.build_id).toBe("b".repeat(64))
    expect(parseRemoteNodeUpgrade({ ...value, actual_runner: null }).actual_runner).toBeNull()
  })

  it.each(Object.keys(remoteUpgradeFixture()))("rejects missing and mis-cased %s", key => {
    const missing = { ...remoteUpgradeFixture() } as Record<string, unknown>
    const original = missing[key]
    delete missing[key]
    const before = structuredClone(missing)
    expect(() => parseRemoteNodeUpgrade(missing)).toThrow(DesktopDataError)
    expect(missing).toEqual(before)
    expect(() => parseRemoteNodeUpgrade({ ...missing, [key.toUpperCase()]: original })).toThrow(DesktopDataError)
  })

  it.each([
    { schema_version: 0 }, { password: "private" }, { configuration_id: "bad" }, { node_id: "bad" },
    { installation_id: "bad" }, { operation_id: "bad" }, { stage: "future" }, { error_code: "secret=/private/path" },
    { expected_build_id: "A".repeat(64) }, { target_build_id: "b".repeat(63) }, { target_build_id: "g".repeat(64) },
    { resume_attempted: 1 }, { restorable: "yes" }, { confirmed: null }, { changed_at: "yesterday" },
    { changed_at: "2026-10-16T12:00:00+00:00" }, { changed_at: "2026-10-16T12:00:00.1234567890Z" },
    { changed_at: "2026-02-30T12:00:00Z" },
    { actual_runner: { ...remoteRunnerFixture(), node_id: "55555555-5555-4555-8555-555555555555" } },
    { actual_runner: { ...remoteRunnerFixture(), installation_id: "55555555-5555-4555-8555-555555555555" } },
    { actual_runner: { ...remoteRunnerFixture(), version: "runner\nprivate" } },
    { actual_runner: { ...remoteRunnerFixture(), version: "runner\ud800private" } },
    { actual_runner: { ...remoteRunnerFixture(), token_ref: "private" } },
  ])("rejects malformed or private values without mutating them: %j", changes => {
    const value = { ...remoteUpgradeFixture(), ...changes }
    const before = structuredClone(value)
    expect(() => parseRemoteNodeUpgrade(value)).toThrow(DesktopDataError)
    expect(value).toEqual(before)
  })

  it("requires upgrade on every node and binds it to the configuration ID", () => {
    const node = remoteNodeFixture({ upgrade: remoteUpgradeFixture() })
    const value = { schema_version: 1, default_revision: 1, default: { kind: "local", configuration_id: "" }, nodes: [node] }
    expect(parseRemoteNodes(value).nodes[0].upgrade).toEqual(node.upgrade)
    const missing = structuredClone(value) as unknown as { nodes: Record<string, unknown>[] }
    delete missing.nodes[0].upgrade
    expect(() => parseRemoteNodes(missing)).toThrow(DesktopDataError)
    node.upgrade!.configuration_id = "55555555-5555-4555-8555-555555555555"
    expect(() => parseRemoteNodes(value)).toThrow(DesktopDataError)
  })
})
