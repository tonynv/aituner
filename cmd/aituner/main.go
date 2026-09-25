// aituner benchmarks and tunes a machine for local AI, then recommends models it can run well.
// It hosts a local web UI (opened in your browser) and shows status in a terminal UI.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/tonynv/aituner/internal/api"
	"github.com/tonynv/aituner/internal/bench"
	"github.com/tonynv/aituner/internal/canirun"
	"github.com/tonynv/aituner/internal/hf"
	"github.com/tonynv/aituner/internal/lock"
	"github.com/tonynv/aituner/internal/platform"
	"github.com/tonynv/aituner/internal/store"
	"github.com/tonynv/aituner/internal/tune"
)

const defaultPort = 8737

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	port := flag.Int("port", defaultPort, "loopback port for the web UI (falls back to a free port if taken)")
	noTUI := flag.Bool("no-tui", false, "headless: log to stdout instead of the terminal UI")
	noOpen := flag.Bool("no-open", false, "do not open the browser")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("aituner", version)
		return
	}

	if err := run(*port, *noTUI, *noOpen); err != nil {
		fmt.Fprintln(os.Stderr, "aituner:", err)
		os.Exit(1)
	}
}

func run(port int, noTUI, noOpen bool) error {
	plat := platform.Current()
	dataDir, err := plat.DataDir()
	if err != nil {
		if errors.Is(err, platform.ErrUnsupported) {
			return fmt.Errorf("%s is not supported yet; aituner currently supports macOS on Apple Silicon", plat.Name())
		}
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	logf, err := openLog(filepath.Join(dataDir, "aituner.log"))
	if err != nil {
		return err
	}
	defer logf.Close()
	if noTUI {
		log.SetOutput(io.MultiWriter(os.Stderr, logf))
	} else {
		log.SetOutput(logf) // the terminal UI owns the screen
	}
	lk, err := lock.Acquire(filepath.Join(dataDir, "aituner.lock"))
	if err != nil {
		return err
	}
	defer lk.Release()
	db, err := store.Open(filepath.Join(dataDir, "aituner.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	token := hex.EncodeToString(tok)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := api.New(ctx, api.Config{Store: db, Tenant: "local", Platform: plat, Token: token, DataDir: dataDir,
		CanIRun: canirun.New(), HF: hf.New(), Ollama: bench.NewOllama(), Runner: tune.NewRunner(), Version: version, Log: func(s string) { log.Print(s) }})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return err
		}
	}
	actual := ln.Addr().(*net.TCPAddr).Port
	srv.SetPort(actual)
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print("server: ", err)
		}
	}()
	// each link carries a fresh single-use nonce, never the session token
	link := func() string { return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", actual, srv.LaunchToken()) }
	if !noOpen {
		openBrowser(link())
	}

	if noTUI {
		fmt.Printf("aituner running. Open (single-use link): %s\n", link())
		<-ctx.Done()
	} else if err := runTUI(ctx, stop, srv, link); err != nil {
		return err
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdown)
}

// openLog opens the private (0600) append-only log. A log over 5 MB is moved aside once at startup so it cannot
// grow without bound.
func openLog(path string) (*os.File, error) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		_ = os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func openBrowser(url string) {
	if runtime.GOOS != "darwin" {
		return
	}
	_ = exec.Command("/usr/bin/open", url).Start()
}
