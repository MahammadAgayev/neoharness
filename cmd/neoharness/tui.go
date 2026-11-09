package main

import (
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/MahammadAgayev/neoharness/internal/hubapi"
	"github.com/MahammadAgayev/neoharness/internal/tui"
)

func cmdTUI() error {
	var own ownHub
	c, err := own.ensure()
	if err != nil {
		return err
	}
	defer own.stop()
	// claude runs in its own process group, so a closed terminal does not
	// reach it. Stop the turns here, or they run on with nobody to see them.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		own.stop()
		os.Exit(1)
	}()
	return tui.Run(c, func() error {
		_, err := own.ensure()
		return err
	})
}

// ownHub is the hub this process runs, if it runs one. Another open
// neoharness may run it instead; when that one closes, this one takes over.
type ownHub struct {
	mu     sync.Mutex
	stopFn func()
}

// ensure returns a client for the hub that answers on the socket, starting
// one in this process if none does.
func (o *ownHub) ensure() (*hubapi.Client, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	c := hubapi.New(hubapi.SocketPath())
	if o.stopFn != nil || c.Ping() == nil {
		return c, nil
	}
	stop, err := startHub()
	if err != nil {
		// Another neoharness may have started one at the same moment.
		if c.Ping() == nil {
			return c, nil
		}
		return nil, err
	}
	o.stopFn = stop
	return c, nil
}

func (o *ownHub) stop() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopFn != nil {
		o.stopFn()
		o.stopFn = nil
	}
}
