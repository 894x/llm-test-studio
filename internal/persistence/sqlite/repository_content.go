package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var _ jobcontent.Ledger = (*Repository)(nil)

func (repository *Repository) RegisterContentStore(ctx context.Context, store jobcontent.Store) error {
	if err := store.Validate(); err != nil {
		return err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin content store registration: %w", err)
	}
	defer tx.Rollback()
	if err := rejectInactiveContentFacts(ctx, tx); err != nil {
		return err
	}
	previous, err := queryContentStore(ctx, tx, store.ID)
	if err == nil {
		sameOwner := previous.Kind == store.Kind && previous.InstallationID == store.InstallationID
		if !sameOwner || !previous.CreatedAt.Equal(store.CreatedAt) {
			return jobcontent.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, jobcontent.ErrNotFound) {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO content_stores(store_id, kind, owner_installation_id, created_at)
		VALUES(?, ?, ?, ?)`,
		store.ID,
		store.Kind,
		store.InstallationID,
		formatTime(store.CreatedAt),
	); err != nil {
		return contentWriteError("register content store", err)
	}
	return contentWriteError("commit content store", tx.Commit())
}

func queryContentStore(ctx context.Context, queryer rowQueryer, id string) (jobcontent.Store, error) {
	var store jobcontent.Store
	var createdAt string
	err := queryer.QueryRowContext(ctx, `SELECT store_id, kind, owner_installation_id, created_at
		FROM content_stores WHERE store_id = ?`, id).Scan(
		&store.ID,
		&store.Kind,
		&store.InstallationID,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return jobcontent.Store{}, jobcontent.ErrNotFound
	}
	if err != nil {
		return jobcontent.Store{}, fmt.Errorf("read content store: %w", err)
	}
	store.CreatedAt, err = contentTime(createdAt)
	if err != nil || store.Validate() != nil {
		return jobcontent.Store{}, contentCorrupt("store identity")
	}
	return store, nil
}

func (repository *Repository) PublishContent(
	ctx context.Context,
	publication jobcontent.Publication,
) (jobcontent.Generation, bool, error) {
	if err := publication.Validate(); err != nil {
		return jobcontent.Generation{}, false, err
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return jobcontent.Generation{}, false, fmt.Errorf("begin content publication: %w", err)
	}
	defer tx.Rollback()
	if err := rejectInactiveContentFacts(ctx, tx); err != nil {
		return jobcontent.Generation{}, false, err
	}
	store, err := queryContentStore(ctx, tx, publication.Object.StoreID)
	if err != nil {
		return jobcontent.Generation{}, false, err
	}
	if publication.At.Before(store.CreatedAt) {
		return jobcontent.Generation{}, false, jobcontent.ErrInvalid
	}
	previous, err := queryContentPublication(
		ctx,
		tx,
		publication.Object.StoreID,
		publication.Object.Content.SHA256,
	)
	created := errors.Is(err, jobcontent.ErrNotFound)
	if err != nil && !created {
		return jobcontent.Generation{}, false, err
	}
	next := jobcontent.Generation{Object: publication.Object, Number: 1, CreatedAt: publication.At}
	if !created {
		if previous.Object != publication.Object {
			return jobcontent.Generation{}, false, jobcontent.ErrConflict
		}
		if publication.At.Before(previous.CreatedAt) {
			return jobcontent.Generation{}, false, jobcontent.ErrInvalid
		}
		var claimed bool
		if err := tx.QueryRowContext(
			ctx,
			`SELECT
			EXISTS(SELECT 1 FROM job_content_entries WHERE store_id = ? AND sha256 = ? AND generation = ?)
			OR EXISTS(SELECT 1 FROM client_job_content_receipt_items
				WHERE cache_store_id = ? AND sha256 = ? AND cache_generation = ?)`,
			publication.Object.StoreID,
			publication.Object.Content.SHA256,
			previous.Number,
			publication.Object.StoreID,
			publication.Object.Content.SHA256,
			previous.Number,
		).Scan(&claimed); err != nil {
			return jobcontent.Generation{}, false, fmt.Errorf("inspect claimed publication: %w", err)
		}
		if !claimed {
			return previous, false, nil
		}
		if previous.Number >= math.MaxInt64 {
			return jobcontent.Generation{}, false, jobcontent.ErrConflict
		}
		next.Number = previous.Number + 1
	} else {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO content_objects(
			store_id, sha256, size, physical_incarnation_id, witness_sha256,
			verified_change_token, physical_state, revision) VALUES(?, ?, ?, ?, ?, ?, 'present', 1)`,
			publication.Object.StoreID,
			publication.Object.Content.SHA256,
			publication.Object.Content.Size,
			publication.Object.IncarnationID,
			publication.Object.WitnessSHA256,
			publication.Object.ChangeToken,
		); err != nil {
			return jobcontent.Generation{}, false, contentWriteError("register content object", err)
		}
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO content_generations(
		store_id, sha256, generation, size, physical_incarnation_id, witness_sha256, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		next.Object.StoreID,
		next.Object.Content.SHA256,
		next.Number,
		next.Object.Content.Size,
		next.Object.IncarnationID,
		next.Object.WitnessSHA256,
		formatTime(next.CreatedAt),
	); err != nil {
		return jobcontent.Generation{}, false, contentWriteError("create content generation", err)
	}
	if created {
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO content_heads(store_id, sha256, generation, revision)
			VALUES(?, ?, ?, 1)`,
			next.Object.StoreID,
			next.Object.Content.SHA256,
			next.Number,
		)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(
			ctx,
			`UPDATE content_heads SET generation = ?, revision = revision + 1
			WHERE store_id = ? AND sha256 = ? AND generation = ? AND revision < ?`,
			next.Number,
			next.Object.StoreID,
			next.Object.Content.SHA256,
			previous.Number,
			int64(math.MaxInt64),
		)
		if err == nil {
			count, countErr := result.RowsAffected()
			if countErr != nil {
				return jobcontent.Generation{}, false, fmt.Errorf("read content head change: %w", countErr)
			}
			if count != 1 {
				return jobcontent.Generation{}, false, jobcontent.ErrConflict
			}
		}
	}
	if err != nil {
		return jobcontent.Generation{}, false, contentWriteError("publish content head", err)
	}
	if err := tx.Commit(); err != nil {
		return jobcontent.Generation{}, false, contentWriteError("commit content publication", err)
	}
	return next, true, nil
}

func (repository *Repository) ContentPublication(
	ctx context.Context,
	storeID, sha256 string,
) (jobcontent.Generation, error) {
	if !domain.IsUUID(storeID) || !jobcontract.IsSHA256(sha256) {
		return jobcontent.Generation{}, jobcontent.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobcontent.Generation{}, fmt.Errorf("begin content publication read: %w", err)
	}
	defer tx.Rollback()
	if err := rejectInactiveContentFacts(ctx, tx); err != nil {
		return jobcontent.Generation{}, err
	}
	return queryContentPublication(
		ctx,
		tx,
		storeID,
		sha256,
	)
}

func queryContentPublication(
	ctx context.Context,
	queryer rowQueryer,
	storeID, sha256 string,
) (jobcontent.Generation, error) {
	var generation jobcontent.Generation
	var object jobcontent.ObjectIdentity
	var createdAt, state string
	var number, objectRevision, headRevision int64
	err := queryer.QueryRowContext(
		ctx,
		`SELECT o.store_id, o.sha256, o.size, o.physical_incarnation_id,
		o.witness_sha256, o.verified_change_token, o.physical_state, o.revision, h.revision,
		g.generation, g.size, g.physical_incarnation_id, g.witness_sha256, g.created_at
		FROM content_objects o JOIN content_heads h ON h.store_id = o.store_id AND h.sha256 = o.sha256
		JOIN content_generations g ON g.store_id = h.store_id AND g.sha256 = h.sha256 AND g.generation = h.generation
		WHERE o.store_id = ? AND o.sha256 = ?`,
		storeID,
		sha256,
	).Scan(
		&object.StoreID,
		&object.Content.SHA256,
		&object.Content.Size,
		&object.IncarnationID,
		&object.WitnessSHA256,
		&object.ChangeToken,
		&state,
		&objectRevision,
		&headRevision,
		&number,
		&generation.Object.Content.Size,
		&generation.Object.IncarnationID,
		&generation.Object.WitnessSHA256,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if err := queryer.QueryRowContext(
			ctx,
			`SELECT EXISTS(SELECT 1 FROM content_objects WHERE store_id = ? AND sha256 = ?)
			OR EXISTS(SELECT 1 FROM content_heads WHERE store_id = ? AND sha256 = ?)
			OR EXISTS(SELECT 1 FROM content_generations WHERE store_id = ? AND sha256 = ?)`,
			storeID,
			sha256,
			storeID,
			sha256,
			storeID,
			sha256,
		).Scan(&exists); err != nil {
			return jobcontent.Generation{}, fmt.Errorf("inspect missing content head: %w", err)
		}
		if exists {
			return jobcontent.Generation{}, contentCorrupt("missing publication relation")
		}
		return jobcontent.Generation{}, jobcontent.ErrNotFound
	}
	if err != nil {
		return jobcontent.Generation{}, fmt.Errorf("read content publication: %w", err)
	}
	generation.Object.StoreID, generation.Object.Content.SHA256 = object.StoreID, object.Content.SHA256
	generation.Object.ChangeToken = object.ChangeToken
	generation.Number = uint64(number)
	generation.CreatedAt, err = contentTime(createdAt)
	validIdentity := object.StoreID == storeID && object.Content.SHA256 == sha256 && object == generation.Object
	validState := state == "present" && objectRevision > 0 && headRevision > 0 && number > 0
	if err != nil || !validIdentity || !validState || generation.Validate() != nil {
		return jobcontent.Generation{}, contentCorrupt("publication identity")
	}
	store, err := queryContentStore(ctx, queryer, storeID)
	if err != nil || generation.CreatedAt.Before(store.CreatedAt) {
		if err != nil && !errors.Is(err, jobcontent.ErrNotFound) {
			return jobcontent.Generation{}, err
		}
		return jobcontent.Generation{}, contentCorrupt("publication store")
	}
	return generation, nil
}

func (repository *Repository) JobContent(ctx context.Context, id string) (jobcontent.Inventory, error) {
	if !domain.IsUUID(id) {
		return jobcontent.Inventory{}, jobcontent.ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return jobcontent.Inventory{}, fmt.Errorf("begin content inventory read: %w", err)
	}
	defer tx.Rollback()
	return queryJobContent(ctx, tx, id)
}

func queryJobContent(ctx context.Context, queryer rowQueryer, id string) (jobcontent.Inventory, error) {
	if err := rejectInactiveContentFacts(ctx, queryer); err != nil {
		return jobcontent.Inventory{}, err
	}
	var revision sql.NullInt64
	var sealed sql.NullString
	var entries int64
	err := queryer.QueryRowContext(ctx, `SELECT c.inventory_revision, c.sealed_manifest_sha256,
		(SELECT COUNT(*) FROM job_content_entries WHERE job_id = j.id)
		FROM execution_jobs j LEFT JOIN execution_job_content c ON c.job_id = j.id WHERE j.id = ?`, id).
		Scan(&revision, &sealed, &entries)
	if errors.Is(err, sql.ErrNoRows) {
		return jobcontent.Inventory{}, jobcontent.ErrNotFound
	}
	if err != nil {
		return jobcontent.Inventory{}, fmt.Errorf("read content inventory: %w", err)
	}
	// This slice publishes content but does not yet admit physical asset pins or
	// capture/seal/release inventory. Unknown nonempty facts must fail explicitly.
	if !revision.Valid || revision.Int64 != 1 || sealed.Valid || entries != 0 {
		return jobcontent.Inventory{}, contentCorrupt("unsupported content inventory facts")
	}
	spec, err := queryJobSpec(ctx, queryer, id)
	if err != nil {
		return jobcontent.Inventory{}, err
	}
	if len(spec.Assets) != 0 {
		return jobcontent.Inventory{}, contentCorrupt("unadmitted asset inventory")
	}
	return jobcontent.Inventory{JobID: id, Revision: 1, Entries: []jobcontent.Entry{}}, nil
}

func contentTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.IsZero() || formatTime(parsed) != value {
		return time.Time{}, contentCorrupt("content timestamp")
	}
	return parsed, nil
}

func contentCorrupt(detail string) error {
	return fmt.Errorf(
		"%w: %w: %s",
		jobcontent.ErrCorrupt,
		ErrCorrupt,
		detail,
	)
}

func contentWriteError(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	err := classifyWriteError(operation, cause)
	if errors.Is(err, ErrConflict) {
		return fmt.Errorf("%w: %w", jobcontent.ErrConflict, err)
	}
	return err
}
