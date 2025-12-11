// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package operations

import (
	"context"
	gosql "database/sql"
	"fmt"
	"reflect"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/modular"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/catpb"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/fingerprintutils"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
)

// TODO: FIXUP this backup_restore operation
// Make it so we test backing up and restoring multiple databases/tables, sometimes under the same hierarchy

// BackupRestoreOptions contains configuration for backup/restore operations
type BackupRestoreOptions struct {
	// Online determines whether to use online (deferred copy) restore
	Online bool
	// Validate determines whether to fingerprint and verify the restored database
	Validate bool
	// IncrementalLayers is the number of incremental backup layers to create (offline only)
	IncrementalLayers int
	// DatabaseWhitelist specifies which databases to look for (defaults to ["cct_tpcc", "tpcc"])
	DatabaseWhitelist []string
	// Database specifies the database name to backup/restore (if set, overrides whitelist search)
	Database string
	// Table specifies the table name for restore resource locking
	Table string
}

// BackupRestoreOp implements the Operation interface for backup/restore operations
type BackupRestoreOp struct {
	name    string
	builder *modular.OperationBuilder
	opts    BackupRestoreOptions
	cluster cluster.Cluster
}

// Chain returns the operation's chain of steps
func (b *BackupRestoreOp) Chain() modular.Chain {
	return b.builder.Chain
}

// Name returns the operation's name
func (b *BackupRestoreOp) Name() string {
	return b.name
}

func (b *BackupRestoreOp) Timeout() time.Duration {
	if b.opts.Validate {
		return 96 * time.Hour
	}
	return 24 * time.Hour
}

// findDatabaseToBackup searches for a database from the whitelist
func findDatabaseToBackup(ctx context.Context, db *gosql.DB, whitelist []string) (string, error) {
	dbs, err := db.QueryContext(ctx, "SELECT database_name FROM [SHOW DATABASES]")
	if err != nil {
		return "", err
	}
	defer dbs.Close()

	for dbs.Next() {
		var dbStr string
		if err := dbs.Scan(&dbStr); err != nil {
			return "", err
		}
		for _, whitelisted := range whitelist {
			if whitelisted == dbStr {
				return dbStr, nil
			}
		}
	}
	return "", nil
}

// waitForRestoreJob waits for the online restore download job to complete
func waitForRestoreJob(ctx context.Context, l *logger.Logger, db *gosql.DB, jobID catpb.JobID, timeout time.Duration) error {
	deadline := timeutil.Now().Add(timeout)
	for timeutil.Now().Before(deadline) {
		var status string
		err := db.QueryRowContext(ctx, "SELECT status FROM [SHOW JOBS] WHERE job_id = $1", jobID).Scan(&status)
		if err != nil {
			return fmt.Errorf("failed to query job status: %w", err)
		}

		switch status {
		case "succeeded":
			l.Printf("Online restore download job %d completed successfully", jobID)
			return nil
		case "failed", "canceled":
			return fmt.Errorf("online restore download job %d finished with status: %s", jobID, status)
		}

		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("online restore download job %d did not complete within %s", jobID, timeout)
}

// BackupRestore creates a backup/restore operation with the given options
func BackupRestore(c cluster.Cluster, opts BackupRestoreOptions) modular.Operation {
	// Set defaults
	if opts.IncrementalLayers == 0 {
		opts.IncrementalLayers = 24
	}
	if opts.DatabaseWhitelist == nil {
		opts.DatabaseWhitelist = []string{"cct_tpcc", "tpcc"}
	}

	rng, _ := randutil.NewPseudoRand()
	var dbName string
	var backupTS hlc.Timestamp
	var bucket string
	var restoreDBName string

	// Create a RestoreAccess resource for locking restore operations
	restoreRes := modular.RestoreAccess{
		Database: opts.Database,
		Table:    opts.Table,
	}

	builder := modular.NewOperation(
		modular.NewStep("find database and create full backup", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		// If Database is specified in options, use it directly
		if opts.Database != "" {
			dbName = opts.Database
			l.Printf("using specified database: %s", dbName)
		} else {
			// Otherwise, search for a database from the whitelist
			db := h.RandomDBConn()

			foundDB, err := findDatabaseToBackup(ctx, db, opts.DatabaseWhitelist)
			if err != nil {
				return fmt.Errorf("failed to find database: %w", err)
			}
			if foundDB == "" {
				l.Printf("did not find a db in the whitelist %v, skipping backup/restore", opts.DatabaseWhitelist)
				return nil
			}

			dbName = foundDB
			l.Printf("found database to backup: %s", dbName)
		}

		// Create full backup
		db := h.RandomDBConn()
		bucket = fmt.Sprintf("gs://%s/operation-backup-restore/%d/?AUTH=implicit", testutils.BackupTestingBucket(), timeutil.Now().UnixNano())
		backupTS = hlc.Timestamp{WallTime: timeutil.Now().Add(-10 * time.Second).UTC().UnixNano()}

		l.Printf("backing up db %s (full) to %s", dbName, bucket)

		var backupSQL string
		if !opts.Online {
			backupSQL = fmt.Sprintf("BACKUP DATABASE %s INTO '%s' AS OF SYSTEM TIME '%s' WITH revision_history", dbName, bucket, backupTS.AsOfSystemTime())
		} else {
			// Revision history doesn't work with online restore
			backupSQL = fmt.Sprintf("BACKUP DATABASE %s INTO '%s' AS OF SYSTEM TIME '%s'", dbName, bucket, backupTS.AsOfSystemTime())
		}

		_, err := db.ExecContext(ctx, backupSQL)
		return err
	}),
	)

	// Add incremental backups for offline restore
	if !opts.Online {
		for i := 0; i < opts.IncrementalLayers; i++ {
			layer := i // Capture loop variable
			builder = builder.Then(
				modular.NewStep(fmt.Sprintf("create incremental backup (layer %d)", layer), func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
				if dbName == "" {
					return nil // Skip if no database found
				}

				db := h.RandomDBConn()
				// Update backupTS to match the latest layer
				backupTS = hlc.Timestamp{WallTime: timeutil.Now().Add(-10 * time.Second).UTC().UnixNano()}

				l.Printf("backing up db %s (incremental layer %d)", dbName, layer)
				backupSQL := fmt.Sprintf("BACKUP DATABASE %s INTO LATEST IN '%s' AS OF SYSTEM TIME '%s' WITH revision_history", dbName, bucket, backupTS.AsOfSystemTime())
				_, err := db.ExecContext(ctx, backupSQL)
				return err
			}),
			)
		}
	}

	// Add restore step with exclusive lock on restore operations
	// If not validating, release the lock in this step; otherwise release after validation
	restoreStepOpts := []modular.StepOption{modular.AcquireLock(restoreRes)}
	if !opts.Validate {
		restoreStepOpts = append(restoreStepOpts, modular.ReleaseLock(restoreRes))
	}

	builder = builder.Then(
		modular.NewStep("restore database", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
		if dbName == "" {
			return nil // Skip if no database found
		}

		db := h.RandomDBConn()
		restoreDBName = fmt.Sprintf("backup_restore_op_%d", rng.Int63())

		onlineStr := "offline"
		if opts.Online {
			onlineStr = "online"
		}
		l.Printf("restoring %s into db %s", onlineStr, restoreDBName)

		startTime := timeutil.Now()
		if !opts.Online {
			l.Printf("beginning offline restore")
			restoreSQL := fmt.Sprintf("RESTORE DATABASE %s FROM LATEST IN '%s' WITH OPTIONS (new_db_name = '%s')", dbName, bucket, restoreDBName)
			_, err := db.ExecContext(ctx, restoreSQL)
			if err != nil {
				return err
			}
		} else {
			l.Printf("beginning online restore")
			restoreSQL := fmt.Sprintf("RESTORE DATABASE %s FROM LATEST IN '%s' WITH OPTIONS (new_db_name = '%s', EXPERIMENTAL DEFERRED COPY)", dbName, bucket, restoreDBName)

			var id, tables, approxRows, approxBytes int64
			var downloadJobID catpb.JobID
			err := db.QueryRowContext(ctx, restoreSQL).Scan(&id, &tables, &approxRows, &approxBytes, &downloadJobID)
			if err != nil {
				return fmt.Errorf("failed to start online restore: %w", err)
			}

			l.Printf("waiting for online restore download job %d", downloadJobID)
			if err := waitForRestoreJob(ctx, l, db, downloadJobID, 24*time.Hour); err != nil {
				return err
			}
		}
		l.Printf("completed restore in %v", timeutil.Since(startTime))
		return nil
	}, restoreStepOpts...),
	)

	// Add validation step if requested (and release lock after validation)
	if opts.Validate {
		builder = builder.Then(
			modular.NewStep("validate restored database", func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
			if dbName == "" || restoreDBName == "" {
				return nil // Skip if no database found or restore didn't happen
			}

			db := h.RandomDBConn()
			l.Printf("verifying db %s matches %s", dbName, restoreDBName)

			sourceFingerprints, err := fingerprintutils.FingerprintDatabase(ctx, db, dbName, fingerprintutils.AOST(backupTS), fingerprintutils.Stripped())
			if err != nil {
				return fmt.Errorf("failed to fingerprint source database: %w", err)
			}

			// No AOST here; the timestamps are rewritten on restore
			destFingerprints, err := fingerprintutils.FingerprintDatabase(ctx, db, restoreDBName, fingerprintutils.Stripped())
			if err != nil {
				return fmt.Errorf("failed to fingerprint restored database: %w", err)
			}

			if !reflect.DeepEqual(sourceFingerprints, destFingerprints) {
				return fmt.Errorf("backup and restore fingerprints do not match: %v != %v", sourceFingerprints, destFingerprints)
			}

			l.Printf("validation successful: fingerprints match")
			return nil
		}, modular.ReleaseLock(restoreRes)),
		)
	}

	onlineStr := "offline"
	if opts.Online {
		onlineStr = "online"
	}
	validateStr := "no-validate"
	if opts.Validate {
		validateStr = "validate"
	}

	return &BackupRestoreOp{
		name:    fmt.Sprintf("backup-restore/%s/%s", onlineStr, validateStr),
		builder: builder,
		opts:    opts,
		cluster: c,
	}
}

// BackupRestorePlan contains the table selection and backup details from PrePlan
type BackupRestorePlan struct {
	DBName      string
	TableName   string
	BackupPath  string
	RestoreDBName string
}

// BackupRestoreDynamic creates a dynamic backup/restore operation that selects a random table.
// This demonstrates dynamic resource selection with two distinct steps: backup and restore.
// BackupRestoreDatabaseDynamic creates a dynamic operation that backs up and restores an entire database.
// The database is selected during PrePlan, making it truly dynamic.
func BackupRestoreDatabaseDynamic() modular.Operation {
	rng, _ := randutil.NewPseudoRand()

	type DatabaseBackupPlan struct {
		DBName        string
		BackupPath    string
		RestoreDBName string
		BackupTS      hlc.Timestamp
	}

	var plan *DatabaseBackupPlan

	// First step: Select database and perform full backup
	dynamicStep := modular.NewDynamicOperation[*DatabaseBackupPlan]("backup random database (dynamic)").
		PrePlan(func(ctx context.Context, l *logger.Logger, h *modular.Helper) (*DatabaseBackupPlan, error) {
			db := h.RandomDBConn()

			// Find a database from the whitelist (tpcc, cct_tpcc, or bank)
			whitelist := []string{"tpcc", "cct_tpcc", "bank"}
			dbName, err := findDatabaseToBackup(ctx, db, whitelist)
			if err != nil {
				return nil, fmt.Errorf("failed to find database: %w", err)
			}
			if dbName == "" {
				return nil, fmt.Errorf("no database found in whitelist %v", whitelist)
			}

			l.Printf("Selected database for backup: %s", dbName)

			backupPath := fmt.Sprintf("gs://%s/operation-backup-restore/%d/?AUTH=implicit",
				testutils.BackupTestingBucket(), timeutil.Now().UnixNano())
			restoreDBName := fmt.Sprintf("%s_restored_%d", dbName, rng.Int63())
			backupTS := hlc.Timestamp{WallTime: timeutil.Now().Add(-10 * time.Second).UTC().UnixNano()}

			p := &DatabaseBackupPlan{
				DBName:        dbName,
				BackupPath:    backupPath,
				RestoreDBName: restoreDBName,
				BackupTS:      backupTS,
			}
			plan = p
			return p, nil
		}).
		WithRun(func(ctx context.Context, l *logger.Logger, h *modular.Helper, p *DatabaseBackupPlan) error {
			db := h.RandomDBConn()

			l.Printf("Backing up database %s (full) to %s", p.DBName, p.BackupPath)
			backupSQL := fmt.Sprintf("BACKUP DATABASE %s INTO '%s' AS OF SYSTEM TIME '%s' WITH revision_history",
				p.DBName, p.BackupPath, p.BackupTS.AsOfSystemTime())

			if _, err := db.ExecContext(ctx, backupSQL); err != nil {
				return fmt.Errorf("full backup failed: %w", err)
			}

			return nil
		}).
		WithDynamicResourceCallback(func(p *DatabaseBackupPlan) ([]modular.ResourceAccess, []modular.ResourceAccess) {
			access := modular.RestoreAccess{
				Database: p.DBName,
			}.Resource(true)
			return []modular.ResourceAccess{access}, []modular.ResourceAccess{}
		}).
		WithDynamicName(func(p *DatabaseBackupPlan) string {
			return fmt.Sprintf("backup database %s (full)", p.DBName)
		})

	// Wrap the dynamic step in an operation and add subsequent steps
	builder := modular.NewOperation(dynamicStep).
		// Second step: Incremental backup
		Then(
		modular.NewStep("create incremental backup",
			func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
				if plan == nil {
					return fmt.Errorf("no backup plan available")
				}

				db := h.RandomDBConn()
				newBackupTS := hlc.Timestamp{WallTime: timeutil.Now().Add(-10 * time.Second).UTC().UnixNano()}

				l.Printf("Backing up database %s (incremental) to %s", plan.DBName, plan.BackupPath)
				backupSQL := fmt.Sprintf("BACKUP DATABASE %s INTO LATEST IN '%s' AS OF SYSTEM TIME '%s' WITH revision_history",
					plan.DBName, plan.BackupPath, newBackupTS.AsOfSystemTime())

				if _, err := db.ExecContext(ctx, backupSQL); err != nil {
					return fmt.Errorf("incremental backup failed: %w", err)
				}

				return nil
			},
		),
	).
		// Third step: Restore the database
		Then(
		modular.NewStep("restore database",
			func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
				if plan == nil {
					return fmt.Errorf("no backup plan available")
				}

				db := h.RandomDBConn()

				l.Printf("Restoring database as %s", plan.RestoreDBName)
				restoreSQL := fmt.Sprintf("RESTORE DATABASE %s FROM LATEST IN '%s' WITH new_db_name = '%s'",
					plan.DBName, plan.BackupPath, plan.RestoreDBName)

				if _, err := db.ExecContext(ctx, restoreSQL); err != nil {
					return fmt.Errorf("restore failed: %w", err)
				}

				l.Printf("Successfully restored %s to %s", plan.DBName, plan.RestoreDBName)
				return nil
			},
			// Release the lock that was acquired in the backup step
			modular.ReleaseLock(modular.RestoreAccess{}),
		),
	)

	return &BackupRestoreOp{
		name:    "backup-restore-database-dynamic",
		builder: builder,
	}
}

func BackupRestoreDynamic() modular.Operation {
	// Variables to store the plan across steps
	rng, _ := randutil.NewPseudoRand()
	var plan *BackupRestorePlan

	// Create a shared restore resource that will be populated by PrePlan
	restoreRes := modular.RestoreAccess{}

	// First step: Select table and perform backup
	dynamicStep := modular.NewDynamicOperation[*BackupRestorePlan]("backup random table (dynamic)").
		PrePlan(func(ctx context.Context, l *logger.Logger, h *modular.Helper) (*BackupRestorePlan, error) {
			// Search for a random table
			dbName, tableName, err := h.SearchTable(func(dbName, tableName string) bool {
				// Skip system tables
				return true
			})
			if err != nil {
				return nil, fmt.Errorf("failed to find table: %w", err)
			}

			l.Printf("Selected table for backup: %s.%s", dbName, tableName)

			// Use nodelocal storage for local testing
			backupPath := fmt.Sprintf("nodelocal://1/backup_%d", timeutil.Now().UnixNano())
			restoreDBName := fmt.Sprintf("restore_%d", rng.Int63())

			p := &BackupRestorePlan{
				DBName:        dbName,
				TableName:     tableName,
				BackupPath:    backupPath,
				RestoreDBName: restoreDBName,
			}
			plan = p // Store for use by restore step
			return p, nil
		}).
		WithRun(func(ctx context.Context, l *logger.Logger, h *modular.Helper, p *BackupRestorePlan) error {
			db := h.RandomDBConn()

			// Backup the table
			l.Printf("Backing up %s.%s to %s", p.DBName, p.TableName, p.BackupPath)
			backupSQL := fmt.Sprintf("BACKUP TABLE %s.%s INTO '%s'",
				p.DBName, p.TableName, p.BackupPath)
			if _, err := db.ExecContext(ctx, backupSQL); err != nil {
				return fmt.Errorf("backup failed: %w", err)
			}

			return nil
		}).
		WithDynamicResourceCallback(func(p *BackupRestorePlan) ([]modular.ResourceAccess, []modular.ResourceAccess) {
			access := modular.RestoreAccess{
				Database: p.DBName,
				Table:    p.TableName,
			}.Resource(true)
			return []modular.ResourceAccess{access}, []modular.ResourceAccess{}
		}).
		WithDynamicName(func(p *BackupRestorePlan) string {
			return fmt.Sprintf("backup %s.%s", p.DBName, p.TableName)
		})

	// Wrap the dynamic step and add the restore step
	builder := modular.NewOperation(dynamicStep).
		// Second step: Restore the backed up table
		Then(
		modular.NewStep("restore table",
			func(ctx context.Context, l *logger.Logger, h *modular.Helper) error {
				if plan == nil {
					return fmt.Errorf("no backup plan available")
				}

				db := h.RandomDBConn()

				// Create a new database for restore
				l.Printf("Creating restore database: %s", plan.RestoreDBName)
				createDBSQL := fmt.Sprintf("CREATE DATABASE %s", plan.RestoreDBName)
				if _, err := db.ExecContext(ctx, createDBSQL); err != nil {
					return fmt.Errorf("create database failed: %w", err)
				}

				// Restore the table into the new database
				l.Printf("Restoring table as %s.%s", plan.RestoreDBName, plan.TableName)
				restoreSQL := fmt.Sprintf("RESTORE TABLE %s.%s FROM LATEST IN '%s' WITH into_db = '%s'",
					plan.DBName, plan.TableName, plan.BackupPath, plan.RestoreDBName)
				if _, err := db.ExecContext(ctx, restoreSQL); err != nil {
					return fmt.Errorf("restore failed: %w", err)
				}

				l.Printf("Successfully restored %s.%s to %s.%s", plan.DBName, plan.TableName, plan.RestoreDBName, plan.TableName)
				return nil
			},
			// Release the lock that was acquired in the backup step
			modular.ReleaseLock(restoreRes),
		),
	)

	return &BackupRestoreOp{
		name:    "backup-restore-dynamic",
		builder: builder,
	}
}
