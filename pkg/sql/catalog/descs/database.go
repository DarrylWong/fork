// Copyright 2021 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package descs

import (
	"context"
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/kv"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/dbdesc"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/descpb"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/sqlerrors"
	"github.com/cockroachdb/errors"
)

// GetMutableDatabaseByName returns a mutable database descriptor with
// properties according to the provided lookup flags. RequireMutable is ignored.
func (tc *Collection) GetMutableDatabaseByName(
	ctx context.Context, txn *kv.Txn, name string, flags tree.DatabaseLookupFlags,
) (*dbdesc.Mutable, error) {
	flags.RequireMutable = true
	desc, err := tc.getDatabaseByName(ctx, txn, name, flags)
	if err != nil || desc == nil {
		return nil, err
	}
	return desc.(*dbdesc.Mutable), nil
}

// GetImmutableDatabaseByName returns an immutable database descriptor with
// properties according to the provided lookup flags. RequireMutable is ignored.
func (tc *Collection) GetImmutableDatabaseByName(
	ctx context.Context, txn *kv.Txn, name string, flags tree.DatabaseLookupFlags,
) (catalog.DatabaseDescriptor, error) {
	flags.RequireMutable = false
	return tc.getDatabaseByName(ctx, txn, name, flags)
}

// getDatabaseByName returns a database descriptor with properties according to
// the provided lookup flags.
func (tc *Collection) getDatabaseByName(
	ctx context.Context, txn *kv.Txn, name string, flags tree.DatabaseLookupFlags,
) (catalog.DatabaseDescriptor, error) {
	desc, err := tc.getDescriptorByName(ctx, txn, nil /* db */, nil /* sc */, name, flags, catalog.Database)
	if err != nil {
		return nil, err
	}
	if desc == nil {
		if flags.Required {
			return nil, sqlerrors.NewUndefinedDatabaseError(name)
		}
		return nil, nil
	}
	db, ok := desc.(catalog.DatabaseDescriptor)
	if !ok {
		if flags.Required {
			return nil, sqlerrors.NewUndefinedDatabaseError(name)
		}
		return nil, nil
	}
	return db, nil
}

// GetImmutableDatabaseByID returns an immutable database descriptor with
// properties according to the provided lookup flags. RequireMutable is ignored.
func (tc *Collection) GetImmutableDatabaseByID(
	ctx context.Context, txn *kv.Txn, dbID descpb.ID, flags tree.DatabaseLookupFlags,
) (bool, catalog.DatabaseDescriptor, error) {
	flags.RequireMutable = false
	return tc.getDatabaseByID(ctx, txn, dbID, flags)
}

func (tc *Collection) getDatabaseByID(
	ctx context.Context, txn *kv.Txn, dbID descpb.ID, flags tree.DatabaseLookupFlags,
) (bool, catalog.DatabaseDescriptor, error) {
	descs, err := tc.getDescriptorsByID(ctx, txn, flags, dbID)
	if err != nil {
		if errors.Is(err, catalog.ErrDescriptorNotFound) {
			if flags.Required {
				return false, nil, sqlerrors.NewUndefinedDatabaseError(fmt.Sprintf("[%d]", dbID))
			}
			return false, nil, nil
		}
		return false, nil, err
	}
	db, ok := descs[0].(catalog.DatabaseDescriptor)
	if !ok {
		return false, nil, sqlerrors.NewUndefinedDatabaseError(fmt.Sprintf("[%d]", dbID))
	}
	return true, db, nil
}
