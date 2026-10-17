package sqlite

// These objects are part of the single current schema. Keeping them together
// makes the content ownership relationships reviewable without a migration path.
var contentSchemaObjects = []schemaObject{
	{kind: "table", name: "content_stores", table: "content_stores", ddl: `CREATE TABLE content_stores (
		store_id TEXT PRIMARY KEY NOT NULL,
		kind TEXT NOT NULL CHECK(kind IN ('executor', 'cache')),
		owner_installation_id TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`},
	{kind: "table", name: "content_objects", table: "content_objects", ddl: `CREATE TABLE content_objects (
		store_id TEXT NOT NULL,
		sha256 TEXT NOT NULL CHECK(length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
		size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 1073741824),
		physical_incarnation_id TEXT NOT NULL,
		witness_sha256 TEXT NOT NULL CHECK(length(witness_sha256) = 64 AND witness_sha256 NOT GLOB '*[^0-9a-f]*'),
		verified_change_token TEXT NOT NULL CHECK(length(verified_change_token) = 64 AND verified_change_token NOT GLOB '*[^0-9a-f]*'),
		physical_state TEXT NOT NULL CHECK(physical_state IN ('present', 'deleting', 'absent', 'quarantined')),
		revision INTEGER NOT NULL CHECK(revision > 0),
		PRIMARY KEY(store_id, sha256),
		UNIQUE(store_id, sha256, size),
		FOREIGN KEY(store_id) REFERENCES content_stores(store_id) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "content_generations", table: "content_generations", ddl: `CREATE TABLE content_generations (
		store_id TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		generation INTEGER NOT NULL CHECK(generation > 0),
		size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 1073741824),
		physical_incarnation_id TEXT NOT NULL,
		witness_sha256 TEXT NOT NULL CHECK(length(witness_sha256) = 64 AND witness_sha256 NOT GLOB '*[^0-9a-f]*'),
		created_at TEXT NOT NULL,
		PRIMARY KEY(store_id, sha256, generation),
		UNIQUE(store_id, sha256, size, generation),
		UNIQUE(store_id, sha256, size, generation, witness_sha256),
		UNIQUE(store_id, sha256, size, generation, physical_incarnation_id, witness_sha256),
		FOREIGN KEY(store_id, sha256, size) REFERENCES content_objects(store_id, sha256, size) ON DELETE RESTRICT
	)`},
	{kind: "index", name: "idx_content_generations_incarnation", table: "content_generations",
		ddl: `CREATE INDEX idx_content_generations_incarnation ON content_generations(store_id, sha256, physical_incarnation_id)`},
	{kind: "table", name: "content_heads", table: "content_heads", ddl: `CREATE TABLE content_heads (
		store_id TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		generation INTEGER NOT NULL CHECK(generation > 0),
		revision INTEGER NOT NULL CHECK(revision > 0),
		PRIMARY KEY(store_id, sha256),
		FOREIGN KEY(store_id, sha256, generation) REFERENCES content_generations(store_id, sha256, generation) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "execution_job_content", table: "execution_job_content", ddl: `CREATE TABLE execution_job_content (
		job_id TEXT PRIMARY KEY NOT NULL,
		inventory_revision INTEGER NOT NULL CHECK(inventory_revision > 0),
		sealed_manifest_sha256 TEXT,
		FOREIGN KEY(job_id) REFERENCES execution_jobs(id) ON DELETE RESTRICT,
		FOREIGN KEY(job_id, sealed_manifest_sha256, inventory_revision)
			REFERENCES job_content_manifests(job_id, sha256, inventory_revision) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "job_content_entries", table: "job_content_entries", ddl: `CREATE TABLE job_content_entries (
		id TEXT PRIMARY KEY NOT NULL,
		job_id TEXT NOT NULL,
		role TEXT NOT NULL CHECK(role IN ('input', 'artifact')),
		ref TEXT NOT NULL CHECK(length(CAST(ref AS BLOB)) BETWEEN 1 AND 256),
		run_id TEXT,
		store_id TEXT NOT NULL,
		sha256 TEXT NOT NULL CHECK(length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
		size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 1073741824),
		media_type TEXT NOT NULL CHECK(length(CAST(media_type AS BLOB)) BETWEEN 1 AND 255),
		generation INTEGER CHECK(generation > 0),
		state TEXT NOT NULL CHECK(state IN ('capturing', 'live', 'released', 'capture_failed')),
		failure_code TEXT CHECK(failure_code IN ('read_failed', 'integrity_mismatch', 'storage_unavailable', 'cancelled')),
		linked_at TEXT NOT NULL,
		released_by_command TEXT,
		CHECK(role != 'input' OR (run_id IS NULL AND state IN ('live', 'released') AND generation IS NOT NULL)),
		CHECK((state IN ('live', 'released') AND generation IS NOT NULL AND failure_code IS NULL)
			OR (role = 'artifact' AND state = 'capturing' AND generation IS NULL AND failure_code IS NULL)
			OR (role = 'artifact' AND state = 'capture_failed' AND generation IS NULL AND failure_code IS NOT NULL)),
		CHECK((state = 'released' AND released_by_command IS NOT NULL) OR (state != 'released' AND released_by_command IS NULL)),
		UNIQUE(job_id, role, ref),
		UNIQUE(job_id, id, store_id, sha256, size, generation),
		FOREIGN KEY(job_id) REFERENCES execution_job_content(job_id) ON DELETE RESTRICT,
		FOREIGN KEY(job_id, run_id) REFERENCES execution_job_runs(job_id, run_id) ON DELETE RESTRICT,
		FOREIGN KEY(store_id) REFERENCES content_stores(store_id) ON DELETE RESTRICT,
		FOREIGN KEY(store_id, sha256, size, generation)
			REFERENCES content_generations(store_id, sha256, size, generation) ON DELETE RESTRICT,
		FOREIGN KEY(job_id, id, released_by_command)
			REFERENCES job_content_release_items(job_id, item_id, command_id) ON DELETE RESTRICT
	)`},
	{kind: "index", name: "idx_job_content_entries_live", table: "job_content_entries",
		ddl: `CREATE INDEX idx_job_content_entries_live ON job_content_entries(store_id, sha256, state, generation)`},
	{kind: "index", name: "idx_job_content_entries_job", table: "job_content_entries",
		ddl: `CREATE INDEX idx_job_content_entries_job ON job_content_entries(job_id, state, id)`},
	{kind: "table", name: "job_content_manifests", table: "job_content_manifests", ddl: `CREATE TABLE job_content_manifests (
		job_id TEXT PRIMARY KEY NOT NULL,
		sha256 TEXT NOT NULL CHECK(length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
		size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 4194304),
		inventory_revision INTEGER NOT NULL CHECK(inventory_revision > 0),
		terminal_revision INTEGER NOT NULL CHECK(terminal_revision > 0),
		sealed_sequence INTEGER NOT NULL CHECK(sealed_sequence > 0),
		created_at TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json) AND length(CAST(document_json AS BLOB)) = size),
		UNIQUE(job_id, sha256),
		UNIQUE(job_id, sha256, inventory_revision),
		FOREIGN KEY(job_id) REFERENCES execution_job_content(job_id) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "job_content_manifest_items", table: "job_content_manifest_items", ddl: `CREATE TABLE job_content_manifest_items (
		job_id TEXT NOT NULL,
		item_id TEXT NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0 AND position < 4224),
		store_id TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 1073741824),
		generation INTEGER NOT NULL CHECK(generation > 0),
		PRIMARY KEY(job_id, item_id),
		UNIQUE(job_id, position),
		UNIQUE(job_id, item_id, store_id, sha256, size, generation),
		FOREIGN KEY(job_id) REFERENCES job_content_manifests(job_id) ON DELETE RESTRICT,
		FOREIGN KEY(job_id, item_id, store_id, sha256, size, generation)
			REFERENCES job_content_entries(job_id, id, store_id, sha256, size, generation) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "job_content_hold_attestations", table: "job_content_hold_attestations", ddl: `CREATE TABLE job_content_hold_attestations (
		receipt_id TEXT PRIMARY KEY NOT NULL,
		job_id TEXT NOT NULL,
		manifest_sha256 TEXT NOT NULL,
		holder_id TEXT NOT NULL,
		received_at TEXT NOT NULL,
		request_sha256 TEXT NOT NULL CHECK(length(request_sha256) = 64 AND request_sha256 NOT GLOB '*[^0-9a-f]*'),
		document_json TEXT NOT NULL CHECK(json_valid(document_json) AND length(CAST(document_json AS BLOB)) BETWEEN 1 AND 8192),
		auth_scope TEXT NOT NULL CHECK(auth_scope = 'node'),
		UNIQUE(job_id, manifest_sha256, receipt_id),
		FOREIGN KEY(job_id, manifest_sha256) REFERENCES job_content_manifests(job_id, sha256) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "job_content_releases", table: "job_content_releases", ddl: `CREATE TABLE job_content_releases (
		command_id TEXT PRIMARY KEY NOT NULL,
		job_id TEXT NOT NULL UNIQUE,
		manifest_sha256 TEXT NOT NULL,
		hold_receipt_id TEXT NOT NULL,
		request_sha256 TEXT NOT NULL CHECK(length(request_sha256) = 64 AND request_sha256 NOT GLOB '*[^0-9a-f]*'),
		expected_revision INTEGER NOT NULL CHECK(expected_revision > 0),
		revision INTEGER NOT NULL CHECK(revision > 0),
		state TEXT NOT NULL CHECK(state IN ('intent', 'completed', 'blocked')),
		started_at TEXT NOT NULL,
		completed_at TEXT,
		tombstone_json TEXT CHECK(tombstone_json IS NULL OR (json_valid(tombstone_json) AND length(CAST(tombstone_json AS BLOB)) BETWEEN 1 AND 4096)),
		CHECK((state = 'completed' AND completed_at IS NOT NULL AND tombstone_json IS NOT NULL)
			OR (state != 'completed' AND completed_at IS NULL AND tombstone_json IS NULL)),
		UNIQUE(job_id, command_id),
		FOREIGN KEY(job_id, manifest_sha256, expected_revision)
			REFERENCES job_content_manifests(job_id, sha256, inventory_revision) ON DELETE RESTRICT,
		FOREIGN KEY(job_id, manifest_sha256, hold_receipt_id)
			REFERENCES job_content_hold_attestations(job_id, manifest_sha256, receipt_id) ON DELETE RESTRICT
	)`},
	{kind: "index", name: "idx_job_content_releases_state", table: "job_content_releases",
		ddl: `CREATE INDEX idx_job_content_releases_state ON job_content_releases(state, job_id)`},
	{kind: "table", name: "job_content_release_items", table: "job_content_release_items", ddl: `CREATE TABLE job_content_release_items (
		command_id TEXT NOT NULL,
		item_id TEXT NOT NULL,
		job_id TEXT NOT NULL,
		store_id TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 1073741824),
		expected_generation INTEGER NOT NULL CHECK(expected_generation > 0),
		expected_incarnation TEXT NOT NULL,
		witness_sha256 TEXT NOT NULL CHECK(length(witness_sha256) = 64 AND witness_sha256 NOT GLOB '*[^0-9a-f]*'),
		disposition TEXT NOT NULL CHECK(disposition IN ('pending', 'shared_retained', 'new_generation_retained', 'deleting', 'deleted', 'absent_confirmed', 'blocked')),
		deletion_id TEXT,
		revision INTEGER NOT NULL CHECK(revision > 0),
		error_code TEXT,
		PRIMARY KEY(command_id, item_id),
		UNIQUE(job_id, item_id, command_id),
		UNIQUE(command_id, item_id, store_id, sha256, size, expected_generation, expected_incarnation, witness_sha256),
		FOREIGN KEY(job_id, command_id) REFERENCES job_content_releases(job_id, command_id) ON DELETE RESTRICT,
		FOREIGN KEY(job_id, item_id, store_id, sha256, size, expected_generation)
			REFERENCES job_content_manifest_items(job_id, item_id, store_id, sha256, size, generation) ON DELETE RESTRICT,
		FOREIGN KEY(store_id, sha256, size, expected_generation, expected_incarnation, witness_sha256)
			REFERENCES content_generations(store_id, sha256, size, generation, physical_incarnation_id, witness_sha256) ON DELETE RESTRICT,
		FOREIGN KEY(deletion_id, store_id, sha256, size, expected_incarnation, witness_sha256)
			REFERENCES content_deletion_intents(deletion_id, store_id, sha256, size, expected_incarnation, witness_sha256) ON DELETE RESTRICT
	)`},
	{kind: "index", name: "idx_job_content_release_items_disposition", table: "job_content_release_items",
		ddl: `CREATE INDEX idx_job_content_release_items_disposition ON job_content_release_items(command_id, disposition, item_id)`},
	{kind: "table", name: "content_deletion_intents", table: "content_deletion_intents", ddl: `CREATE TABLE content_deletion_intents (
		deletion_id TEXT PRIMARY KEY NOT NULL,
		release_command_id TEXT NOT NULL,
		grant_item_id TEXT NOT NULL,
		store_id TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 1073741824),
		expected_generation INTEGER NOT NULL CHECK(expected_generation > 0),
		expected_incarnation TEXT NOT NULL,
		witness_sha256 TEXT NOT NULL CHECK(length(witness_sha256) = 64 AND witness_sha256 NOT GLOB '*[^0-9a-f]*'),
		pre_change_token TEXT NOT NULL CHECK(length(pre_change_token) = 64 AND pre_change_token NOT GLOB '*[^0-9a-f]*'),
		post_rename_change_token TEXT CHECK(post_rename_change_token IS NULL OR (length(post_rename_change_token) = 64 AND post_rename_change_token NOT GLOB '*[^0-9a-f]*')),
		step TEXT NOT NULL CHECK(step IN ('prepared', 'isolated', 'removed', 'blocked')),
		revision INTEGER NOT NULL CHECK(revision > 0),
		UNIQUE(store_id, sha256, expected_incarnation),
		UNIQUE(deletion_id, store_id, sha256, size, expected_incarnation, witness_sha256),
		FOREIGN KEY(release_command_id, grant_item_id, store_id, sha256, size, expected_generation, expected_incarnation, witness_sha256)
			REFERENCES job_content_release_items(command_id, item_id, store_id, sha256, size, expected_generation, expected_incarnation, witness_sha256) ON DELETE RESTRICT
	)`},
	{kind: "index", name: "idx_content_deletion_intents_step", table: "content_deletion_intents",
		ddl: `CREATE INDEX idx_content_deletion_intents_step ON content_deletion_intents(step, deletion_id)`},
	{kind: "table", name: "remote_job_content_manifests", table: "remote_job_content_manifests", ddl: `CREATE TABLE remote_job_content_manifests (
		job_id TEXT PRIMARY KEY NOT NULL,
		reference_sequence INTEGER NOT NULL CHECK(reference_sequence > 0),
		sha256 TEXT NOT NULL CHECK(length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
		size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 4194304),
		reference_json TEXT NOT NULL CHECK(json_valid(reference_json) AND length(CAST(reference_json AS BLOB)) BETWEEN 1 AND 8192),
		document_json TEXT CHECK(document_json IS NULL OR (json_valid(document_json) AND length(CAST(document_json AS BLOB)) = size)),
		imported INTEGER NOT NULL CHECK(imported IN (0, 1)),
		CHECK((imported = 0 AND document_json IS NULL) OR (imported = 1 AND document_json IS NOT NULL)),
		UNIQUE(job_id, sha256),
		FOREIGN KEY(job_id) REFERENCES remote_job_mirrors(job_id) ON DELETE RESTRICT,
		FOREIGN KEY(job_id, reference_sequence) REFERENCES remote_job_events(job_id, sequence) ON DELETE RESTRICT
	)`},
	{kind: "index", name: "idx_remote_job_content_manifests_imported", table: "remote_job_content_manifests",
		ddl: `CREATE INDEX idx_remote_job_content_manifests_imported ON remote_job_content_manifests(imported, job_id)`},
	{kind: "table", name: "client_job_content_receipts", table: "client_job_content_receipts", ddl: `CREATE TABLE client_job_content_receipts (
		receipt_id TEXT PRIMARY KEY NOT NULL,
		job_id TEXT NOT NULL,
		location_kind TEXT NOT NULL CHECK(location_kind IN ('local', 'remote')),
		configuration_id TEXT,
		node_id TEXT NOT NULL,
		installation_id TEXT NOT NULL,
		spec_sha256 TEXT NOT NULL CHECK(length(spec_sha256) = 64 AND spec_sha256 NOT GLOB '*[^0-9a-f]*'),
		manifest_sha256 TEXT NOT NULL CHECK(length(manifest_sha256) = 64 AND manifest_sha256 NOT GLOB '*[^0-9a-f]*'),
		holder_id TEXT NOT NULL,
		actual_execution_job_id TEXT,
		remote_job_id TEXT,
		request_sha256 TEXT NOT NULL CHECK(length(request_sha256) = 64 AND request_sha256 NOT GLOB '*[^0-9a-f]*'),
		document_json TEXT NOT NULL CHECK(json_valid(document_json) AND length(CAST(document_json AS BLOB)) BETWEEN 1 AND 8192),
		revision INTEGER NOT NULL CHECK(revision > 0),
		delivery_state TEXT NOT NULL CHECK(delivery_state IN ('pending', 'acknowledged')),
		ack_json TEXT CHECK(ack_json IS NULL OR (json_valid(ack_json) AND length(CAST(ack_json AS BLOB)) BETWEEN 1 AND 8192)),
		CHECK((location_kind = 'local' AND configuration_id IS NULL AND actual_execution_job_id IS NOT NULL AND actual_execution_job_id = job_id AND remote_job_id IS NULL)
			OR (location_kind = 'remote' AND configuration_id IS NOT NULL AND actual_execution_job_id IS NULL AND remote_job_id IS NOT NULL AND remote_job_id = job_id)),
		CHECK((delivery_state = 'pending' AND ack_json IS NULL) OR (delivery_state = 'acknowledged' AND ack_json IS NOT NULL)),
		FOREIGN KEY(actual_execution_job_id) REFERENCES execution_jobs(id) ON DELETE RESTRICT,
		FOREIGN KEY(remote_job_id) REFERENCES remote_job_mirrors(job_id) ON DELETE RESTRICT,
		FOREIGN KEY(actual_execution_job_id, manifest_sha256) REFERENCES job_content_manifests(job_id, sha256) ON DELETE RESTRICT,
		FOREIGN KEY(remote_job_id, manifest_sha256) REFERENCES remote_job_content_manifests(job_id, sha256) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "client_job_content_receipt_items", table: "client_job_content_receipt_items", ddl: `CREATE TABLE client_job_content_receipt_items (
		receipt_id TEXT NOT NULL,
		item_id TEXT NOT NULL,
		cache_store_id TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 1073741824),
		cache_generation INTEGER NOT NULL CHECK(cache_generation > 0),
		witness_sha256 TEXT NOT NULL CHECK(length(witness_sha256) = 64 AND witness_sha256 NOT GLOB '*[^0-9a-f]*'),
		PRIMARY KEY(receipt_id, item_id),
		FOREIGN KEY(receipt_id) REFERENCES client_job_content_receipts(receipt_id) ON DELETE RESTRICT,
		FOREIGN KEY(cache_store_id, sha256, size, cache_generation, witness_sha256)
			REFERENCES content_generations(store_id, sha256, size, generation, witness_sha256) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "client_job_content_release_outbox", table: "client_job_content_release_outbox", ddl: `CREATE TABLE client_job_content_release_outbox (
		command_id TEXT PRIMARY KEY NOT NULL,
		receipt_id TEXT NOT NULL,
		target_json TEXT NOT NULL CHECK(json_valid(target_json) AND length(CAST(target_json AS BLOB)) BETWEEN 1 AND 8192),
		request_sha256 TEXT NOT NULL CHECK(length(request_sha256) = 64 AND request_sha256 NOT GLOB '*[^0-9a-f]*'),
		request_json TEXT NOT NULL CHECK(json_valid(request_json) AND length(CAST(request_json AS BLOB)) BETWEEN 1 AND 4096),
		revision INTEGER NOT NULL CHECK(revision > 0),
		delivery_state TEXT NOT NULL CHECK(delivery_state IN ('pending', 'acknowledged', 'blocked')),
		ack_json TEXT CHECK(ack_json IS NULL OR (json_valid(ack_json) AND length(CAST(ack_json AS BLOB)) BETWEEN 1 AND 4096)),
		CHECK((delivery_state = 'acknowledged' AND ack_json IS NOT NULL) OR (delivery_state != 'acknowledged' AND ack_json IS NULL)),
		FOREIGN KEY(receipt_id) REFERENCES client_job_content_receipts(receipt_id) ON DELETE RESTRICT
	)`},
	{kind: "index", name: "idx_client_job_content_release_outbox_delivery", table: "client_job_content_release_outbox",
		ddl: `CREATE INDEX idx_client_job_content_release_outbox_delivery ON client_job_content_release_outbox(delivery_state, command_id)`},
}
