import type { RemoteNode, RemoteNodeUpgradeView, RemoteRunner } from "@/features/remote-nodes/data"

export const REMOTE_CONFIGURATION_ID = "11111111-1111-4111-8111-111111111111"
export const REMOTE_PHYSICAL_ID = "22222222-2222-4222-8222-222222222222"
export const REMOTE_INSTALLATION_ID = "33333333-3333-4333-8333-333333333333"
export const UPGRADE_OPERATION_ID = "44444444-4444-4444-8444-444444444444"

export function remoteRunnerFixture(changes: Partial<RemoteRunner> = {}): RemoteRunner {
  return { node_id: REMOTE_PHYSICAL_ID, installation_id: REMOTE_INSTALLATION_ID, version: "runner-old-compatible",
    build_id: "a".repeat(64), os: "linux", arch: "amd64", ready: true, healthy: true,
    active: 0, queued: 0, pending: 0, max_active_jobs: 4, max_concurrency: 16, ...changes }
}

export function remoteUpgradeFixture(changes: Partial<RemoteNodeUpgradeView> = {}): RemoteNodeUpgradeView {
  return { configuration_id: REMOTE_CONFIGURATION_ID, node_id: REMOTE_PHYSICAL_ID, installation_id: REMOTE_INSTALLATION_ID,
    operation_id: UPGRADE_OPERATION_ID, stage: "checking", error_code: "", expected_build_id: "a".repeat(64),
    target_build_id: "b".repeat(64), actual_runner: remoteRunnerFixture(), resume_attempted: false,
    restorable: false, confirmed: true, changed_at: "2026-10-16T12:00:00.123456789Z", ...changes }
}

export function remoteNodeFixture(changes: Partial<RemoteNode> = {}): RemoteNode {
  return { id: REMOTE_CONFIGURATION_ID, revision: 3, name: "Upgrade test node", region: "Test", host: "runner.example.test",
    port: 22, user: "runner", enabled: true, authentication_kind: "password", has_authentication: true,
    has_passphrase: false, host_key: { type: "ssh-ed25519", fingerprint: `SHA256:${"A".repeat(43)}` },
    proposed_host_key: null, status: "ready", eligible: true, checking: false, checked_at: "2026-10-16T12:00:00Z",
    runner: remoteRunnerFixture(), installation_stage: "", installation_error: "", upgrade: null, ...changes }
}
