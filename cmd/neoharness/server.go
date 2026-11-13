package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/hubapi"
	"github.com/MahammadAgayev/neoharness/internal/model"
)

// stopWait is how long `hub stop` waits for the hub to go. The hub gives its
// turns 5 seconds to end, then closes its servers.
const stopWait = 15 * time.Second

func cmdHub(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "serve":
		return serveHub()
	case "start":
		c, err := ensureHub()
		if err != nil {
			return err
		}
		return printStatus(c)
	case "stop":
		return stopHub(hubapi.New(hubapi.SocketPath()))
	case "restart":
		c := hubapi.New(hubapi.SocketPath())
		if c.Ping() == nil {
			if err := stopHub(c); err != nil {
				return err
			}
		}
		if err := spawnHub(c); err != nil {
			return err
		}
		return printStatus(c)
	case "status":
		c := hubapi.New(hubapi.SocketPath())
		if c.Ping() != nil {
			return errors.New("no hub is running")
		}
		return printStatus(c)
	}
	return fmt.Errorf("unknown hub command %q; the commands are start, stop, restart, status, serve", sub)
}

// serveHub runs the hub in this process until a signal or a stop request.
func serveHub() error {
	asked := make(chan struct{}, 1)
	stop, err := startHub(func() {
		select {
		case asked <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-sig:
	case <-asked:
	}
	stop()
	return nil
}

// ensureHub returns a client for the hub on the socket, starting one in the
// background if none answers.
func ensureHub() (*hubapi.Client, error) {
	c := hubapi.New(hubapi.SocketPath())
	if c.Ping() == nil {
		return c, nil
	}
	return c, spawnHub(c)
}

// spawnHub starts `neoharness hub serve` in a session of its own, so closing
// the terminal does not reach it, and waits until it answers.
func spawnHub(c *hubapi.Client) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(hubapi.SocketPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(dir, "hub.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "hub", "serve")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	// The hub outlives the folder it was started in, which may be removed.
	cmd.Dir, _ = os.UserHomeDir()
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-exited:
			// Another neoharness may have started one at the same moment.
			if c.Ping() == nil {
				return nil
			}
			return fmt.Errorf("the hub did not start: %s", lastLine(logPath))
		case <-deadline:
			return fmt.Errorf("the hub did not answer in 10 seconds; see %s", logPath)
		case <-time.After(50 * time.Millisecond):
			if c.Ping() == nil {
				return nil
			}
		}
	}
}

func stopHub(c *hubapi.Client) error {
	if c.Ping() != nil {
		return errors.New("no hub is running")
	}
	if err := c.StopHub(); err != nil {
		return fmt.Errorf("%w; a hub of an older neoharness stops when the neoharness that runs it closes", err)
	}
	for end := time.Now().Add(stopWait); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if c.Ping() != nil {
			fmt.Println("hub stopped")
			return nil
		}
	}
	return fmt.Errorf("the hub did not stop in %s", stopWait)
}

func printStatus(c *hubapi.Client) error {
	info, err := c.Info()
	if err != nil {
		return err
	}
	ws, err := c.Workspaces()
	if err != nil {
		return err
	}
	running := 0
	for _, w := range ws {
		running += w.Counts[model.StateRunning]
		if w.Coordinator == model.StateRunning {
			running++
		}
	}
	fmt.Printf("hub      pid %d, up %s\n", info.PID, time.Since(info.Started).Round(time.Second))
	fmt.Printf("socket   %s\n", hubapi.SocketPath())
	fmt.Printf("turns    %d running\n", running)
	if info.Version != "" {
		v := info.Version
		if own := version(); own != "" && own != v {
			v += " (this neoharness is " + own + "; run neoharness hub restart)"
		}
		fmt.Printf("version  %s\n", v)
	}
	return nil
}

// version is the commit this binary was built from, empty without version
// control data.
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev != "" && dirty {
		rev += "-dirty"
	}
	return rev
}

func lastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer f.Close()
	last := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			last = l
		}
	}
	return last
}
