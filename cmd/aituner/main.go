// aituner benchmarks and tunes a machine for local AI, then recommends models it can run well.
// It hosts a local web UI (opened in your browser) and shows status in a terminal UI.
package main

import (
	"bufio"
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
	"strings"
	"sync"
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
	appMode := flag.Bool("app", false, "run under the macOS app (implies -no-tui -no-open): a line protocol on stdin/stdout (see serveAppControl); exits when stdin closes")
	flag.Parse()
	if *showVersion {
		fmt.Println("aituner", version)
		return
	}

	if err := run(*port, *noTUI || *appMode, *noOpen || *appMode, *appMode); err != nil {
		fmt.Fprintln(os.Stderr, "aituner:", err)
		os.Exit(1)
	}
}

func run(port int, noTUI, noOpen, appMode bool) error {
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

	cfg := api.Config{Store: db, Tenant: "local", Platform: plat, Token: token, DataDir: dataDir,
		CanIRun: canirun.New(), HF: hf.New(), Ollama: bench.NewOllama(), Runner: tune.NewRunner(), Version: version, Log: func(s string) { log.Print(s) }}
	out := &lineWriter{w: os.Stdout}
	if appMode {
		cfg.Executable, _ = os.Executable()
		cfg.AppPID = os.Getppid() // the app shell started us
		cfg.Notify = func(line string) { out.send(line) }
		cfg.Quit = func() { out.send("quit") }
	}
	srv, err := api.New(ctx, cfg)
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

	if appMode {
		go serveAppControl(os.Stdin, out, link, stop, func(cmd string) { appCommand(ctx, srv, cmd) })
		<-ctx.Done()
	} else if noTUI {
		fmt.Printf("aituner running. Open (single-use link): %s\n", link())
		<-ctx.Done()
	} else if err := runTUI(ctx, stop, srv, link); err != nil {
		return err
	}
	stop()
	srv.Close() // stops the gateway and the model server so no model keeps GPU memory after aituner exits
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdown)
}

// lineWriter serialises lines to the app shell: link replies and notifications come from different goroutines.
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) send(line string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := fmt.Fprintln(l.w, line)
	return err
}

// serveAppControl speaks the macOS app's line protocol and stops aituner when stdin closes, so the server never
// outlives the app, even one that was force-quit.
//
//	app -> aituner: an empty line asks for a fresh single-use launch link; "check" checks for an update now;
//	                "update" installs the available update; "skip <version>" stops offering that version.
//	aituner -> app: "link <url>"; "update <version>" (a newer release, prompt the user); "uptodate <version>";
//	                "update-error <message>"; "quit" (an update is staged: quit so it can be swapped in).
func serveAppControl(in io.Reader, out *lineWriter, link func() string, stop context.CancelFunc, command func(string)) {
	defer stop()
	if out.send("link "+link()) != nil {
		return
	}
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			go command(line)
			continue
		}
		if out.send("link "+link()) != nil {
			return
		}
	}
}

// appCommand runs one command from the app shell.
func appCommand(ctx context.Context, srv *api.Server, cmd string) {
	switch verb, arg, _ := strings.Cut(cmd, " "); verb {
	case "check":
		srv.CheckForUpdate(ctx, true)
	case "update":
		if err := srv.InstallUpdate(ctx); err != nil {
			srv.NotifyApp("update-error " + err.Error())
		}
	case "skip":
		_ = srv.SkipUpdate(ctx, arg)
	default:
		log.Printf("app: unknown command %q", verb)
	}
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
