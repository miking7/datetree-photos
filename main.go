package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/miking7/datetree-photos/components"
)

//go:embed static
var staticFS embed.FS

const (
	bindHost     = "127.0.0.1"
	autoPortLow  = 9700
	autoPortHigh = 9799
)

// Overridden at link time via -ldflags "-X main.version=...". "dev" is the local-build sentinel.
var version = "dev"

// httpListener is captured at startup so handleQuit can close it to unblock
// http.Serve and let main return cleanly. Closing the listener (rather than
// os.Exit) lets in-flight Config.Save calls finish.
var httpListener net.Listener

func main() {
	port := flag.Int("port", 0, "explicit port (0 = pick free in 9700-9799)")
	noOpen := flag.Bool("no-open", false, "don't auto-open browser")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version)
		return
	}
	components.Version = version

	listener, err := listen(*port)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	httpListener = listener

	url := fmt.Sprintf("http://%s/", listener.Addr().String())
	fmt.Println(url)

	if !*noOpen {
		// Fire-and-forget; if the launcher fails the user still has the printed URL.
		go func() { _ = openBrowser(url) }()
	}

	// Best-effort launch-time update check. Goroutine isolates a slow or
	// failing GitHub fetch from server startup; the result, if any, is
	// surfaced via components.SetBanner.
	if cfg, lerr := LoadConfig(); lerr == nil {
		go updater.RunCheckIfEnabled(context.Background(), cfg)
	}

	mux := newMux()
	if err := http.Serve(listener, mux); err != nil && !errors.Is(err, net.ErrClosed) {
		// net.ErrClosed is the expected exit when handleQuit closes the
		// listener; only surface unexpected errors.
		fmt.Println("server error:", err)
	}
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func listen(explicit int) (net.Listener, error) {
	if explicit != 0 {
		return net.Listen("tcp", fmt.Sprintf("%s:%d", bindHost, explicit))
	}
	for p := autoPortLow; p <= autoPortHigh; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", bindHost, p))
		if err == nil {
			return l, nil
		}
	}
	return nil, fmt.Errorf("no free port in %d-%d", autoPortLow, autoPortHigh)
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleHome)
	mux.HandleFunc("/scan", handleScan)
	mux.HandleFunc("/scan/events", handleScanEvents)
	mux.HandleFunc("/scan/cancel", handleScanCancel)
	mux.HandleFunc("/execute", handleExecute)
	mux.HandleFunc("/execute/events", handleExecuteEvents)
	mux.HandleFunc("/execute/cancel", handleExecuteCancel)
	mux.HandleFunc("/settings", handleSettings)
	mux.HandleFunc("/runs/", handleManifest)
	mux.HandleFunc("/update/check", handleUpdateCheck)
	mux.HandleFunc("/update/apply", handleUpdateApply)
	mux.HandleFunc("/update/events", handleUpdateEvents)
	mux.HandleFunc("/update/dismiss", handleUpdateDismiss)
	mux.HandleFunc("/quit", handleQuit)

	// Serve from the embedded FS rooted at /static/.
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	return mux
}
