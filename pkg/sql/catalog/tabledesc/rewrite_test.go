// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tabledesc_test

import (
	"testing"

	"github.com/cockroachdb/cockroach/pkg/sql/catalog"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/descpb"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/tabledesc"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/catid"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

// makeTestRewriter builds a DescriptorRewriteFn from a simple old->new ID map.
// IDs not in the map cause an error.
func makeTestRewriter(
	idMap map[descpb.ID]descpb.ID,
) catalog.DescriptorRewriteFn {
	return func(id descpb.ID) (descpb.ID, error) {
		newID, ok := idMap[id]
		if !ok {
			return 0, errors.Newf("descriptor %d not found in rewrite map", id)
		}
		return newID, nil
	}
}

func TestRewriteStructuralIDs(t *testing.T) {
	defer leaktest.AfterTest(t)()

	const (
		oldTableID    descpb.ID = 100
		oldParentID   descpb.ID = 50
		oldSchemaID   descpb.ID = 51
		oldRefTableID descpb.ID = 101
		oldSeqID      descpb.ID = 102
		oldFnID       descpb.ID = 103
		oldTypeID     descpb.ID = 104
		oldArrTypeID  descpb.ID = 105

		newTableID    descpb.ID = 200
		newParentID   descpb.ID = 150
		newSchemaID   descpb.ID = 151
		newRefTableID descpb.ID = 201
		newSeqID      descpb.ID = 202
		newFnID       descpb.ID = 203
		newTypeID     descpb.ID = 204
		newArrTypeID  descpb.ID = 205
	)

	idMap := map[descpb.ID]descpb.ID{
		oldTableID: newTableID, oldParentID: newParentID, oldSchemaID: newSchemaID,
		oldRefTableID: newRefTableID, oldSeqID: newSeqID, oldFnID: newFnID,
		oldTypeID: newTypeID, oldArrTypeID: newArrTypeID,
	}

	desc := tabledesc.NewBuilder(&descpb.TableDescriptor{
		ID:                       oldTableID,
		Name:                     "test_table",
		ParentID:                 oldParentID,
		UnexposedParentSchemaID:  oldSchemaID,
		Version:                  5,
		OutboundFKs: []descpb.ForeignKeyConstraint{
			{
				Name:              "fk_out",
				OriginTableID:     oldTableID,
				ReferencedTableID: oldRefTableID,
				ConstraintID:      1,
			},
		},
		InboundFKs: []descpb.ForeignKeyConstraint{
			{
				Name:              "fk_in",
				OriginTableID:     oldRefTableID,
				ReferencedTableID: oldTableID,
				ConstraintID:      2,
			},
		},
		DependsOn:          []descpb.ID{oldRefTableID},
		DependsOnTypes:     []descpb.ID{oldTypeID},
		DependsOnFunctions: []descpb.ID{oldFnID},
		DependedOnBy: []descpb.TableDescriptor_Reference{
			{ID: oldRefTableID},
		},
		Columns: []descpb.ColumnDescriptor{
			{
				ID:              1,
				Name:            "col_a",
				Type:            types.MakeEnum(catid.TypeIDToOID(oldTypeID), catid.TypeIDToOID(oldArrTypeID)),
				UsesSequenceIds: []descpb.ID{oldSeqID},
				OwnsSequenceIds: []descpb.ID{oldSeqID},
				UsesFunctionIds: []descpb.ID{oldFnID},
			},
		},
		UniqueWithoutIndexConstraints: []descpb.UniqueWithoutIndexConstraint{
			{TableID: oldTableID, ConstraintID: 3},
		},
		Triggers: []descpb.TriggerDescriptor{
			{
				ID:               1,
				Name:             "trg1",
				FuncID:           oldFnID,
				FuncBody:         "BEGIN END;",
				DependsOn:        []descpb.ID{oldRefTableID},
				DependsOnTypes:   []descpb.ID{oldTypeID},
				DependsOnRoutines: []descpb.ID{oldFnID},
			},
		},
		Policies: []descpb.PolicyDescriptor{
			{
				ID:                 1,
				Name:               "pol1",
				DependsOnFunctions: []descpb.ID{oldFnID},
				DependsOnTypes:     []descpb.ID{oldTypeID},
				DependsOnRelations: []descpb.ID{oldRefTableID},
			},
		},
	}).BuildCreatedMutableTable()

	err := desc.Rewrite(makeTestRewriter(idMap))
	require.NoError(t, err)

	// Self identity.
	require.Equal(t, newTableID, desc.ID)
	require.Equal(t, newParentID, desc.ParentID)
	require.Equal(t, newSchemaID, desc.UnexposedParentSchemaID)
	require.Equal(t, descpb.DescriptorVersion(1), desc.Version)

	// Outbound FK.
	require.Len(t, desc.OutboundFKs, 1)
	require.Equal(t, newTableID, desc.OutboundFKs[0].OriginTableID)
	require.Equal(t, newRefTableID, desc.OutboundFKs[0].ReferencedTableID)

	// Inbound FK.
	require.Len(t, desc.InboundFKs, 1)
	require.Equal(t, newRefTableID, desc.InboundFKs[0].OriginTableID)
	require.Equal(t, newTableID, desc.InboundFKs[0].ReferencedTableID)

	// Dependencies.
	require.Equal(t, []descpb.ID{newRefTableID}, desc.DependsOn)
	require.Equal(t, []descpb.ID{newTypeID}, desc.DependsOnTypes)
	require.Equal(t, []descpb.ID{newFnID}, desc.DependsOnFunctions)
	require.Len(t, desc.DependedOnBy, 1)
	require.Equal(t, newRefTableID, desc.DependedOnBy[0].ID)

	// Column types.T OID rewrite.
	colType := desc.Columns[0].Type
	require.Equal(t, catid.TypeIDToOID(newTypeID), colType.Oid())

	// Column sequence and function refs.
	require.Equal(t, []descpb.ID{newSeqID}, desc.Columns[0].UsesSequenceIds)
	require.Equal(t, []descpb.ID{newSeqID}, desc.Columns[0].OwnsSequenceIds)
	require.Equal(t, []descpb.ID{newFnID}, desc.Columns[0].UsesFunctionIds)

	// Unique without index.
	require.Equal(t, newTableID, desc.UniqueWithoutIndexConstraints[0].TableID)

	// Triggers.
	require.Len(t, desc.Triggers, 1)
	require.Equal(t, newFnID, desc.Triggers[0].FuncID)
	require.Equal(t, []descpb.ID{newRefTableID}, desc.Triggers[0].DependsOn)
	require.Equal(t, []descpb.ID{newTypeID}, desc.Triggers[0].DependsOnTypes)
	require.Equal(t, []descpb.ID{newFnID}, desc.Triggers[0].DependsOnRoutines)

	// Policies.
	require.Len(t, desc.Policies, 1)
	require.Equal(t, []descpb.ID{newFnID}, desc.Policies[0].DependsOnFunctions)
	require.Equal(t, []descpb.ID{newTypeID}, desc.Policies[0].DependsOnTypes)
	require.Equal(t, []descpb.ID{newRefTableID}, desc.Policies[0].DependsOnRelations)
}

func TestRewriteErrorOnMissingID(t *testing.T) {
	defer leaktest.AfterTest(t)()

	const (
		oldTableID    descpb.ID = 100
		oldParentID   descpb.ID = 50
		oldSchemaID   descpb.ID = 51
		oldRefTableID descpb.ID = 101

		newTableID  descpb.ID = 200
		newParentID descpb.ID = 150
		newSchemaID descpb.ID = 151
	)

	// Map does NOT include oldRefTableID.
	idMap := map[descpb.ID]descpb.ID{
		oldTableID: newTableID, oldParentID: newParentID, oldSchemaID: newSchemaID,
	}

	desc := tabledesc.NewBuilder(&descpb.TableDescriptor{
		ID:                      oldTableID,
		Name:                    "test_table",
		ParentID:                oldParentID,
		UnexposedParentSchemaID: oldSchemaID,
		OutboundFKs: []descpb.ForeignKeyConstraint{
			{
				Name:              "fk_missing",
				OriginTableID:     oldTableID,
				ReferencedTableID: oldRefTableID,
				ConstraintID:      1,
			},
		},
		Columns: []descpb.ColumnDescriptor{
			{ID: 1, Name: "id", Type: types.Int},
		},
	}).BuildCreatedMutableTable()

	err := desc.Rewrite(makeTestRewriter(idMap))
	require.Error(t, err)
	require.Contains(t, err.Error(), "101")
}

func TestRewriteSequenceOwnership(t *testing.T) {
	defer leaktest.AfterTest(t)()

	const (
		oldSeqID    descpb.ID = 100
		oldParentID descpb.ID = 50
		oldSchemaID descpb.ID = 51
		oldOwnerID  descpb.ID = 102

		newSeqID    descpb.ID = 200
		newParentID descpb.ID = 150
		newSchemaID descpb.ID = 151
		newOwnerID  descpb.ID = 202
	)

	idMap := map[descpb.ID]descpb.ID{
		oldSeqID: newSeqID, oldParentID: newParentID, oldSchemaID: newSchemaID,
		oldOwnerID: newOwnerID,
	}

	desc := tabledesc.NewBuilder(&descpb.TableDescriptor{
		ID:                      oldSeqID,
		Name:                    "test_seq",
		ParentID:                oldParentID,
		UnexposedParentSchemaID: oldSchemaID,
		SequenceOpts: &descpb.TableDescriptor_SequenceOpts{
			SequenceOwner: descpb.TableDescriptor_SequenceOpts_SequenceOwner{
				OwnerTableID: oldOwnerID,
			},
		},
		Columns: []descpb.ColumnDescriptor{
			{ID: 1, Name: "value", Type: types.Int},
		},
	}).BuildCreatedMutableTable()

	err := desc.Rewrite(makeTestRewriter(idMap))
	require.NoError(t, err)
	require.Equal(t, newOwnerID, desc.SequenceOpts.SequenceOwner.OwnerTableID)
}

func TestRewriteMultiTableFKs(t *testing.T) {
	defer leaktest.AfterTest(t)()

	// Simulates the LDR use case: two tables with FK references between them,
	// both being rewritten together.
	const (
		oldParentID descpb.ID = 50
		oldSchemaID descpb.ID = 51
		oldParentT  descpb.ID = 100
		oldChildT   descpb.ID = 101

		newParentID descpb.ID = 150
		newSchemaID descpb.ID = 151
		newParentT  descpb.ID = 200
		newChildT   descpb.ID = 201
	)

	idMap := map[descpb.ID]descpb.ID{
		oldParentID: newParentID, oldSchemaID: newSchemaID,
		oldParentT: newParentT, oldChildT: newChildT,
	}
	rewriter := makeTestRewriter(idMap)

	parent := tabledesc.NewBuilder(&descpb.TableDescriptor{
		ID:                      oldParentT,
		Name:                    "parent",
		ParentID:                oldParentID,
		UnexposedParentSchemaID: oldSchemaID,
		InboundFKs: []descpb.ForeignKeyConstraint{
			{
				Name:              "fk_child_parent",
				OriginTableID:     oldChildT,
				ReferencedTableID: oldParentT,
				ConstraintID:      1,
			},
		},
		Columns: []descpb.ColumnDescriptor{
			{ID: 1, Name: "pid", Type: types.Int},
		},
	}).BuildCreatedMutableTable()

	child := tabledesc.NewBuilder(&descpb.TableDescriptor{
		ID:                      oldChildT,
		Name:                    "child",
		ParentID:                oldParentID,
		UnexposedParentSchemaID: oldSchemaID,
		OutboundFKs: []descpb.ForeignKeyConstraint{
			{
				Name:              "fk_child_parent",
				OriginTableID:     oldChildT,
				ReferencedTableID: oldParentT,
				ConstraintID:      1,
			},
		},
		Columns: []descpb.ColumnDescriptor{
			{ID: 1, Name: "id", Type: types.Int},
			{ID: 2, Name: "pid", Type: types.Int},
		},
	}).BuildCreatedMutableTable()

	require.NoError(t, parent.Rewrite(rewriter))
	require.NoError(t, child.Rewrite(rewriter))

	// Parent's inbound FK should point to new child and new parent.
	require.Equal(t, newChildT, parent.InboundFKs[0].OriginTableID)
	require.Equal(t, newParentT, parent.InboundFKs[0].ReferencedTableID)

	// Child's outbound FK should point to new child and new parent.
	require.Equal(t, newChildT, child.OutboundFKs[0].OriginTableID)
	require.Equal(t, newParentT, child.OutboundFKs[0].ReferencedTableID)
}
