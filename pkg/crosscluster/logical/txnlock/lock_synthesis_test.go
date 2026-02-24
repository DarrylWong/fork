// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package txnlock

import (
	"context"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/crosscluster/logical/ldrdecoder"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/desctestutils"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/lease"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/sqlutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/stretchr/testify/require"
)

func TestLockSynthesisNoConstraint(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, conn, kvDB := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	s := srv.ApplicationLayer()
	codec := s.Codec()

	runner := sqlutils.MakeSQLRunner(conn)

	// Create two tables with no unique constraints
	runner.Exec(t, `
		CREATE TABLE table1 (
			id INT PRIMARY KEY,
			data STRING
		)
	`)
	runner.Exec(t, `
		CREATE TABLE table2 (
			id INT PRIMARY KEY,
			value INT
		)
	`)

	// Get table descriptors
	table1Desc := desctestutils.TestingGetTableDescriptor(kvDB, codec, "defaultdb", "public", "table1")
	table2Desc := desctestutils.TestingGetTableDescriptor(kvDB, codec, "defaultdb", "public", "table2")

	table1ID := table1Desc.GetID()
	table2ID := table2Desc.GetID()

	// Create lock synthesizer
	ls, err := NewLockSynthesizer(
		ctx,
		s.LeaseManager().(*lease.Manager),
		s.Clock(),
		[]ldrdecoder.TableMapping{
			{DestID: table1ID},
			{DestID: table2ID},
		},
	)
	require.NoError(t, err)

	// Create test rows - simple inserts/updates with no dependencies
	rows := []ldrdecoder.DecodedRow{
		{
			TableID:  table1ID,
			Row:      tree.Datums{tree.NewDInt(1), tree.NewDString("data1")},
			PrevRow:  nil,
			IsDelete: false,
		},
		{
			TableID:  table1ID,
			Row:      tree.Datums{tree.NewDInt(2), tree.NewDString("data2")},
			PrevRow:  nil,
			IsDelete: false,
		},
		{
			TableID:  table2ID,
			Row:      tree.Datums{tree.NewDInt(1), tree.NewDInt(100)},
			PrevRow:  nil,
			IsDelete: false,
		},
	}

	// Derive locks
	lockSet, err := ls.DeriveLocks(rows)
	require.NoError(t, err)

	// Verify we got locks - at least one per table since there are no unique constraints
	require.Len(t, lockSet.Locks, 3)

	// Verify sorted rows - with no dependencies, order should be preserved
	require.Len(t, lockSet.SortedRows, 3)
}

func TestLockSynthesisForeignKey(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, conn, kvDB := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	s := srv.ApplicationLayer()
	codec := s.Codec()

	runner := sqlutils.MakeSQLRunner(conn)

	runner.Exec(t, `
		CREATE TABLE parent (
			id INT PRIMARY KEY,
			data STRING
		)
	`)
	runner.Exec(t, `
		CREATE TABLE child (
			id INT PRIMARY KEY,
			parent_id INT REFERENCES parent(id),
			info STRING
		)
	`)

	parentDesc := desctestutils.TestingGetTableDescriptor(kvDB, codec, "defaultdb", "public", "parent")
	childDesc := desctestutils.TestingGetTableDescriptor(kvDB, codec, "defaultdb", "public", "child")
	parentID := parentDesc.GetID()
	childID := childDesc.GetID()

	makeLs := func() *LockSynthesizer {
		ls, err := NewLockSynthesizer(
			ctx,
			s.LeaseManager().(*lease.Manager),
			s.Clock(),
			[]ldrdecoder.TableMapping{
				{DestID: parentID},
				{DestID: childID},
			},
		)
		require.NoError(t, err)
		return ls
	}

	t.Run("fk_read_lock_emitted", func(t *testing.T) {
		// Inserting a child row with a non-null FK should emit a read lock
		// whose hash collides with the parent's PK write lock for the same value.
		ls := makeLs()

		// child: (id=1, parent_id=10, info='x')
		childRow := ldrdecoder.DecodedRow{
			TableID: childID,
			Row:     tree.Datums{tree.NewDInt(1), tree.NewDInt(10), tree.NewDString("x")},
		}
		// parent: (id=10, data='hello')
		parentRow := ldrdecoder.DecodedRow{
			TableID: parentID,
			Row:     tree.Datums{tree.NewDInt(10), tree.NewDString("hello")},
		}

		childLocks := ls.deriveLocks(childRow, nil)
		parentLocks := ls.deriveLocks(parentRow, nil)

		// Child should have a PK write lock and an FK read lock.
		require.Len(t, childLocks, 2)
		// First lock is PK (write), second is FK (read).
		require.False(t, childLocks[0].Read, "PK lock should be write")
		require.True(t, childLocks[1].Read, "FK lock should be read")

		// The FK read lock hash should equal the parent's PK write lock hash.
		require.Equal(t, parentLocks[0].Hash, childLocks[1].Hash,
			"FK read lock hash should collide with parent PK write lock hash")
	})

	t.Run("fk_no_lock_when_null", func(t *testing.T) {
		// A child row with a NULL FK value should not emit an FK read lock.
		ls := makeLs()

		childRow := ldrdecoder.DecodedRow{
			TableID: childID,
			Row:     tree.Datums{tree.NewDInt(1), tree.DNull, tree.NewDString("x")},
		}

		childLocks := ls.deriveLocks(childRow, nil)
		// Only PK write lock, no FK read lock.
		require.Len(t, childLocks, 1)
		require.False(t, childLocks[0].Read)
	})

	t.Run("fk_no_lock_when_unchanged", func(t *testing.T) {
		// Updating a child row without changing the FK value should not emit
		// an FK read lock.
		ls := makeLs()

		childRow := ldrdecoder.DecodedRow{
			TableID: childID,
			Row:     tree.Datums{tree.NewDInt(1), tree.NewDInt(10), tree.NewDString("updated")},
			PrevRow: tree.Datums{tree.NewDInt(1), tree.NewDInt(10), tree.NewDString("original")},
		}

		childLocks := ls.deriveLocks(childRow, nil)
		// Only PK write lock; FK value unchanged so no FK lock.
		require.Len(t, childLocks, 1)
	})

	t.Run("insert_ordering_parent_before_child", func(t *testing.T) {
		// When a transaction inserts both a parent and child row, the parent
		// insert must be applied before the child insert.
		ls := makeLs()

		rows := []ldrdecoder.DecodedRow{
			// Child insert first in input order.
			{
				TableID: childID,
				Row:     tree.Datums{tree.NewDInt(1), tree.NewDInt(10), tree.NewDString("x")},
			},
			// Parent insert second in input order.
			{
				TableID: parentID,
				Row:     tree.Datums{tree.NewDInt(10), tree.NewDString("hello")},
			},
		}

		lockSet, err := ls.DeriveLocks(rows)
		require.NoError(t, err)
		require.Len(t, lockSet.SortedRows, 2)

		// Parent must come first in sorted output.
		require.Equal(t, parentID, lockSet.SortedRows[0].TableID,
			"parent insert should be sorted before child insert")
		require.Equal(t, childID, lockSet.SortedRows[1].TableID,
			"child insert should be sorted after parent insert")
	})

	t.Run("delete_ordering_child_before_parent", func(t *testing.T) {
		// When a transaction deletes both a child and parent row, the child
		// delete must be applied before the parent delete.
		ls := makeLs()

		rows := []ldrdecoder.DecodedRow{
			// Parent delete first in input order.
			{
				TableID:  parentID,
				Row:      tree.Datums{tree.NewDInt(10), tree.DNull},
				PrevRow:  tree.Datums{tree.NewDInt(10), tree.NewDString("hello")},
				IsDelete: true,
			},
			// Child delete second in input order.
			{
				TableID:  childID,
				Row:      tree.Datums{tree.NewDInt(1), tree.DNull, tree.DNull},
				PrevRow:  tree.Datums{tree.NewDInt(1), tree.NewDInt(10), tree.NewDString("x")},
				IsDelete: true,
			},
		}

		lockSet, err := ls.DeriveLocks(rows)
		require.NoError(t, err)
		require.Len(t, lockSet.SortedRows, 2)

		// Child must come first in sorted output.
		require.Equal(t, childID, lockSet.SortedRows[0].TableID,
			"child delete should be sorted before parent delete")
		require.Equal(t, parentID, lockSet.SortedRows[1].TableID,
			"parent delete should be sorted after child delete")
	})

	t.Run("no_ordering_between_unrelated_inserts", func(t *testing.T) {
		// A parent insert and child delete with NULL FK should not
		// create any ordering constraint. Input order should be preserved.
		ls := makeLs()

		rows := []ldrdecoder.DecodedRow{
			{
				TableID: parentID,
				Row:     tree.Datums{tree.NewDInt(10), tree.NewDString("hello")},
			},
			{
				TableID:  childID,
				Row:      tree.Datums{tree.NewDInt(1), tree.DNull, tree.DNull},
				PrevRow:  tree.Datums{tree.NewDInt(1), tree.DNull, tree.NewDString("x")},
				IsDelete: true,
			},
		}

		lockSet, err := ls.DeriveLocks(rows)
		require.NoError(t, err)
		require.Len(t, lockSet.SortedRows, 2)

		// Input order preserved since there is no FK dependency (child FK is null).
		require.Equal(t, parentID, lockSet.SortedRows[0].TableID)
		require.Equal(t, childID, lockSet.SortedRows[1].TableID)
	})

	t.Run("fk_referencing_unique_constraint", func(t *testing.T) {
		// FK can reference a unique constraint rather than the PK.
		// Verify ordering is still enforced.
		runner.Exec(t, `
			CREATE TABLE uc_parent (
				id INT PRIMARY KEY,
				code STRING UNIQUE
			)
		`)
		runner.Exec(t, `
			CREATE TABLE uc_child (
				id INT PRIMARY KEY,
				parent_code STRING REFERENCES uc_parent(code)
			)
		`)

		ucParentDesc := desctestutils.TestingGetTableDescriptor(
			kvDB, codec, "defaultdb", "public", "uc_parent")
		ucChildDesc := desctestutils.TestingGetTableDescriptor(
			kvDB, codec, "defaultdb", "public", "uc_child")
		ucParentID := ucParentDesc.GetID()
		ucChildID := ucChildDesc.GetID()

		ucLs, err := NewLockSynthesizer(
			ctx,
			s.LeaseManager().(*lease.Manager),
			s.Clock(),
			[]ldrdecoder.TableMapping{
				{DestID: ucParentID},
				{DestID: ucChildID},
			},
		)
		require.NoError(t, err)

		// Child insert referencing parent's UC column should be ordered
		// after parent insert.
		rows := []ldrdecoder.DecodedRow{
			{
				TableID: ucChildID,
				Row:     tree.Datums{tree.NewDInt(1), tree.NewDString("abc")},
			},
			{
				TableID: ucParentID,
				Row:     tree.Datums{tree.NewDInt(10), tree.NewDString("abc")},
			},
		}

		lockSet, err := ucLs.DeriveLocks(rows)
		require.NoError(t, err)
		require.Len(t, lockSet.SortedRows, 2)
		require.Equal(t, ucParentID, lockSet.SortedRows[0].TableID,
			"parent insert should be sorted before child insert when FK references UC")
		require.Equal(t, ucChildID, lockSet.SortedRows[1].TableID,
			"child insert should be sorted after parent insert when FK references UC")
	})
}

func TestLockSynthesisUniqueConstraint(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, conn, kvDB := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	s := srv.ApplicationLayer()
	codec := s.Codec()

	runner := sqlutils.MakeSQLRunner(conn)

	t.Run("delete_then_insert_unique_constraint", func(t *testing.T) {
		// Create a table with a unique constraint
		runner.Exec(t, `
			CREATE TABLE users (
				id INT PRIMARY KEY,
				email STRING UNIQUE
			)
		`)

		// Get table descriptor
		tableDesc := desctestutils.TestingGetTableDescriptor(kvDB, codec, "defaultdb", "public", "users")
		tableID := tableDesc.GetID()

		// Create lock synthesizer
		ls, err := NewLockSynthesizer(
			ctx,
			s.LeaseManager().(*lease.Manager),
			s.Clock(),
			[]ldrdecoder.TableMapping{{DestID: tableID}},
		)
		require.NoError(t, err)

		// Test case: Delete a row with email 'user@example.com' and insert a new row with the same email
		// This creates a dependency: delete must happen before insert
		rows := []ldrdecoder.DecodedRow{
			// Insert with email 'user@example.com'
			{
				TableID:  tableID,
				Row:      tree.Datums{tree.NewDInt(2), tree.NewDString("user@example.com")},
				PrevRow:  tree.Datums{tree.DNull, tree.DNull},
				IsDelete: false,
			},
			// Delete with email 'user@example.com'
			// For deletes, Row contains PK values with NULLs for non-PK columns
			{
				TableID:  tableID,
				Row:      tree.Datums{tree.NewDInt(1), tree.DNull},
				PrevRow:  tree.Datums{tree.NewDInt(1), tree.NewDString("user@example.com")},
				IsDelete: true,
			},
		}

		// Derive locks
		lockSet, err := ls.DeriveLocks(rows)
		require.NoError(t, err)

		// Verify we got locks for both primary keys and the unique constraint
		require.Greater(t, len(lockSet.Locks), 2, "Expected locks for primary keys and unique email")

		// Verify the sorted order: delete should come before insert
		require.Len(t, lockSet.SortedRows, 2)
		require.True(t, lockSet.SortedRows[0].IsDelete, "Delete should be sorted first")
		require.False(t, lockSet.SortedRows[1].IsDelete, "Insert should be sorted second")
	})

	t.Run("update_cycle_detection", func(t *testing.T) {
		// Create a table with a unique constraint
		runner.Exec(t, `
			CREATE TABLE accounts (
				id INT PRIMARY KEY,
				username STRING UNIQUE
			)
		`)

		// Get table descriptor
		tableDesc := desctestutils.TestingGetTableDescriptor(kvDB, codec, "defaultdb", "public", "accounts")
		tableID := tableDesc.GetID()

		// Create lock synthesizer
		ls, err := NewLockSynthesizer(
			ctx,
			s.LeaseManager().(*lease.Manager),
			s.Clock(),
			[]ldrdecoder.TableMapping{{DestID: tableID}},
		)
		require.NoError(t, err)

		// Test case: Two updates that create a cycle
		// Update 1: username 'alice' -> 'bob'
		// Update 2: username 'bob' -> 'alice'
		// This creates a cycle that should be detected
		rows := []ldrdecoder.DecodedRow{
			{
				TableID:  tableID,
				Row:      tree.Datums{tree.NewDInt(1), tree.NewDString("bob")},
				PrevRow:  tree.Datums{tree.NewDInt(1), tree.NewDString("alice")},
				IsDelete: false,
			},
			{
				TableID:  tableID,
				Row:      tree.Datums{tree.NewDInt(2), tree.NewDString("alice")},
				PrevRow:  tree.Datums{tree.NewDInt(2), tree.NewDString("bob")},
				IsDelete: false,
			},
		}

		// Derive locks - this should detect a cycle
		_, err = ls.DeriveLocks(rows)
		require.ErrorIs(t, err, ApplyCycle, "Expected cycle detection error")
	})
}
