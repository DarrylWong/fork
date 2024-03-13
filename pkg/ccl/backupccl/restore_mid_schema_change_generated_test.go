// Copyright 2023 The Cockroach Authors.
//
// Licensed as a CockroachDB Enterprise file under the Cockroach Community
// License (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//     https://github.com/cockroachdb/cockroach/blob/master/licenses/CCL.txt

// Code generated by pkg/ccl/backupccl/testgen, DO NOT EDIT.

package backupccl

import (
	"testing"

	"github.com/cockroachdb/cockroach/pkg/testutils/skip"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
)

func TestRestoreMidSchemaChange_schemaOnly_true_clusterRestore_true(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderRaceWithIssue(t, 56584)

	runTestRestoreMidSchemaChange(t, true, true)
}

func TestRestoreMidSchemaChange_schemaOnly_true_clusterRestore_false(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderRaceWithIssue(t, 56584)

	runTestRestoreMidSchemaChange(t, true, false)
}

func TestRestoreMidSchemaChange_schemaOnly_false_clusterRestore_true(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderRaceWithIssue(t, 56584)

	runTestRestoreMidSchemaChange(t, false, true)
}

func TestRestoreMidSchemaChange_schemaOnly_false_clusterRestore_false(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)
	skip.UnderRaceWithIssue(t, 56584)

	runTestRestoreMidSchemaChange(t, false, false)
}
