// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package fktxn

import (
	"fmt"
	"sort"
	"strings"
)

// Column represents a column in a table.
type Column struct {
	Name     string
	Nullable bool
}

// UniqueConstraint represents a UNIQUE or PRIMARY KEY constraint.
type UniqueConstraint struct {
	Name      string
	Columns   []string
	IsPrimary bool
}

// FKEdge represents a foreign key constraint from a child table to a parent
// table. ReferencingTable/Columns are the child side; ReferencedTable/Columns
// are the parent side.
type FKEdge struct {
	Name                 string
	ReferencingTable     string
	ReferencingColumns   []string
	ReferencedTable      string
	ReferencedColumns    []string
	ReferencedConstraint string
	Nullable             bool
}

// Table represents a database table with its columns, unique constraints, and
// FK edges in both directions.
type Table struct {
	Name              string
	Columns           []Column
	UniqueConstraints []UniqueConstraint
	OutboundFKs       []FKEdge
	InboundFKs        []FKEdge
}

// Schema holds all tables keyed by name.
type Schema struct {
	Tables map[string]*Table
}

// NewSchema creates an empty Schema.
func NewSchema() *Schema {
	return &Schema{Tables: make(map[string]*Table)}
}

// AddTable adds a table to the schema.
func (s *Schema) AddTable(t *Table) {
	s.Tables[t.Name] = t
}

// String returns a deterministic text representation of the schema, sorted by
// table name. Useful for datadriven test output.
func (s *Schema) String() string {
	names := make([]string, 0, len(s.Tables))
	for name := range s.Tables {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte('\n')
		}
		t := s.Tables[name]
		fmt.Fprintf(&b, "table %s\n", t.Name)

		// Columns.
		colStrs := make([]string, len(t.Columns))
		for j, c := range t.Columns {
			if c.Nullable {
				colStrs[j] = c.Name
			} else {
				colStrs[j] = c.Name + " (not null)"
			}
		}
		fmt.Fprintf(&b, "  columns: [%s]\n", strings.Join(colStrs, ", "))

		// Unique constraints, sorted by name.
		ucs := make([]UniqueConstraint, len(t.UniqueConstraints))
		copy(ucs, t.UniqueConstraints)
		sort.Slice(ucs, func(a, b int) bool { return ucs[a].Name < ucs[b].Name })
		for _, uc := range ucs {
			kind := "unique"
			if uc.IsPrimary {
				kind = "primary"
			}
			fmt.Fprintf(&b, "  %s: %s [%s]\n", kind, uc.Name, strings.Join(uc.Columns, ", "))
		}

		// Outbound FKs, sorted by name.
		outFKs := make([]FKEdge, len(t.OutboundFKs))
		copy(outFKs, t.OutboundFKs)
		sort.Slice(outFKs, func(a, b int) bool { return outFKs[a].Name < outFKs[b].Name })
		for _, fk := range outFKs {
			nullable := ""
			if fk.Nullable {
				nullable = " (nullable)"
			}
			fmt.Fprintf(&b, "  fk: %s %s(%s) -> %s(%s)%s\n",
				fk.Name,
				fk.ReferencingTable, strings.Join(fk.ReferencingColumns, ", "),
				fk.ReferencedTable, strings.Join(fk.ReferencedColumns, ", "),
				nullable,
			)
		}

		// Inbound FKs, sorted by name.
		inFKs := make([]FKEdge, len(t.InboundFKs))
		copy(inFKs, t.InboundFKs)
		sort.Slice(inFKs, func(a, b int) bool { return inFKs[a].Name < inFKs[b].Name })
		for _, fk := range inFKs {
			fmt.Fprintf(&b, "  referenced by: %s %s(%s)\n",
				fk.Name,
				fk.ReferencingTable, strings.Join(fk.ReferencingColumns, ", "),
			)
		}
	}
	return b.String()
}

// Finalize cross-links InboundFKs on each parent table from the OutboundFKs
// on child tables. Call after all tables and FKs have been added.
func (s *Schema) Finalize() {
	for _, t := range s.Tables {
		t.InboundFKs = nil
	}
	for _, t := range s.Tables {
		for _, fk := range t.OutboundFKs {
			parent, ok := s.Tables[fk.ReferencedTable]
			if !ok {
				continue
			}
			parent.InboundFKs = append(parent.InboundFKs, fk)
		}
	}
}
