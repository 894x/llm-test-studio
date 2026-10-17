import { describe, expect, it } from "vitest"
import { DesktopDataError } from "@/app/data-error"
import { EMPTY_REMOTE_NODES, parseRemoteNodes, type RemoteNodesSnapshot } from "./data"

function snapshot(): RemoteNodesSnapshot {
  const id = "11111111-1111-4111-8111-111111111111"
  return { schema_version: 1, default_revision: 2, default: { kind: "remote", configuration_id: id }, nodes: [{
    id, revision: 1, name: "Shared node", region: "test", host: "runner.example.test", port: 22, user: "root",
    enabled: true, authentication_kind: "password", has_authentication: true, has_passphrase: false,
    host_key: { type: "ssh-ed25519", fingerprint: `SHA256:${"A".repeat(43)}` }, proposed_host_key: null,
    status: "ready", eligible: true, checking: false, checked_at: "2026-10-16T00:00:00Z",
    installation_stage: "", installation_error: "", upgrade: null, runner: {
      node_id: id, installation_id: id, version: "runner-test", build_id: "a".repeat(64), os: "linux", arch: "amd64",
      ready: true, healthy: true, active: 0, queued: 0, pending: 0, max_active_jobs: 1, max_concurrency: 2000,
    },
  }] }
}

describe("current remote node boundary", () => {
  it("keeps local empty and offline configurations explicit and owns returned values", () => {
    expect(parseRemoteNodes(EMPTY_REMOTE_NODES)).toEqual(EMPTY_REMOTE_NODES)
    const original = snapshot()
    const parsed = parseRemoteNodes(original)
    parsed.nodes[0].runner!.version = "changed"
    expect(original.nodes[0].runner!.version).toBe("runner-test")
    original.nodes[0].eligible = false
    original.nodes[0].status = "offline"
    expect(parseRemoteNodes(original).default.kind).toBe("remote")
  })

  it.each([
    (value: Record<string, unknown>) => { delete value.schema_version },
    (value: Record<string, unknown>) => { value.schema_version = 0 },
    (value: Record<string, unknown>) => { value.api_key = "private" },
    (value: Record<string, unknown>) => { value.default = { kind: "local", configuration_id: "bad" } },
    (value: Record<string, unknown>) => { value.default = { kind: "remote", configuration_id: "22222222-2222-4222-8222-222222222222" } },
    (value: Record<string, unknown>) => { value.nodes = [null] },
    (value: Record<string, unknown>) => { value.nodes = Array(65).fill(snapshot().nodes[0]) },
    (value: Record<string, unknown>) => { value.nodes = [snapshot().nodes[0], snapshot().nodes[0]] },
    (value: Record<string, unknown>) => { (value.nodes as Record<string, unknown>[])[0].password_ref = "private" },
    (value: Record<string, unknown>) => { (value.nodes as Record<string, unknown>[])[0].port = 0 },
    (value: Record<string, unknown>) => { (value.nodes as Record<string, unknown>[])[0].checking = "yes" },
    (value: Record<string, unknown>) => { (value.nodes as Record<string, unknown>[])[0].status = "future-state" },
    (value: Record<string, unknown>) => { (value.nodes as Record<string, unknown>[])[0].checked_at = "yesterday" },
    (value: Record<string, unknown>) => { (value.nodes as Record<string, unknown>[])[0].proposed_host_key = { type: "ssh-ed25519", fingerprint: "bad" } },
    (value: Record<string, unknown>) => { ((value.nodes as Record<string, unknown>[])[0].runner as Record<string, unknown>).token_ref = "private" },
    (value: Record<string, unknown>) => { ((value.nodes as Record<string, unknown>[])[0].runner as Record<string, unknown>).build_id = "bad" },
  ])("rejects unsupported, malformed and private fields without changing data", mutate => {
    const value = snapshot() as unknown as Record<string, unknown>
    mutate(value)
    const before = JSON.stringify(value)
    expect(() => parseRemoteNodes(value)).toThrow(DesktopDataError)
    expect(JSON.stringify(value)).toBe(before)
  })
})
