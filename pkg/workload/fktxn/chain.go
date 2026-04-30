// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"math/rand"

	"github.com/cockroachdb/cockroach/pkg/util/fsm"
	"github.com/cockroachdb/errors"
)

// chainStateGone means the row chain identified by the chain's PK assignment
// does not exist in the database. The only valid next event is eventUpsert.
type chainStateGone struct{}

// chainStateExists means the row chain has been written and not deleted. Any
// of eventUpsert, eventUpdate, or eventDelete is valid.
type chainStateExists struct{}

func (chainStateGone) State()   {}
func (chainStateExists) State() {}

// eventUpsert writes (or rewrites) the entire row chain in topological order.
// Allowed from both Gone (creates the chain) and Exists (overwrites,
// re-pointing FK columns at the parent values picked for this chain).
type eventUpsert struct{}

// eventUpdate re-points FK columns on one row in the chain to the parent
// values from the chain's PK assignment. Allowed only from Exists.
type eventUpdate struct{}

// eventDelete removes the entire row chain (children before parents). Allowed
// only from Exists.
type eventDelete struct{}

func (eventUpsert) Event() {}
func (eventUpdate) Event() {}
func (eventDelete) Event() {}

// chainExtended is the per-chain state passed through fsm.Args.Extended to
// each action function. The sub-DAG, dropped edges, PK assignment, and RNG
// are stable for the lifetime of the chain. The transaction handle is
// rewritten by the worker before every event, so the FSM action always sees
// the current event's tx via Extended.tx. Typed as dbTx so a recording
// wrapper can be substituted by tests; production sets it to a *sql.Tx.
type chainExtended struct {
	tx      dbTx
	rng     *rand.Rand
	sorted  []*Table
	sub     *FKGraph
	dropped []FKEdge
	pks     PKAssignment
}

// chainTransitions defines the valid (state, event) → (next state, action)
// table for the per-chain row lifecycle. Each chain follows the lifecycle
// shape `Upsert → (Update*) → Delete → Upsert → ...`: Upsert is only valid
// when the chain doesn't exist yet, Update and Delete only when it does.
// Disallowing Upsert from Exists prevents redundant row rewrites and forces
// every chain to go through the full insert→modify→delete lifecycle. Failed
// actions return an error and leave the state unchanged so the caller can
// break the chain cleanly.
var chainTransitions = fsm.Compile(fsm.Pattern{
	chainStateGone{}: {
		eventUpsert{}: {
			Next:        chainStateExists{},
			Action:      runUpsert,
			Description: "insert chain",
		},
	},
	chainStateExists{}: {
		eventUpdate{}: {
			Next:        chainStateExists{},
			Action:      runUpdate,
			Description: "re-point one FK row",
		},
		eventDelete{}: {
			Next:        chainStateGone{},
			Action:      runDelete,
			Description: "delete chain",
		},
	},
})

func runUpsert(a fsm.Args) error {
	c, ok := a.Extended.(*chainExtended)
	if !ok {
		return errors.AssertionFailedf("chain action: bad extended state %T", a.Extended)
	}
	_, err := ExecuteUpsert(a.Ctx, c.tx, c.rng, c.sorted, c.sub, c.dropped, c.pks)
	return err
}

func runUpdate(a fsm.Args) error {
	c, ok := a.Extended.(*chainExtended)
	if !ok {
		return errors.AssertionFailedf("chain action: bad extended state %T", a.Extended)
	}
	_, err := ExecuteUpdate(a.Ctx, c.tx, c.rng, c.sorted, c.sub, c.pks)
	return err
}

func runDelete(a fsm.Args) error {
	c, ok := a.Extended.(*chainExtended)
	if !ok {
		return errors.AssertionFailedf("chain action: bad extended state %T", a.Extended)
	}
	_, err := ExecuteDelete(a.Ctx, c.tx, c.sorted, c.pks)
	return err
}

// OpMix weights the choice between Update and Delete when the chain is in
// the Exists state. Upsert is not weighted because it is forced from Gone
// (the only valid event there). Higher Delete weight ends chains sooner and
// triggers the next chain's Upsert; higher Update weight keeps the chain
// alive longer, producing more re-points in a row.
type OpMix struct {
	Update int
	Delete int
}

// pickEvent selects the next event to apply given the current state. Gone
// always advances via Upsert (no choice). Exists samples Update vs Delete by
// weight.
func (m OpMix) pickEvent(rng *rand.Rand, state fsm.State) fsm.Event {
	if _, gone := state.(chainStateGone); gone {
		return eventUpsert{}
	}
	total := m.Update + m.Delete
	if total <= 0 {
		// Defensive: caller passed an empty mix. Default to Delete so the
		// chain advances back to Gone rather than being stuck in Exists.
		return eventDelete{}
	}
	if rng.Intn(total) < m.Update {
		return eventUpdate{}
	}
	return eventDelete{}
}
