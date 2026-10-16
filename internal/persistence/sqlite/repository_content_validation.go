package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// Publication and empty inventory are the only enabled content writes. The
// complete current DDL also reserves the final lifecycle relations, whose rows
// cannot become trusted facts before their typed writers and validators exist.
var inactiveContentTables = []string{
	"job_content_entries", "job_content_manifests", "job_content_manifest_items",
	"job_content_hold_attestations", "job_content_releases", "job_content_release_items",
	"content_deletion_intents", "remote_job_content_manifests", "client_job_content_receipts",
	"client_job_content_receipt_items", "client_job_content_release_outbox",
}

func rejectInactiveContentFacts(ctx context.Context, queryer rowQueryer) error {
	checks := make([]string, 0, len(inactiveContentTables)+1)
	for _, table := range inactiveContentTables {
		checks = append(checks, `EXISTS(SELECT 1 FROM "`+table+`")`)
	}
	var unsupported bool
	if err := queryer.QueryRowContext(ctx, `SELECT `+strings.Join(checks, " OR ")).Scan(&unsupported); err != nil {
		return fmt.Errorf("inspect current content facts: %w", err)
	}
	if unsupported {
		return contentCorrupt("unsupported content lifecycle facts")
	}
	return nil
}

func validateContentRelations(ctx context.Context, conn relationQueryer) error {
	if err := rejectInactiveContentFacts(ctx, conn); err != nil {
		return err
	}
	var unsupported bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM execution_jobs j
		LEFT JOIN execution_job_content c ON c.job_id = j.id
		WHERE c.job_id IS NULL OR c.inventory_revision != 1 OR c.sealed_manifest_sha256 IS NOT NULL
		OR json_type(j.spec_json, '$.assets') IS NOT 'array'
		OR json_array_length(j.spec_json, '$.assets') != 0)`).Scan(&unsupported); err != nil {
		return fmt.Errorf("inspect current content facts: %w", err)
	}
	if unsupported {
		return contentCorrupt("unsupported content lifecycle facts")
	}
	if err := runnerBackupEachID(
		ctx,
		conn,
		"content_stores",
		"store_id",
		func(id string) error {
			_, err := queryContentStore(ctx, conn, id)
			return err
		},
	); err != nil {
		return err
	}
	var afterStore, afterSHA string
	for {
		rows, err := conn.QueryContext(
			ctx,
			`SELECT store_id, sha256 FROM content_objects
			WHERE store_id > ? OR (store_id = ? AND sha256 > ?)
			ORDER BY store_id, sha256 LIMIT 128`,
			afterStore,
			afterStore,
			afterSHA,
		)
		if err != nil {
			return fmt.Errorf("list content relations: %w", err)
		}
		type identity struct{ store, sha256 string }
		page := []identity{}
		for rows.Next() {
			var item identity
			if err := rows.Scan(&item.store, &item.sha256); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan content relation: %w", err)
			}
			page = append(page, item)
		}
		iterationErr, closeErr := rows.Err(), rows.Close()
		if iterationErr != nil {
			return iterationErr
		}
		if closeErr != nil {
			return closeErr
		}
		for _, item := range page {
			if !domain.IsUUID(item.store) || !jobcontract.IsSHA256(item.sha256) {
				return contentCorrupt("content relation key")
			}
			head, err := queryContentPublication(
				ctx,
				conn,
				item.store,
				item.sha256,
			)
			if err != nil {
				return err
			}
			var count, first, last int64
			var mismatch bool
			if err := conn.QueryRowContext(
				ctx,
				`SELECT COUNT(*), MIN(generation), MAX(generation),
				COALESCE(MAX(size != ? OR physical_incarnation_id != ? OR witness_sha256 != ?), 0)
				FROM content_generations WHERE store_id = ? AND sha256 = ?`,
				head.Object.Content.Size,
				head.Object.IncarnationID,
				head.Object.WitnessSHA256,
				item.store,
				item.sha256,
			).
				Scan(
					&count,
					&first,
					&last,
					&mismatch,
				); err != nil {
				return fmt.Errorf("inspect content generation history: %w", err)
			}
			if first != 1 || last != int64(head.Number) || count != last || mismatch {
				return contentCorrupt("content generation history")
			}
			afterStore, afterSHA = item.store, item.sha256
		}
		if len(page) < 128 {
			return ctx.Err()
		}
	}
}

func validateContentBackupRow(table string, row runnerBackupRecord) error {
	switch table {
	case "content_stores":
		createdAt, err := contentTime(row.text("created_at"))
		store := jobcontent.Store{
			ID: row.text("store_id"), Kind: jobcontent.StoreKind(row.text("kind")),
			InstallationID: row.text("owner_installation_id"), CreatedAt: createdAt,
		}
		if err != nil || store.Validate() != nil {
			return contentCorrupt("backup content store")
		}
	case "content_objects":
		object := jobcontent.ObjectIdentity{
			StoreID:       row.text("store_id"),
			Content:       jobcontract.ContentIdentity{SHA256: row.text("sha256"), Size: row.integer("size")},
			IncarnationID: row.text("physical_incarnation_id"), WitnessSHA256: row.text("witness_sha256"),
			ChangeToken: row.text("verified_change_token"),
		}
		if object.Validate() != nil || row.text("physical_state") != "present" || row.integer("revision") <= 0 {
			return contentCorrupt("backup content object")
		}
	case "content_generations":
		_, err := contentTime(row.text("created_at"))
		content := jobcontract.ContentIdentity{SHA256: row.text("sha256"), Size: row.integer("size")}
		validIDs := domain.IsUUID(row.text("store_id")) && domain.IsUUID(row.text("physical_incarnation_id"))
		validContent := content.Validate() == nil && jobcontract.IsSHA256(row.text("witness_sha256"))
		if err != nil || !validIDs || !validContent || row.integer("generation") <= 0 {
			return contentCorrupt("backup content generation")
		}
	case "content_heads":
		validKey := domain.IsUUID(row.text("store_id")) && jobcontract.IsSHA256(row.text("sha256"))
		if !validKey || row.integer("generation") <= 0 || row.integer("revision") <= 0 {
			return contentCorrupt("backup content head")
		}
	case "execution_job_content":
		validIdentity := domain.IsUUID(row.text("job_id")) && row.integer("inventory_revision") == 1
		if !validIdentity || row["sealed_manifest_sha256"] != nil {
			return contentCorrupt("backup content inventory")
		}
	default:
		for _, inactive := range inactiveContentTables {
			if table == inactive {
				return contentCorrupt("inactive content lifecycle table is nonempty")
			}
		}
	}
	return nil
}
