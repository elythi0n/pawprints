package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/elythi0n/pawprints/internal/web"
)

// defaultUIPort is fixed so a bookmark keeps working.
const defaultUIPort = 7878

func (c *cli) cmdUI(args []string) int {
	fs := newFlags("ui")
	port := fs.Int("port", defaultUIPort, "")
	noOpen := fs.Bool("no-open", false, "")
	keep := fs.Bool("keep", false, "")
	rangeFlag := fs.String("range", "", "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	token, err := web.LoadToken()
	if err != nil {
		return c.failErr(err)
	}
	suffix := ""
	switch *rangeFlag {
	case "":
	case "1h", "24h", "7d", "30d":
		suffix = "&range=" + *rangeFlag
	default:
		return c.fail("--range is 1h, 24h, 7d, or 30d", 2)
	}
	// One server is enough: when one already answers on the default port,
	// open its page instead of starting another.
	if *port == defaultUIPort && !*keep {
		addr := fmt.Sprintf("127.0.0.1:%d", defaultUIPort)
		if web.Running(addr, token) {
			url := fmt.Sprintf("http://%s/?t=%s%s", addr, token, suffix)
			fmt.Fprintln(c.out, url)
			if !*noOpen {
				openBrowser(url)
			}
			return 0
		}
	}
	ln, err := web.Listen(*port, *port == defaultUIPort)
	if err != nil {
		return c.fail(fmt.Sprintf("cannot listen on port %d: %v", *port, err), 1)
	}
	url := fmt.Sprintf("http://%s/?t=%s%s", ln.Addr().String(), token, suffix)
	// The first line is the URL alone, so the editor extension can read it.
	fmt.Fprintln(c.out, url)
	if f, ok := c.out.(*os.File); ok {
		_ = f.Sync()
	}
	if !*noOpen {
		openBrowser(url)
	}
	var mu sync.Mutex
	srv := &web.Server{
		Token: token,
		Run: func(args []string) ([]byte, int) {
			// CLI handlers share process-wide styling, so one runs at a time.
			mu.Lock()
			defer mu.Unlock()
			var out, errOut bytes.Buffer
			code := RunWith(args, strings.NewReader(""), &out, &errOut)
			return out.Bytes(), code
		},
	}
	if !*keep {
		srv.Idle = time.Minute
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := srv.Serve(ctx, ln); err != nil {
		return c.failErr(err)
	}
	return 0
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	_ = cmd.Start()
	go func() { _ = cmd.Wait() }()
}
