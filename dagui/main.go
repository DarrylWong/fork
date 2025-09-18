package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed ui/dist/*
var uiFS embed.FS

func main() {
	if runtime.GOOS != "darwin" {
		log.Fatal("This server is macOS-only.")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	addr := "http://" + ln.Addr().String()

	mux := http.NewServeMux()
	mux.Handle("/", spa(uiFS, "ui/dist"))
	mux.HandleFunc("/api/dag", handleDag)

	srv := &http.Server{Handler: logRequests(mux), ReadHeaderTimeout: 10 * time.Second}

	go func() {
		log.Printf("DAG UI: %s\n", addr)
		_ = exec.Command("open", addr).Start() // mac-only
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	// block; use Ctrl+C to quit
	select {}
}

func handleDag(w http.ResponseWriter, r *http.Request) {
	test := strings.TrimSpace(r.URL.Query().Get("test"))
	if test == "" {
		http.Error(w, "missing ?test=", http.StatusBadRequest)
		return
	}

	raw, stderr, err := runRoachtestJSON(r.Context(), test)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "roachtest failed: %v\n\nstdout:\n%s\n\nstderr:\n%s\n", err, raw, stderr)
		return
	}

	data, extractErr := extractJSON(raw)
	if extractErr != nil {
		http.Error(w, "failed to extract JSON: "+extractErr.Error(), http.StatusBadRequest)
		return
	}

	var any interface{}
	if err := json.Unmarshal(data, &any); err != nil {
		http.Error(w, "invalid JSON from roachtest: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func runRoachtestJSON(ctx context.Context, test string) (stdout, stderr []byte, err error) {
	rt := "./roachtest" // mac-only server expects local binary
	if _, statErr := os.Stat(rt); statErr != nil {
		return nil, nil, fmt.Errorf("roachtest not found at ./roachtest")
	}

	var outBuf, errBuf bytes.Buffer

	// generous timeout (JSON generation can be slow)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, rt, "run", test, "--local")
	cmd.Env = append(os.Environ(), "MOD_TEST_GENERATE_JSON=true")
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

func extractJSON(raw []byte) ([]byte, error) {
	trim := bytes.TrimSpace(raw)
	if len(trim) > 0 && (trim[0] == '[' || trim[0] == '{') {
		return trim, nil
	}
	type opener struct{ open, close byte }
	for _, c := range []opener{{'[', ']'}, {'{', '}'}} {
		start := bytes.IndexByte(raw, c.open)
		if start == -1 {
			continue
		}
		if out, ok := sliceBalancedJSON(raw[start:], c.open, c.close); ok {
			return out, nil
		}
	}
	return nil, fmt.Errorf("no JSON object/array found in output")
}

func sliceBalancedJSON(b []byte, open, close byte) ([]byte, bool) {
	depth := 0
	inString, esc := false, false
	for i, ch := range b {
		if inString {
			if esc {
				esc = false
				continue
			}
			if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return b[:i+1], true
			}
		}
	}
	return nil, false
}

func spa(fs embed.FS, root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Clean(filepath.Join(root, r.URL.Path))
		if !strings.HasPrefix(p, root) {
			http.NotFound(w, r)
			return
		}
		if f, err := fs.Open(p); err == nil {
			defer f.Close()
			http.ServeContent(w, r, p, time.Time{}, toReadSeeker(f))
			return
		}
		index := filepath.Join(root, "index.html")
		f2, err2 := fs.Open(index)
		if err2 != nil {
			http.NotFound(w, r)
			return
		}
		defer f2.Close()
		http.ServeContent(w, r, index, time.Time{}, toReadSeeker(f2))
	})
}

func toReadSeeker(f io.Reader) io.ReadSeeker {
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, f)
	return bytes.NewReader(buf.Bytes())
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
