// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package descutil

import (
	"go/constant"
	"strconv"

	"github.com/cockroachdb/cockroach/pkg/sql/catalog"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/catpb"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/descpb"
	"github.com/cockroachdb/cockroach/pkg/sql/parserutils"
	"github.com/cockroachdb/cockroach/pkg/sql/schemachanger/scpb"
	"github.com/cockroachdb/cockroach/pkg/sql/schemachanger/screl"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/catid"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/errors"
	"github.com/lib/pq/oid"
)

// RewriteIDsInTypesT rewrites user-defined type OIDs in a types.T value
// using the provided rewriter. Non-user-defined types are left unchanged.
func RewriteIDsInTypesT(typ *types.T, rewriter catalog.DescriptorRewriteFn) {
	if !typ.UserDefined() {
		return
	}
	tid := catid.UserDefinedOIDToID(typ.Oid())
	var newOID, newArrayOID oid.Oid
	if newID, err := rewriter(tid); err == nil {
		newOID = catid.TypeIDToOID(newID)
	}
	if typ.Family() != types.ArrayFamily {
		arrayOid := typ.UserDefinedArrayOID()
		if arrayOid != 0 {
			arrayTid := catid.UserDefinedOIDToID(arrayOid)
			if newID, err := rewriter(arrayTid); err == nil {
				newArrayOID = catid.TypeIDToOID(newID)
			}
		}
	}
	types.RemapUserDefinedTypeOIDs(typ, newOID, newArrayOID)
	if typ.Family() == types.ArrayFamily {
		RewriteIDsInTypesT(typ.ArrayContents(), rewriter)
	}
}

// makeTypeReplaceFmtCtx returns a FmtCtx that rewrites user-defined type OID
// references in formatted SQL output.
func makeTypeReplaceFmtCtx(rewriter catalog.DescriptorRewriteFn) *tree.FmtCtx {
	return tree.NewFmtCtx(
		tree.FmtSerializable,
		tree.FmtIndexedTypeFormat(func(ctx *tree.FmtCtx, ref *tree.OIDTypeReference) {
			id := catid.UserDefinedOIDToID(ref.OID)
			if descpb.IsVirtualTable(id) {
				id = descpb.ID(ref.OID)
			}
			newRef := ref
			if newID, err := rewriter(id); err == nil {
				newRef = &tree.OIDTypeReference{OID: catid.TypeIDToOID(newID)}
			}
			ctx.WriteString(newRef.SQLString())
		}),
	)
}

// makeSeqReplaceFunc returns a visitor function that rewrites sequence ID
// literals in SQL expressions.
func makeSeqReplaceFunc(
	rewriter catalog.DescriptorRewriteFn,
) func(expr tree.Expr) (bool, tree.Expr, error) {
	return func(expr tree.Expr) (bool, tree.Expr, error) {
		annotateTypeExpr, ok := expr.(*tree.AnnotateTypeExpr)
		if !ok {
			return true, expr, nil
		}
		typ, safe := tree.GetStaticallyKnownType(annotateTypeExpr.Type)
		if !safe || typ.Family() != types.OidFamily {
			return true, expr, nil
		}
		numVal, ok := annotateTypeExpr.Expr.(*tree.NumVal)
		if !ok {
			return true, expr, nil
		}
		seqID, err := numVal.AsInt64()
		if err != nil {
			// Not a valid sequence ID literal; skip this expression.
			return true, expr, nil //nolint:returnerrcheck
		}
		newID, err := rewriter(descpb.ID(seqID))
		if err != nil {
			return false, expr, err
		}
		annotateTypeExpr.Expr = tree.NewNumVal(
			constant.MakeInt64(int64(newID)),
			strconv.Itoa(int(newID)),
			false, /* negative */
		)
		return false, annotateTypeExpr, nil
	}
}

// makeFuncReplaceFunc returns a visitor function that rewrites user-defined
// function OID references in SQL expressions.
func makeFuncReplaceFunc(
	rewriter catalog.DescriptorRewriteFn,
) func(expr tree.Expr) (bool, tree.Expr, error) {
	return func(expr tree.Expr) (bool, tree.Expr, error) {
		funcExpr, ok := expr.(*tree.FuncExpr)
		if !ok {
			return true, expr, nil
		}
		oidRef, ok := funcExpr.Func.FunctionReference.(*tree.FunctionOID)
		if !ok {
			return true, expr, nil
		}
		if !catid.IsOIDUserDefined(oidRef.OID) {
			return true, expr, nil
		}
		fnID := catid.UserDefinedOIDToID(oidRef.OID)
		newID, err := rewriter(fnID)
		if err != nil {
			return false, expr, err
		}
		newFuncExpr := *funcExpr
		newFuncExpr.Func = tree.ResolvableFunctionReference{
			FunctionReference: &tree.FunctionOID{OID: catid.FuncIDToOID(newID)},
		}
		return true, &newFuncExpr, nil
	}
}

// RewriteExprIDs rewrites type, sequence, and function OID references
// embedded in a SQL expression string.
func RewriteExprIDs(expr string, rewriter catalog.DescriptorRewriteFn) (string, error) {
	parsed, err := parserutils.ParseExpr(expr)
	if err != nil {
		return "", err
	}

	// Rewrite type OIDs.
	typCtx := makeTypeReplaceFmtCtx(rewriter)
	typCtx.FormatNode(parsed)
	expr = typCtx.CloseAndGetString()

	// Re-parse after type rewrite for sequence/function rewriting.
	parsed, err = parserutils.ParseExpr(expr)
	if err != nil {
		return "", err
	}

	// Rewrite sequence IDs.
	newExpr, err := tree.SimpleVisit(parsed, makeSeqReplaceFunc(rewriter))
	if err != nil {
		return "", err
	}

	// Rewrite function OIDs.
	newExpr, err = tree.SimpleVisit(newExpr, makeFuncReplaceFunc(rewriter))
	if err != nil {
		return "", err
	}

	return newExpr.String(), nil
}

// RewriteViewQueryIDs rewrites type and sequence OID references in a
// view query string.
func RewriteViewQueryIDs(query string, rewriter catalog.DescriptorRewriteFn) (string, error) {
	stmt, err := parserutils.ParseOne(query)
	if err != nil {
		return "", err
	}

	// Rewrite type OIDs.
	typCtx := makeTypeReplaceFmtCtx(rewriter)
	typCtx.FormatNode(stmt.AST)
	query = typCtx.CloseAndGetString()

	// Re-parse for sequence rewriting.
	stmt, err = parserutils.ParseOne(query)
	if err != nil {
		return "", err
	}
	newStmt, err := tree.SimpleStmtVisit(stmt.AST, makeSeqReplaceFunc(rewriter))
	if err != nil {
		return "", err
	}
	return newStmt.String(), nil
}

// RewritePLpgSQLBodyIDs rewrites type OID references in a PL/pgSQL
// function body string.
func RewritePLpgSQLBodyIDs(body string, rewriter catalog.DescriptorRewriteFn) (string, error) {
	stmt, err := parserutils.PLpgSQLParse(body)
	if err != nil {
		return "", err
	}
	typCtx := makeTypeReplaceFmtCtx(rewriter)
	typCtx.FormatNode(stmt.AST)
	return typCtx.CloseAndGetString(), nil
}

// RewriteSchemaChangerState rewrites all descriptor IDs, type OIDs, and
// expression references in declarative schema changer state. This is the
// mechanical remapping only — it does not drop elements with missing
// dependencies (that is restore-specific pre-processing).
func RewriteSchemaChangerState(
	state *scpb.DescriptorState, rewriter catalog.DescriptorRewriteFn,
) error {
	for i := range state.Targets {
		t := &state.Targets[i]

		// Rewrite special-case elements that carry parent info.
		if data := t.GetTableData(); data != nil {
			var err error
			if data.TableID, err = rewriter(data.TableID); err != nil {
				return errors.Wrapf(err, "rewriting schema changer state element %s",
					screl.ElementString(t.Element()))
			}
			if data.DatabaseID, err = rewriter(data.DatabaseID); err != nil {
				return errors.Wrapf(err, "rewriting schema changer state element %s",
					screl.ElementString(t.Element()))
			}
			continue
		}
		if data := t.GetNamespace(); data != nil {
			var err error
			if data.DescriptorID, err = rewriter(data.DescriptorID); err != nil {
				return errors.Wrapf(err, "rewriting schema changer state element %s",
					screl.ElementString(t.Element()))
			}
			if data.DatabaseID, err = rewriter(data.DatabaseID); err != nil {
				return errors.Wrapf(err, "rewriting schema changer state element %s",
					screl.ElementString(t.Element()))
			}
			if data.SchemaID, err = rewriter(data.SchemaID); err != nil {
				return errors.Wrapf(err, "rewriting schema changer state element %s",
					screl.ElementString(t.Element()))
			}
			continue
		}

		// Walk and rewrite all descriptor IDs in the element.
		if err := screl.WalkDescIDs(t.Element(), func(id *descpb.ID) error {
			if *id == descpb.InvalidID {
				return nil
			}
			newID, err := rewriter(*id)
			if err != nil {
				return err
			}
			*id = newID
			return nil
		}); err != nil {
			return errors.Wrapf(err,
				"rewriting descriptor IDs in schema changer state element %s",
				screl.ElementString(t.Element()))
		}

		// Walk and rewrite expressions.
		if err := screl.WalkExpressions(t.Element(), func(expr *catpb.Expression) error {
			if *expr == "" {
				return nil
			}
			newExpr, err := RewriteExprIDs(string(*expr), rewriter)
			if err != nil {
				return err
			}
			*expr = catpb.Expression(newExpr)
			return nil
		}); err != nil {
			return errors.Wrap(err, "rewriting expressions in schema changer state")
		}

		// Walk and rewrite type OIDs.
		if err := screl.WalkTypes(t.Element(), func(t *types.T) error {
			RewriteIDsInTypesT(t, rewriter)
			return nil
		}); err != nil {
			return errors.Wrap(err, "rewriting type OIDs in schema changer state")
		}
	}
	return nil
}
