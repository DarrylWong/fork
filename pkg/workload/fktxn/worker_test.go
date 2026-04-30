// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"context"
	"fmt"
	"math/rand"
	randv2 "math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/sqlutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/randutil"
	"github.com/stretchr/testify/require"
)

// TestWorker_SingleChain verifies a single worker can drive a chain through
// the FSM, executing UPSERT/UPDATE/DELETE in the right order. With one
// worker and tolerate=true, no source errors should occur.
func TestWorker_SingleChain(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	for _, tc := range txnSchemaCases {
		t.Run(tc.name, func(t *testing.T) {
			rng, seed := randutil.NewTestRand()
			t.Logf("seed=%d", seed)

			dbName := "wrk_single_" + tc.name
			discoverSchemaFromDDL(t, srv, sqlDB, dbName, tc.ddl)
			setup := newShardSetup(t, srv, dbName, rng, 50)

			testDB := srv.ApplicationLayer().SQLConn(t, serverutils.DBName(dbName))
			worker := NewWorker(WorkerConfig{
				DB:                testDB,
				Sorted:            setup.sorted,
				Sub:               setup.sub,
				Dropped:           setup.dropped,
				Pool:              setup.pool,
				Mix:               OpMix{Update: 70, Delete: 30},
				MinChainLen:       3,
				MaxChainLen:       6,
				TolerateSrcErrors: false,
			}, rand.New(rand.NewSource(rng.Int63())))

			res, err := worker.Run(ctx)
			require.NoError(t, err, "single worker should not see src errors")
			require.GreaterOrEqual(t, res.Committed, 3, "expected at least MinChainLen events to commit")
			require.Equal(t, res.Attempted, res.Committed, "no attempts should fail without contention")
		})
	}
}

// TestWorker_ConcurrentContention runs many workers on the same shard
// configuration and asserts that source errors are tolerated and the
// workload makes forward progress.
func TestWorker_ConcurrentContention(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	srv, db, _ := serverutils.StartServer(t, base.TestServerArgs{})
	defer srv.Stopper().Stop(ctx)
	sqlDB := sqlutils.MakeSQLRunner(db)

	rng, seed := randutil.NewTestRand()
	t.Logf("seed=%d", seed)

	const dbName = "wrk_concurrent"
	discoverSchemaFromDDL(t, srv, sqlDB, dbName, chainDDL)

	// Build a single shared sub-DAG and pool. Workers contend on the same
	// rows by sampling from the same small pool.
	testDB := srv.ApplicationLayer().SQLConn(t, serverutils.DBName(dbName))
	s, err := DiscoverSchema(testDB, dbName)
	require.NoError(t, err)
	graphs := BuildFKGraphs(s)
	require.NotEmpty(t, graphs)
	rngV2 := randv2.New(randv2.NewPCG(rng.Uint64(), rng.Uint64()))
	sorted, sub, dropped, err := RandomSubDAG(rngV2, graphs[0])
	require.NoError(t, err)
	pool, err := BuildPKPool(rng, sorted, 5)
	require.NoError(t, err)

	const numWorkers = 4
	const chainsPerWorker = 5

	var totalAttempted, totalCommitted int64
	var mu sync.Mutex
	failByClass := map[string]int{}
	failByEvent := map[string]int{}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		workerSeed := rng.Int63()
		go func() {
			defer wg.Done()
			workerDB := srv.ApplicationLayer().SQLConn(t, serverutils.DBName(dbName))
			w := NewWorker(WorkerConfig{
				DB:                workerDB,
				Sorted:            sorted,
				Sub:               sub,
				Dropped:           dropped,
				Pool:              pool,
				Mix:               OpMix{Update: 70, Delete: 30},
				MinChainLen:       2,
				MaxChainLen:       5,
				TolerateSrcErrors: true,
			}, rand.New(rand.NewSource(workerSeed)))
			for c := 0; c < chainsPerWorker; c++ {
				res, err := w.Run(ctx)
				if err != nil {
					t.Errorf("worker run: %v", err)
					return
				}
				atomic.AddInt64(&totalAttempted, int64(res.Attempted))
				atomic.AddInt64(&totalCommitted, int64(res.Committed))
				if res.FailErr != nil {
					mu.Lock()
					failByClass[res.FailureClass()]++
					failByEvent[fmt.Sprintf("%T", res.FailedEvent)]++
					mu.Unlock()
					t.Logf("chain failed on %T: %v", res.FailedEvent, res.FailErr)
				}
			}
		}()
	}
	wg.Wait()

	require.Greater(t, totalCommitted, int64(0), "expected some chains to commit")
	successRatio := float64(totalCommitted) / float64(totalAttempted)
	t.Logf("contention summary: %d/%d events committed (%.1f%% success) across %d workers x %d chains",
		totalCommitted, totalAttempted, 100*successRatio, numWorkers, chainsPerWorker)
	if len(failByClass) > 0 {
		t.Logf("failure breakdown by class: %v", failByClass)
		t.Logf("failure breakdown by event: %v", failByEvent)
	}
}

// TestPickEvent_GoneOnlyEmitsUpsert verifies the FSM's event-picker honors
// the state machine's restrictions. From Gone, only Upsert is valid and the
// picker must always return it regardless of the mix weights.
func TestPickEvent_GoneOnlyEmitsUpsert(t *testing.T) {
	mix := OpMix{Update: 100, Delete: 100}
	rng := rand.New(rand.NewSource(0))
	for i := 0; i < 50; i++ {
		ev := mix.pickEvent(rng, chainStateGone{})
		_, ok := ev.(eventUpsert)
		require.True(t, ok, "iteration %d: expected eventUpsert from Gone, got %T", i, ev)
	}
}

// TestPickEvent_ExistsRespectsMix verifies the picker samples Update vs
// Delete per the configured mix when in Exists. Upsert never appears from
// Exists because it's not a valid transition. Over many trials the observed
// frequencies should approximate the configured weights.
func TestPickEvent_ExistsRespectsMix(t *testing.T) {
	mix := OpMix{Update: 70, Delete: 30}
	rng := rand.New(rand.NewSource(0))
	const trials = 10000
	var updateCount, deleteCount int
	for i := 0; i < trials; i++ {
		ev := mix.pickEvent(rng, chainStateExists{})
		switch ev.(type) {
		case eventUpdate:
			updateCount++
		case eventDelete:
			deleteCount++
		default:
			t.Fatalf("unexpected event %T from Exists", ev)
		}
	}
	require.InDelta(t, 0.7, float64(updateCount)/trials, 0.05)
	require.InDelta(t, 0.3, float64(deleteCount)/trials, 0.05)
}

