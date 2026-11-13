package main

import (
	"github.com/MahammadAgayev/neoharness/internal/tui"
)

func cmdTUI() error {
	c, err := ensureHub()
	if err != nil {
		return err
	}
	return tui.Run(c, func() error {
		_, err := ensureHub()
		return err
	})
}
