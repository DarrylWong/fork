// Copyright 2018 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/cockroachdb/cockroach/pkg/sql/colexec/execgen"
	"github.com/cockroachdb/cockroach/pkg/sql/colexecerror"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/gostdlib/x/tools/imports"
)

func main() {
	gen := execgenTool{stdErr: os.Stderr}
	if !gen.run(os.Args[1:]...) {
		os.Exit(2)
	}
}

type execgenTool struct {
	// fmtSources runs the go fmt tool on code generated by execgenTool, if this
	// setting is true.
	fmtSources bool

	// stdErr is the writer to which all standard error output will be redirected.
	stdErr io.Writer

	// cmdLine stores the set of flags used to invoke the Execgen tool.
	cmdLine *flag.FlagSet
	verbose bool
}

// generator is a func that, given an input file's contents as a string,
// outputs the result of execgen to the outputFile.
type generator func(inputFileContents string, outputFile io.Writer) error

var generators = make(map[string]entry)

type entry struct {
	fn        generator
	inputFile string
}

func registerGenerator(g generator, outputFile, inputFile string) {
	if _, ok := generators[outputFile]; ok {
		colexecerror.InternalError(errors.AssertionFailedf("%s generator already registered", outputFile))
	}
	generators[outputFile] = entry{fn: g, inputFile: inputFile}
}

func (g *execgenTool) run(args ...string) bool {
	// Parse command line.
	var printDeps bool
	var template string
	g.cmdLine = flag.NewFlagSet("execgen", flag.ContinueOnError)
	g.cmdLine.SetOutput(g.stdErr)
	g.cmdLine.Usage = g.usage
	g.cmdLine.BoolVar(&g.fmtSources, "fmt", true, "format and imports-process generated code")
	g.cmdLine.BoolVar(&g.verbose, "verbose", false, "print out debug information to stderr")
	g.cmdLine.BoolVar(&printDeps, "M", false, "print the dependency list")
	g.cmdLine.StringVar(&template, "template", "", "path")
	err := g.cmdLine.Parse(args)
	if err != nil {
		return false
	}

	// Get remaining args after any flags have been parsed.
	args = g.cmdLine.Args()
	if len(args) != 1 {
		g.cmdLine.Usage()
		g.reportError(errors.New("invalid number of arguments"))
		return false
	}

	outPath := args[0]

	_, file := filepath.Split(outPath)
	e := generators[file]
	if e.fn == nil {
		g.reportError(errors.Errorf("unrecognized filename: %s", file))
		return false
	}
	if template != "" {
		if e.inputFile == "" {
			g.reportError(errors.Errorf("file %s expected no input template, found %s", file, template))
			return false
		}
		e.inputFile = template
	}
	if err := g.generate(outPath, e); err != nil {
		g.reportError(err)
		return false
	}
	return true
}

// This matches /* ... */ containing only spaces.
// We just delete those.
var emptyBlockCommentRegex = regexp.MustCompile(`(?m)^[ \t]*/\*[ \t]*\*/[ \t]*\n`)

// Empty comment in the middle of a line.
// We just delete those.
var emptyInlineCommentRegex = regexp.MustCompile(
	`(?m)(^[ \t]+//[ \t]*\n)`, /* not at beginning of line */
)

// Multiple consecutive empty comments at the beginning of a line.
// We replace this by a single empty comment.
var multiEmptyCommentRegex = regexp.MustCompile(
	`(?m)(^//[ \t]*)(\n//[ \t]*$)+`,
)

// Empty comments surrounded by empty lines.
// We delete those but keep an empty line.
var solitaryEmptyCommentRegex = regexp.MustCompile(`(?m)^(\n//[ \t]*\n)+`)

// Empty comment before a 'type' or 'func' keyword.
// We also delete those (and keep the keyword).
var emptyFuncTypeCommentRegex = regexp.MustCompile(`(?m)^\n//[ \t]*\n((?:func|type) )`)

func (g *execgenTool) generate(path string, entry entry) error {
	var buf bytes.Buffer
	buf.WriteString("// Code generated by execgen; DO NOT EDIT.\n")

	var inputFileContents string
	var err error
	if entry.inputFile != "" {
		inputFileBytes, err := os.ReadFile(entry.inputFile)
		if err != nil {
			return err
		}
		// Delete execgen_template build tag.
		inputFileBytes = bytes.ReplaceAll(inputFileBytes, []byte("//go:build execgen_template"), []byte{})
		inputFileContents, err = execgen.Generate(string(inputFileBytes))
		if err != nil {
			return err
		}
		if g.verbose {
			fmt.Fprintln(os.Stderr, "generated code before text/template runs")
			fmt.Fprintln(os.Stderr, "-----------------------------------")
			fmt.Fprintln(os.Stderr, inputFileContents)
			fmt.Fprintln(os.Stderr, "-----------------------------------")
		}
	}

	err = entry.fn(inputFileContents, &buf)
	if err != nil {
		return err
	}

	b := buf.Bytes()

	// Delete empty comments (/* */) that tend to get generated by templating.
	// As well as empty line comments that are _not_ at the beginning of a line.
	// As well as run-ins of multiple empty line comments at the beginning of a line
	// (when there is more than one).
	// As well as solitary empty line comments.
	//
	// Note: we do not remove single empty single-line comments (//)
	// that appear in the middle of a larger documentation
	// comment, because removing them can break the formatting inside the
	// comment and make gofmt unhappy.
	b = emptyBlockCommentRegex.ReplaceAllLiteral(b, []byte{})
	b = emptyInlineCommentRegex.ReplaceAllLiteral(b, []byte{})
	b = multiEmptyCommentRegex.ReplaceAllLiteral(b, []byte("//\n"))
	b = solitaryEmptyCommentRegex.ReplaceAllLiteral(b, []byte("\n"))
	b = emptyFuncTypeCommentRegex.ReplaceAll(b, []byte("$1"))

	if g.fmtSources {
		oldB := b
		b, err = imports.Process(path, b,
			&imports.Options{Comments: true, TabIndent: true, TabWidth: 2})
		if err != nil {
			// Write out incorrect source for easier debugging.
			b = oldB
			err = errors.Wrap(err, "Code formatting failed with Go parse error")
		}
	}

	// Ignore any write error if another error already occurred.
	_, writeErr := os.Stdout.Write(b)
	if err != nil {
		return err
	}
	return writeErr
}

// usage is a replacement usage function for the flags package.
func (g *execgenTool) usage() {
	fmt.Fprintf(g.stdErr, "Execgen is a tool for generating templated code related to ")
	fmt.Fprintf(g.stdErr, "columnarized execution.\n\n")

	fmt.Fprintf(g.stdErr, "Usage:\n")
	fmt.Fprintf(g.stdErr, "\texecgen [path]...\n\n")

	fmt.Fprintf(g.stdErr, "Supported filenames are:\n")
	for filename := range generators {
		fmt.Fprintf(g.stdErr, "\t%s\n", filename)
	}
	fmt.Fprintf(g.stdErr, "\n")

	fmt.Fprintf(g.stdErr, "Flags:\n")
	g.cmdLine.PrintDefaults()
	fmt.Fprintf(g.stdErr, "\n")
}

func (g *execgenTool) reportError(err error) {
	fmt.Fprintf(g.stdErr, "ERROR: %v\n", err)
}
