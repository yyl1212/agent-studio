package backup

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/internal/database"
)

// RestorePlan describes the checks a restore would perform without changing the target.
type RestorePlan struct {
	Archive                Summary `json:"archive"`
	TargetMigrationVersion int64   `json:"targetMigrationVersion"`
	LatestMigrationVersion int64   `json:"latestMigrationVersion"`
	PendingMigrations      []int64 `json:"pendingMigrations"`
	TargetEmpty            bool    `json:"targetEmpty"`
}

type importerKey struct {
	APIVersion       string
	MigrationVersion int64
}

// importer restores one explicitly supported archive representation.
type importer func(context.Context, *database.MaintenanceLease, *Archive, RestorePlan, restoreHooks) (RestoreResult, error)

var importers = map[importerKey]importer{
	{APIVersion: APIVersion, MigrationVersion: 6}: importV1Alpha1Migration6,
}

type dryRunHooks struct {
	afterLeaseTransaction func(context.Context, pgx.Tx)
}

// DryRun validates an archive and its restore prerequisites without changing the target.
func DryRun(ctx context.Context, pool *pgxpool.Pool, path string) (RestorePlan, error) {
	return dryRunWithHooks(ctx, pool, path, dryRunHooks{})
}

func dryRunWithHooks(ctx context.Context, pool *pgxpool.Pool, path string, hooks dryRunHooks) (RestorePlan, error) {
	archive, err := OpenArchive(ctx, path)
	if err != nil {
		return RestorePlan{}, err
	}
	defer archive.Close()

	lease, err := database.TryExclusive(ctx, pool)
	if errors.Is(err, database.ErrMaintenanceBusy) {
		return RestorePlan{}, Wrap(CodeAPIRunning, "target is in use", err)
	}
	if err != nil {
		return RestorePlan{}, Wrap(CodeRestoreFailed, "acquire maintenance lease", err)
	}
	defer lease.Release(context.Background())
	preflightContext, stopMonitoring := monitorLeaseLoss(ctx, lease.MonitorConnectionLoss())
	defer stopMonitoring()

	plan, err := preflightWithLeaseAndHooks(preflightContext, lease, archive, hooks)
	if ctx.Err() == nil && context.Cause(preflightContext) != nil {
		return RestorePlan{}, Wrap(CodeRestoreFailed, "target maintenance lease lost", context.Cause(preflightContext))
	}
	return plan, err
}

// preflightWithLease validates a verified archive while the caller holds the target's
// exclusive maintenance lease. It never opens the archive, runs migrations, or commits.
func preflightWithLease(ctx context.Context, lease *database.MaintenanceLease, archive *Archive) (RestorePlan, error) {
	return preflightWithLeaseAndHooks(ctx, lease, archive, dryRunHooks{})
}

func preflightWithLeaseAndHooks(ctx context.Context, lease *database.MaintenanceLease, archive *Archive, hooks dryRunHooks) (RestorePlan, error) {
	if err := ctx.Err(); err != nil {
		return RestorePlan{}, err
	}
	if lease == nil || archive == nil {
		return RestorePlan{}, Wrap(CodeArchiveInvalid, "validate opened backup archive", nil)
	}

	latest, err := database.LatestVersion()
	if err != nil {
		return RestorePlan{}, Wrap(CodeRestoreFailed, "read runtime migration version", err)
	}
	if archive.manifest.APIVersion != APIVersion {
		return RestorePlan{}, Wrap(CodeFormatUnsupported, "validate backup api version", nil)
	}
	if archive.manifest.DatabaseMigrationVersion > latest {
		return RestorePlan{}, Wrap(CodeRuntimeTooOld, "backup migration is newer than runtime", nil)
	}
	if archive.manifest.DatabaseMigrationVersion < 1 {
		return RestorePlan{}, Wrap(CodeArchiveInvalid, "validate backup migration version", nil)
	}

	targetVersion, err := lease.CurrentVersion(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return RestorePlan{}, err
		}
		return RestorePlan{}, Wrap(CodeRestoreFailed, "read target migration version", err)
	}
	if targetVersion > latest {
		return RestorePlan{}, Wrap(CodeRuntimeTooOld, "target migration is newer than runtime", nil)
	}
	if _, ok := importers[importerKey{
		APIVersion: archive.manifest.APIVersion, MigrationVersion: archive.manifest.DatabaseMigrationVersion,
	}]; !ok {
		return RestorePlan{}, Wrap(CodeFormatUnsupported, "backup migration is not supported", nil)
	}

	transaction, err := lease.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return RestorePlan{}, err
		}
		return RestorePlan{}, Wrap(CodeRestoreFailed, "begin backup reference validation", err)
	}
	defer transaction.Rollback(context.Background())
	if hooks.afterLeaseTransaction != nil {
		hooks.afterLeaseTransaction(ctx, transaction)
	}

	targetEmpty, err := targetIsEmpty(ctx, transaction)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return RestorePlan{}, err
		}
		return RestorePlan{}, Wrap(CodeRestoreFailed, "check target contents", err)
	}
	if !targetEmpty {
		return RestorePlan{}, Wrap(CodeTargetNotEmpty, "target contains backup data", nil)
	}
	if _, err := stageReferences(ctx, transaction, archive); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || CodeOf(err) != "" {
			return RestorePlan{}, err
		}
		return RestorePlan{}, Wrap(CodeRestoreFailed, "validate backup references", err)
	}

	return RestorePlan{
		Archive:                archive.Summary(),
		TargetMigrationVersion: targetVersion,
		LatestMigrationVersion: latest,
		PendingMigrations:      pendingMigrations(targetVersion, latest),
		TargetEmpty:            true,
	}, nil
}

func targetIsEmpty(ctx context.Context, querier database.RowQuerier) (bool, error) {
	for _, name := range TableOrder {
		var relation *string
		if err := querier.QueryRow(ctx, `SELECT to_regclass($1)::text`, string(name)).Scan(&relation); err != nil {
			return false, err
		}
		if relation == nil {
			continue
		}
		var exists bool
		statement := "SELECT EXISTS(SELECT 1 FROM " + pgx.Identifier{string(name)}.Sanitize() + " LIMIT 1)"
		if err := querier.QueryRow(ctx, statement).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
	}
	return true, nil
}

func pendingMigrations(targetVersion, latestVersion int64) []int64 {
	pending := make([]int64, 0, latestVersion-targetVersion)
	for version := targetVersion + 1; version <= latestVersion; version++ {
		pending = append(pending, version)
	}
	return pending
}
