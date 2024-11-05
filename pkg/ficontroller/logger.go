package ficontroller

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	crdblog "github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/syncutil"
	"github.com/cockroachdb/errors"
)

// The flags used by the internal loggers.
const logFlags = log.Lshortfile | log.Ltime | log.LUTC | log.Ldate

type logger struct {
	path string
	File *os.File
	// stdoutL and stderrL are the loggers used internally by Printf()/Errorf().
	// They write to Stdout/Stderr (below), but prefix the messages with
	// logger-specific formatting (file/line, time), plus an optional configurable
	// prefix.
	stdoutL, stderrL *log.Logger

	mu struct {
		syncutil.Mutex
		closed bool
	}
}

// TODO: do we need child loggers?
func newLogger(path string) (*logger, error) {
	if path == "" {
		return nil, errors.New("path must be non-empty")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	stdout := f
	stderr := f
	stdoutL := log.New(stdout, "", logFlags)
	stderrL := log.New(stderr, "", logFlags)
	return &logger{
		path:    path,
		File:    f,
		stdoutL: stdoutL,
		stderrL: stderrL,
	}, nil
}

func (l *logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.mu.closed {
		return
	}
	l.mu.closed = true
	if l.File != nil {
		l.File.Close()
		l.File = nil
	}
}

func (l *logger) Printf(f string, args ...interface{}) {
	msg := crdblog.FormatWithContextTags(context.Background(), f, args...)
	if err := l.stdoutL.Output(2, msg); err != nil {
		// Changing our interface to return an Error from a logging method seems too
		// onerous. Let's yell to the default logger and if that fails, oh well.
		_ = log.Output(2, fmt.Sprintf("failed to log message: %v: %s", err, msg))
	}
}
