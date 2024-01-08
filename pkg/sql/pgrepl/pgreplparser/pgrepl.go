// Code generated by goyacc -p pgrepl -o bazel-out/k8-fastbuild/bin/pkg/sql/pgrepl/pgreplparser/pgrepl.go pgrepl-gen.y. DO NOT EDIT.

//line pgrepl-gen.y:2

/*-------------------------------------------------------------------------
 *
 * repl_gram.y        - Parser for the replication commands
 *
 * Portions Copyright (c) 1996-2023, PostgreSQL Global Development Group
 * Portions Copyright (c) 1994, Regents of the University of California
 *
 *
 * IDENTIFICATION
 *    src/backend/replication/repl_gram.y
 *
 *-------------------------------------------------------------------------
 */

package pgreplparser

import __yyfmt__ "fmt"

//line pgrepl-gen.y:17

import (
	"github.com/cockroachdb/cockroach/pkg/sql/pgrepl/lsn"
	"github.com/cockroachdb/cockroach/pkg/sql/pgrepl/pgrepltree"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgcode"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
)

//line pgrepl-gen.y:29

func (s *pgreplSymType) ID() int32 {
	return s.id
}

func (s *pgreplSymType) SetID(id int32) {
	s.id = id
}

func (s *pgreplSymType) Pos() int32 {
	return s.pos
}

func (s *pgreplSymType) SetPos(pos int32) {
	s.pos = pos
}

func (s *pgreplSymType) Str() string {
	return s.str
}

func (s *pgreplSymType) SetStr(str string) {
	s.str = str
}

func (s *pgreplSymType) UnionVal() interface{} {
	return s.union.val
}

func (s *pgreplSymType) SetUnionVal(val interface{}) {
	s.union.val = val
}

type pgreplSymUnion struct {
	val interface{}
}

func (u *pgreplSymUnion) replicationStatement() pgrepltree.ReplicationStatement {
	return u.val.(pgrepltree.ReplicationStatement)
}

func (u *pgreplSymUnion) identifySystem() *pgrepltree.IdentifySystem {
	return u.val.(*pgrepltree.IdentifySystem)
}

func (u *pgreplSymUnion) readReplicationSlot() *pgrepltree.ReadReplicationSlot {
	return u.val.(*pgrepltree.ReadReplicationSlot)
}

func (u *pgreplSymUnion) createReplicationSlot() *pgrepltree.CreateReplicationSlot {
	return u.val.(*pgrepltree.CreateReplicationSlot)
}

func (u *pgreplSymUnion) dropReplicationSlot() *pgrepltree.DropReplicationSlot {
	return u.val.(*pgrepltree.DropReplicationSlot)
}

func (u *pgreplSymUnion) startReplication() *pgrepltree.StartReplication {
	return u.val.(*pgrepltree.StartReplication)
}

func (u *pgreplSymUnion) timelineHistory() *pgrepltree.TimelineHistory {
	return u.val.(*pgrepltree.TimelineHistory)
}

func (u *pgreplSymUnion) option() pgrepltree.Option {
	return u.val.(pgrepltree.Option)
}

func (u *pgreplSymUnion) options() pgrepltree.Options {
	return u.val.(pgrepltree.Options)
}

func (u *pgreplSymUnion) numVal() *tree.NumVal {
	return u.val.(*tree.NumVal)
}

func (u *pgreplSymUnion) expr() tree.Expr {
	return u.val.(tree.Expr)
}

func (u *pgreplSymUnion) bool() bool {
	return u.val.(bool)
}

func (u *pgreplSymUnion) lsn() lsn.LSN {
	return u.val.(lsn.LSN)
}

//line pgrepl-gen.y:119
type pgreplSymType struct {
	yys   int
	id    int32
	pos   int32
	str   string
	union pgreplSymUnion
}

const SCONST = 57346
const IDENT = 57347
const UCONST = 57348
const RECPTR = 57349
const ERROR = 57350
const K_BASE_BACKUP = 57351
const K_IDENTIFY_SYSTEM = 57352
const K_READ_REPLICATION_SLOT = 57353
const K_START_REPLICATION = 57354
const K_CREATE_REPLICATION_SLOT = 57355
const K_DROP_REPLICATION_SLOT = 57356
const K_TIMELINE_HISTORY = 57357
const K_WAIT = 57358
const K_TIMELINE = 57359
const K_PHYSICAL = 57360
const K_LOGICAL = 57361
const K_SLOT = 57362
const K_RESERVE_WAL = 57363
const K_TEMPORARY = 57364
const K_TWO_PHASE = 57365
const K_EXPORT_SNAPSHOT = 57366
const K_NOEXPORT_SNAPSHOT = 57367
const K_USE_SNAPSHOT = 57368

var pgreplToknames = [...]string{
	"$end",
	"error",
	"$unk",
	"SCONST",
	"IDENT",
	"UCONST",
	"RECPTR",
	"ERROR",
	"K_BASE_BACKUP",
	"K_IDENTIFY_SYSTEM",
	"K_READ_REPLICATION_SLOT",
	"K_START_REPLICATION",
	"K_CREATE_REPLICATION_SLOT",
	"K_DROP_REPLICATION_SLOT",
	"K_TIMELINE_HISTORY",
	"K_WAIT",
	"K_TIMELINE",
	"K_PHYSICAL",
	"K_LOGICAL",
	"K_SLOT",
	"K_RESERVE_WAL",
	"K_TEMPORARY",
	"K_TWO_PHASE",
	"K_EXPORT_SNAPSHOT",
	"K_NOEXPORT_SNAPSHOT",
	"K_USE_SNAPSHOT",
	"';'",
	"'('",
	"')'",
	"','",
}

var pgreplStatenames = [...]string{}

const pgreplEofCode = 1
const pgreplErrCode = 2
const pgreplInitialStackSize = 16

//line pgrepl-gen.y:503

//line yacctab:1
var pgreplExca = [...]int8{
	-1, 1,
	1, -1,
	-2, 0,
}

const pgreplPrivate = 57344

const pgreplLast = 91

var pgreplAct = [...]int8{
	83, 67, 27, 28, 30, 86, 87, 68, 31, 32,
	73, 33, 34, 35, 36, 37, 38, 39, 40, 41,
	42, 43, 44, 45, 46, 47, 85, 55, 19, 54,
	55, 79, 20, 80, 76, 77, 78, 52, 12, 11,
	16, 13, 14, 15, 17, 61, 62, 53, 22, 60,
	49, 65, 57, 56, 58, 89, 66, 59, 71, 63,
	26, 84, 70, 50, 25, 24, 23, 48, 18, 75,
	69, 74, 81, 51, 29, 21, 88, 82, 72, 64,
	10, 9, 3, 8, 7, 6, 5, 4, 90, 2,
	1,
}

var pgreplPact = [...]int16{
	29, -1000, 1, -1000, -1000, -1000, -1000, -1000, -1000, -1000,
	-1000, -1000, 4, 28, 61, 60, 59, 54, -1000, -1000,
	-1, 32, 58, 15, 31, -1000, -1000, 0, -1000, 48,
	-1000, -1000, -1000, -1000, -1000, -1000, -1000, -1000, -1000, -1000,
	-1000, -1000, -1000, -1000, -1000, -1000, -1000, -1000, 50, -1000,
	30, 27, -1000, -1000, -1000, -1, -1000, -1000, -1000, 34,
	49, -21, 57, -1000, -1000, 52, -18, -1000, -1, 10,
	-21, -1000, -1000, 56, -3, -1000, -1000, -1000, -1000, -1000,
	-1000, -1000, -24, -1000, 51, -1000, -1000, 56, -1000, -1000,
	-1000,
}

var pgreplPgo = [...]int8{
	0, 90, 89, 87, 86, 85, 84, 83, 82, 81,
	80, 2, 3, 79, 78, 77, 0, 76, 75, 74,
	73, 1, 70, 69, 68, 67,
}

var pgreplR1 = [...]int8{
	0, 1, 24, 24, 2, 2, 2, 2, 2, 2,
	2, 2, 8, 9, 3, 3, 6, 6, 21, 21,
	22, 22, 23, 23, 23, 23, 23, 7, 7, 4,
	5, 10, 25, 25, 20, 20, 18, 18, 13, 13,
	14, 14, 15, 15, 16, 17, 17, 11, 11, 12,
	12, 12, 12, 19, 19, 19, 19, 19, 19, 19,
	19, 19, 19, 19, 19, 19, 19, 19, 19, 19,
	19,
}

var pgreplR2 = [...]int8{
	0, 2, 1, 0, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 2, 4, 1, 5, 6, 3, 1,
	2, 0, 1, 1, 1, 1, 1, 2, 3, 5,
	6, 2, 1, 0, 1, 0, 2, 0, 2, 0,
	3, 0, 1, 3, 2, 1, 0, 3, 1, 1,
	2, 2, 2, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1,
}

var pgreplChk = [...]int16{
	-1000, -1, -2, -8, -3, -4, -5, -6, -7, -9,
	-10, 10, 9, 12, 13, 14, 11, 15, -24, 27,
	28, -18, 20, 5, 5, 5, 6, -11, -12, -19,
	5, 9, 10, 12, 13, 14, 15, 16, 17, 18,
	19, 20, 21, 22, 23, 24, 25, 26, -25, 18,
	5, -20, 22, 16, 29, 30, 5, 4, 6, 7,
	19, 18, 19, -12, -13, 17, 7, -21, 28, -22,
	5, 6, -14, 28, -11, -23, 24, 25, 26, 21,
	23, -21, -15, -16, 5, 29, 29, 30, -17, 4,
	-16,
}

var pgreplDef = [...]int8{
	0, -2, 3, 4, 5, 6, 7, 8, 9, 10,
	11, 12, 15, 37, 0, 0, 0, 0, 1, 2,
	0, 33, 0, 35, 27, 13, 31, 0, 48, 49,
	53, 54, 55, 56, 57, 58, 59, 60, 61, 62,
	63, 64, 65, 66, 67, 68, 69, 70, 0, 32,
	36, 0, 34, 28, 14, 0, 50, 51, 52, 39,
	0, 21, 0, 47, 29, 0, 41, 16, 0, 19,
	21, 38, 30, 0, 0, 20, 22, 23, 24, 25,
	26, 17, 0, 42, 46, 18, 40, 0, 44, 45,
	43,
}

var pgreplTok1 = [...]int8{
	1, 3, 3, 3, 3, 3, 3, 3, 3, 3,
	3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
	3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
	3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
	28, 29, 3, 3, 30, 3, 3, 3, 3, 3,
	3, 3, 3, 3, 3, 3, 3, 3, 3, 27,
}

var pgreplTok2 = [...]int8{
	2, 3, 4, 5, 6, 7, 8, 9, 10, 11,
	12, 13, 14, 15, 16, 17, 18, 19, 20, 21,
	22, 23, 24, 25, 26,
}

var pgreplTok3 = [...]int8{
	0,
}

var pgreplErrorMessages = [...]struct {
	state int
	token int
	msg   string
}{}

//line yaccpar:1

/*	parser for yacc output	*/

var (
	pgreplDebug        = 0
	pgreplErrorVerbose = false
)

type pgreplLexer interface {
	Lex(lval *pgreplSymType) int
	Error(s string)
}

type pgreplParser interface {
	Parse(pgreplLexer) int
	Lookahead() int
}

type pgreplParserImpl struct {
	lval  pgreplSymType
	stack [pgreplInitialStackSize]pgreplSymType
	char  int
}

func (p *pgreplParserImpl) Lookahead() int {
	return p.char
}

func pgreplNewParser() pgreplParser {
	return &pgreplParserImpl{}
}

const pgreplFlag = -1000

func pgreplTokname(c int) string {
	if c >= 1 && c-1 < len(pgreplToknames) {
		if pgreplToknames[c-1] != "" {
			return pgreplToknames[c-1]
		}
	}
	return __yyfmt__.Sprintf("tok-%v", c)
}

func pgreplStatname(s int) string {
	if s >= 0 && s < len(pgreplStatenames) {
		if pgreplStatenames[s] != "" {
			return pgreplStatenames[s]
		}
	}
	return __yyfmt__.Sprintf("state-%v", s)
}

func pgreplErrorMessage(state, lookAhead int) string {
	const TOKSTART = 4

	if !pgreplErrorVerbose {
		return "syntax error"
	}

	for _, e := range pgreplErrorMessages {
		if e.state == state && e.token == lookAhead {
			return "syntax error: " + e.msg
		}
	}

	res := "syntax error: unexpected " + pgreplTokname(lookAhead)

	// To match Bison, suggest at most four expected tokens.
	expected := make([]int, 0, 4)

	// Look for shiftable tokens.
	base := int(pgreplPact[state])
	for tok := TOKSTART; tok-1 < len(pgreplToknames); tok++ {
		if n := base + tok; n >= 0 && n < pgreplLast && int(pgreplChk[int(pgreplAct[n])]) == tok {
			if len(expected) == cap(expected) {
				return res
			}
			expected = append(expected, tok)
		}
	}

	if pgreplDef[state] == -2 {
		i := 0
		for pgreplExca[i] != -1 || int(pgreplExca[i+1]) != state {
			i += 2
		}

		// Look for tokens that we accept or reduce.
		for i += 2; pgreplExca[i] >= 0; i += 2 {
			tok := int(pgreplExca[i])
			if tok < TOKSTART || pgreplExca[i+1] == 0 {
				continue
			}
			if len(expected) == cap(expected) {
				return res
			}
			expected = append(expected, tok)
		}

		// If the default action is to accept or reduce, give up.
		if pgreplExca[i+1] != 0 {
			return res
		}
	}

	for i, tok := range expected {
		if i == 0 {
			res += ", expecting "
		} else {
			res += " or "
		}
		res += pgreplTokname(tok)
	}
	return res
}

func pgrepllex1(lex pgreplLexer, lval *pgreplSymType) (char, token int) {
	token = 0
	char = lex.Lex(lval)
	if char <= 0 {
		token = int(pgreplTok1[0])
		goto out
	}
	if char < len(pgreplTok1) {
		token = int(pgreplTok1[char])
		goto out
	}
	if char >= pgreplPrivate {
		if char < pgreplPrivate+len(pgreplTok2) {
			token = int(pgreplTok2[char-pgreplPrivate])
			goto out
		}
	}
	for i := 0; i < len(pgreplTok3); i += 2 {
		token = int(pgreplTok3[i+0])
		if token == char {
			token = int(pgreplTok3[i+1])
			goto out
		}
	}

out:
	if token == 0 {
		token = int(pgreplTok2[1]) /* unknown char */
	}
	if pgreplDebug >= 3 {
		__yyfmt__.Printf("lex %s(%d)\n", pgreplTokname(token), uint(char))
	}
	return char, token
}

func pgreplParse(pgrepllex pgreplLexer) int {
	return pgreplNewParser().Parse(pgrepllex)
}

func (pgreplrcvr *pgreplParserImpl) Parse(pgrepllex pgreplLexer) int {
	var pgrepln int
	var pgreplVAL pgreplSymType
	var pgreplDollar []pgreplSymType
	_ = pgreplDollar // silence set and not used
	pgreplS := pgreplrcvr.stack[:]

	Nerrs := 0   /* number of errors */
	Errflag := 0 /* error recovery flag */
	pgreplstate := 0
	pgreplrcvr.char = -1
	pgrepltoken := -1 // pgreplrcvr.char translated into internal numbering
	defer func() {
		// Make sure we report no lookahead when not parsing.
		pgreplstate = -1
		pgreplrcvr.char = -1
		pgrepltoken = -1
	}()
	pgreplp := -1
	goto pgreplstack

ret0:
	return 0

ret1:
	return 1

pgreplstack:
	/* put a state and value onto the stack */
	if pgreplDebug >= 4 {
		__yyfmt__.Printf("char %v in %v\n", pgreplTokname(pgrepltoken), pgreplStatname(pgreplstate))
	}

	pgreplp++
	if pgreplp >= len(pgreplS) {
		nyys := make([]pgreplSymType, len(pgreplS)*2)
		copy(nyys, pgreplS)
		pgreplS = nyys
	}
	pgreplS[pgreplp] = pgreplVAL
	pgreplS[pgreplp].yys = pgreplstate

pgreplnewstate:
	pgrepln = int(pgreplPact[pgreplstate])
	if pgrepln <= pgreplFlag {
		goto pgrepldefault /* simple state */
	}
	if pgreplrcvr.char < 0 {
		pgreplrcvr.char, pgrepltoken = pgrepllex1(pgrepllex, &pgreplrcvr.lval)
	}
	pgrepln += pgrepltoken
	if pgrepln < 0 || pgrepln >= pgreplLast {
		goto pgrepldefault
	}
	pgrepln = int(pgreplAct[pgrepln])
	if int(pgreplChk[pgrepln]) == pgrepltoken { /* valid shift */
		pgreplrcvr.char = -1
		pgrepltoken = -1
		pgreplVAL = pgreplrcvr.lval
		pgreplstate = pgrepln
		if Errflag > 0 {
			Errflag--
		}
		goto pgreplstack
	}

pgrepldefault:
	/* default state action */
	pgrepln = int(pgreplDef[pgreplstate])
	if pgrepln == -2 {
		if pgreplrcvr.char < 0 {
			pgreplrcvr.char, pgrepltoken = pgrepllex1(pgrepllex, &pgreplrcvr.lval)
		}

		/* look through exception table */
		xi := 0
		for {
			if pgreplExca[xi+0] == -1 && int(pgreplExca[xi+1]) == pgreplstate {
				break
			}
			xi += 2
		}
		for xi += 2; ; xi += 2 {
			pgrepln = int(pgreplExca[xi+0])
			if pgrepln < 0 || pgrepln == pgrepltoken {
				break
			}
		}
		pgrepln = int(pgreplExca[xi+1])
		if pgrepln < 0 {
			goto ret0
		}
	}
	if pgrepln == 0 {
		/* error ... attempt to resume parsing */
		switch Errflag {
		case 0: /* brand new error */
			pgrepllex.Error(pgreplErrorMessage(pgreplstate, pgrepltoken))
			Nerrs++
			if pgreplDebug >= 1 {
				__yyfmt__.Printf("%s", pgreplStatname(pgreplstate))
				__yyfmt__.Printf(" saw %s\n", pgreplTokname(pgrepltoken))
			}
			fallthrough

		case 1, 2: /* incompletely recovered error ... try again */
			Errflag = 3

			/* find a state where "error" is a legal shift action */
			for pgreplp >= 0 {
				pgrepln = int(pgreplPact[pgreplS[pgreplp].yys]) + pgreplErrCode
				if pgrepln >= 0 && pgrepln < pgreplLast {
					pgreplstate = int(pgreplAct[pgrepln]) /* simulate a shift of "error" */
					if int(pgreplChk[pgreplstate]) == pgreplErrCode {
						goto pgreplstack
					}
				}

				/* the current p has no shift on "error", pop stack */
				if pgreplDebug >= 2 {
					__yyfmt__.Printf("error recovery pops state %d\n", pgreplS[pgreplp].yys)
				}
				pgreplp--
			}
			/* there is no state on the stack with an error shift ... abort */
			goto ret1

		case 3: /* no shift yet; clobber input char */
			if pgreplDebug >= 2 {
				__yyfmt__.Printf("error recovery discards %s\n", pgreplTokname(pgrepltoken))
			}
			if pgrepltoken == pgreplEofCode {
				goto ret1
			}
			pgreplrcvr.char = -1
			pgrepltoken = -1
			goto pgreplnewstate /* try again in the same state */
		}
	}

	/* reduction by production pgrepln */
	if pgreplDebug >= 2 {
		__yyfmt__.Printf("reduce %v in:\n\t%v\n", pgrepln, pgreplStatname(pgreplstate))
	}

	pgreplnt := pgrepln
	pgreplpt := pgreplp
	_ = pgreplpt // guard against "declared and not used"

	pgreplp -= int(pgreplR2[pgrepln])
	// pgreplp is now the index of $0. Perform the default action. Iff the
	// reduced production is ε, $1 is possibly out of range.
	if pgreplp+1 >= len(pgreplS) {
		nyys := make([]pgreplSymType, len(pgreplS)*2)
		copy(nyys, pgreplS)
		pgreplS = nyys
	}
	pgreplVAL = pgreplS[pgreplp+1]

	/* consult goto table to find next state */
	pgrepln = int(pgreplR1[pgrepln])
	pgreplg := int(pgreplPgo[pgrepln])
	pgreplj := pgreplg + pgreplS[pgreplp].yys + 1

	if pgreplj >= pgreplLast {
		pgreplstate = int(pgreplAct[pgreplg])
	} else {
		pgreplstate = int(pgreplAct[pgreplj])
		if int(pgreplChk[pgreplstate]) != -pgrepln {
			pgreplstate = int(pgreplAct[pgreplg])
		}
	}
	// dummy call; replaced with literal code
	switch pgreplnt {

	case 1:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:172
		{
			pgrepllex.(*lexer).stmt = pgreplDollar[1].union.replicationStatement()
		}
	case 12:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:197
		{
			pgreplVAL.union.val = &pgrepltree.IdentifySystem{}
		}
	case 13:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:208
		{
			pgreplVAL.union.val = &pgrepltree.ReadReplicationSlot{
				Slot: tree.Name(pgreplDollar[2].str),
			}
		}
	case 14:
		pgreplDollar = pgreplS[pgreplpt-4 : pgreplpt+1]
//line pgrepl-gen.y:239
		{
			pgreplVAL.union.val = &pgrepltree.BaseBackup{
				Options: pgreplDollar[3].union.options(),
			}
		}
	case 15:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:245
		{
			pgreplVAL.union.val = &pgrepltree.BaseBackup{}
		}
	case 16:
		pgreplDollar = pgreplS[pgreplpt-5 : pgreplpt+1]
//line pgrepl-gen.y:253
		{
			pgreplVAL.union.val = &pgrepltree.CreateReplicationSlot{
				Slot:      tree.Name(pgreplDollar[2].str),
				Temporary: pgreplDollar[3].union.bool(),
				Kind:      pgrepltree.PhysicalReplication,
				Options:   pgreplDollar[5].union.options(),
			}
		}
	case 17:
		pgreplDollar = pgreplS[pgreplpt-6 : pgreplpt+1]
//line pgrepl-gen.y:263
		{
			pgreplVAL.union.val = &pgrepltree.CreateReplicationSlot{
				Slot:      tree.Name(pgreplDollar[2].str),
				Temporary: pgreplDollar[3].union.bool(),
				Kind:      pgrepltree.LogicalReplication,
				Plugin:    tree.Name(pgreplDollar[5].str),
				Options:   pgreplDollar[6].union.options(),
			}
		}
	case 18:
		pgreplDollar = pgreplS[pgreplpt-3 : pgreplpt+1]
//line pgrepl-gen.y:275
		{
			pgreplVAL.union = pgreplDollar[2].union
		}
	case 19:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:276
		{
			pgreplVAL.union = pgreplDollar[1].union
		}
	case 20:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:281
		{
			pgreplVAL.union.val = append(pgreplDollar[1].union.options(), pgreplDollar[2].union.option())
		}
	case 21:
		pgreplDollar = pgreplS[pgreplpt-0 : pgreplpt+1]
//line pgrepl-gen.y:283
		{
			pgreplVAL.union.val = pgrepltree.Options(nil)
		}
	case 22:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:288
		{
			pgreplVAL.union.val = pgrepltree.Option{
				Key:   tree.Name("snapshot"),
				Value: tree.NewStrVal("export"),
			}
		}
	case 23:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:295
		{
			pgreplVAL.union.val = pgrepltree.Option{
				Key:   tree.Name("snapshot"),
				Value: tree.NewStrVal("nothing"),
			}
		}
	case 24:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:302
		{
			pgreplVAL.union.val = pgrepltree.Option{
				Key:   tree.Name("snapshot"),
				Value: tree.NewStrVal("use"),
			}
		}
	case 25:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:309
		{
			pgreplVAL.union.val = pgrepltree.Option{
				Key:   tree.Name("reserve_wal"),
				Value: tree.NewStrVal("true"),
			}
		}
	case 26:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:316
		{
			pgreplVAL.union.val = pgrepltree.Option{
				Key:   tree.Name("two_phase"),
				Value: tree.NewStrVal("true"),
			}
		}
	case 27:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:327
		{
			pgreplVAL.union.val = &pgrepltree.DropReplicationSlot{
				Slot: tree.Name(pgreplDollar[2].str),
			}
		}
	case 28:
		pgreplDollar = pgreplS[pgreplpt-3 : pgreplpt+1]
//line pgrepl-gen.y:333
		{
			pgreplVAL.union.val = &pgrepltree.DropReplicationSlot{
				Slot: tree.Name(pgreplDollar[2].str),
				Wait: true,
			}
		}
	case 29:
		pgreplDollar = pgreplS[pgreplpt-5 : pgreplpt+1]
//line pgrepl-gen.y:346
		{
			ret := &pgrepltree.StartReplication{
				Slot: tree.Name(pgreplDollar[2].str),
				Kind: pgrepltree.PhysicalReplication,
				LSN:  pgreplDollar[4].union.lsn(),
			}
			if pgreplDollar[5].union.val != nil {
				ret.Timeline = pgreplDollar[5].union.numVal()
			}
			pgreplVAL.union.val = ret
		}
	case 30:
		pgreplDollar = pgreplS[pgreplpt-6 : pgreplpt+1]
//line pgrepl-gen.y:362
		{
			pgreplVAL.union.val = &pgrepltree.StartReplication{
				Slot:    tree.Name(pgreplDollar[3].str),
				Kind:    pgrepltree.LogicalReplication,
				LSN:     pgreplDollar[5].union.lsn(),
				Options: pgreplDollar[6].union.options(),
			}
		}
	case 31:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:376
		{
			if i, err := pgreplDollar[2].union.numVal().AsInt64(); err != nil || uint64(i) <= 0 {
				pgrepllex.(*lexer).setErr(pgerror.Newf(pgcode.Syntax, "expected a positive integer for timeline"))
				return 1
			}
			pgreplVAL.union.val = &pgrepltree.TimelineHistory{Timeline: pgreplDollar[2].union.numVal()}
		}
	case 34:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:391
		{
			pgreplVAL.union.val = true
		}
	case 35:
		pgreplDollar = pgreplS[pgreplpt-0 : pgreplpt+1]
//line pgrepl-gen.y:392
		{
			pgreplVAL.union.val = false
		}
	case 36:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:397
		{
			pgreplVAL.str = pgreplDollar[2].str
		}
	case 37:
		pgreplDollar = pgreplS[pgreplpt-0 : pgreplpt+1]
//line pgrepl-gen.y:399
		{
			pgreplVAL.str = ""
		}
	case 38:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:404
		{
			if i, err := pgreplDollar[2].union.numVal().AsInt64(); err != nil || uint64(i) <= 0 {
				pgrepllex.(*lexer).setErr(pgerror.Newf(pgcode.Syntax, "expected a positive integer for timeline"))
				return 1
			}
			pgreplVAL.union.val = pgreplDollar[2].union.numVal()
		}
	case 39:
		pgreplDollar = pgreplS[pgreplpt-0 : pgreplpt+1]
//line pgrepl-gen.y:411
		{
			pgreplVAL.union.val = nil
		}
	case 40:
		pgreplDollar = pgreplS[pgreplpt-3 : pgreplpt+1]
//line pgrepl-gen.y:416
		{
			pgreplVAL.union = pgreplDollar[2].union
		}
	case 41:
		pgreplDollar = pgreplS[pgreplpt-0 : pgreplpt+1]
//line pgrepl-gen.y:417
		{
			pgreplVAL.union.val = pgrepltree.Options(nil)
		}
	case 42:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:422
		{
			pgreplVAL.union.val = pgrepltree.Options{pgreplDollar[1].union.option()}
		}
	case 43:
		pgreplDollar = pgreplS[pgreplpt-3 : pgreplpt+1]
//line pgrepl-gen.y:426
		{
			pgreplVAL.union.val = append(pgreplDollar[1].union.options(), pgreplDollar[3].union.option())
		}
	case 44:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:433
		{
			pgreplVAL.union.val = pgrepltree.Option{
				Key:   tree.Name(pgreplDollar[1].str),
				Value: pgreplDollar[2].union.expr(),
			}
		}
	case 45:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:442
		{
			pgreplVAL.union.val = tree.NewStrVal(pgreplDollar[1].str)
		}
	case 46:
		pgreplDollar = pgreplS[pgreplpt-0 : pgreplpt+1]
//line pgrepl-gen.y:443
		{
			pgreplVAL.union.val = tree.Expr(nil)
		}
	case 47:
		pgreplDollar = pgreplS[pgreplpt-3 : pgreplpt+1]
//line pgrepl-gen.y:448
		{
			pgreplVAL.union.val = append(pgreplDollar[1].union.options(), pgreplDollar[3].union.option())
		}
	case 48:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:450
		{
			pgreplVAL.union.val = pgrepltree.Options{pgreplDollar[1].union.option()}
		}
	case 49:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:455
		{
			pgreplVAL.union.val = pgrepltree.Option{Key: tree.Name(pgreplDollar[1].str)}
		}
	case 50:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:459
		{
			pgreplVAL.union.val = pgrepltree.Option{
				Key:   tree.Name(pgreplDollar[1].str),
				Value: tree.NewStrVal(pgreplDollar[2].str),
			}
		}
	case 51:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:466
		{
			pgreplVAL.union.val = pgrepltree.Option{
				Key:   tree.Name(pgreplDollar[1].str),
				Value: tree.NewStrVal(pgreplDollar[2].str),
			}
		}
	case 52:
		pgreplDollar = pgreplS[pgreplpt-2 : pgreplpt+1]
//line pgrepl-gen.y:473
		{
			pgreplVAL.union.val = pgrepltree.Option{
				Key:   tree.Name(pgreplDollar[1].str),
				Value: pgreplDollar[2].union.numVal(),
			}
		}
	case 53:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:482
		{
			pgreplVAL.str = pgreplDollar[1].str
		}
	case 54:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:483
		{
			pgreplVAL.str = "base_backup"
		}
	case 55:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:484
		{
			pgreplVAL.str = "identify_system"
		}
	case 56:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:486
		{
			pgreplVAL.str = "start_replication"
		}
	case 57:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:487
		{
			pgreplVAL.str = "create_replication_slot"
		}
	case 58:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:488
		{
			pgreplVAL.str = "drop_replication_slot"
		}
	case 59:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:489
		{
			pgreplVAL.str = "timeline_history"
		}
	case 60:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:490
		{
			pgreplVAL.str = "wait"
		}
	case 61:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:491
		{
			pgreplVAL.str = "timeline"
		}
	case 62:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:492
		{
			pgreplVAL.str = "physical"
		}
	case 63:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:493
		{
			pgreplVAL.str = "logical"
		}
	case 64:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:494
		{
			pgreplVAL.str = "slot"
		}
	case 65:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:495
		{
			pgreplVAL.str = "reserve_wal"
		}
	case 66:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:496
		{
			pgreplVAL.str = "temporary"
		}
	case 67:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:497
		{
			pgreplVAL.str = "two_phase"
		}
	case 68:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:498
		{
			pgreplVAL.str = "export_snapshot"
		}
	case 69:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:499
		{
			pgreplVAL.str = "noexport_snapshot"
		}
	case 70:
		pgreplDollar = pgreplS[pgreplpt-1 : pgreplpt+1]
//line pgrepl-gen.y:500
		{
			pgreplVAL.str = "use_snapshot"
		}
	}
	goto pgreplstack /* stack new state and value */
}
