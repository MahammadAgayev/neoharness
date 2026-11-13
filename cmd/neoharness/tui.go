package main

import (
	"github.com/MahammadAgayev/neoharness/internal/tui"
)

func cmdTUI() error {
	c, err := ensureHub()
	if err != nil {
		return err
	}
	return tui.Run(c, tui.Hub{
		Start: func() error {
			_, err := ensureHub()
			return err
		},
		Restart: func() error {
			if c.Ping() == nil {
				if err := stopHub(c); err != nil {
					return err
				}
			}
			return spawnHub(c)
		},
		Version: version(),
	})
}
