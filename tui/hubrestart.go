package tui

import (
	"fmt"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// checkVersion compares the hub with this binary on connect. After an update
// the hub still runs the old one: it restarts on the new one as soon as no
// turn runs, so an update cuts no turn.
func (a *App) checkVersion() {
	a.newer = ""
	if a.hub.Version == nil || a.hub.Restart == nil {
		return
	}
	info, err := a.c.Info()
	disk := a.hub.Version()
	if err != nil || info.Version == "" || disk == "" || info.Version == disk {
		return
	}
	a.newer = disk
	if a.running() > 0 {
		a.notify("this neoharness is newer than the hub; it restarts when the turns end", a.th.Fg3)
	}
	a.restartWhenIdle()
}

func (a *App) running() int {
	n := 0
	for _, w := range a.workspaces {
		n += w.Counts[model.StateRunning] + w.Counts[model.StateBackground]
		if w.Coordinator == model.StateRunning {
			n++
		}
	}
	return n
}

func (a *App) restartWhenIdle() {
	if a.newer == "" || a.running() > 0 {
		return
	}
	a.newer = ""
	a.restartHub("the hub restarts on this neoharness")
}

// restartHub restarts the hub in the background; the update stream comes
// back by itself once the new hub answers.
func (a *App) restartHub(why string) {
	a.notify(why, a.th.Fg3)
	go func() {
		if err := a.hub.Restart(); err != nil {
			_ = a.s.PostEvent(evError{"the hub did not restart: " + err.Error()})
		}
	}()
}

// askRestartHub restarts the hub, after a yes when turns run: they stop.
func (a *App) askRestartHub() {
	if a.hub.Restart == nil {
		a.notify("this neoharness cannot restart the hub", a.th.Alert)
		return
	}
	n := a.running()
	if n == 0 {
		a.restartHub("restarting the hub")
		return
	}
	turns := fmt.Sprintf("%d running turns stop", n)
	if n == 1 {
		turns = "the running turn stops"
	}
	a.notice, a.noticeSt = turns+" with the hub. restart it? y/n", a.th.Wait
	a.confirm = func() { a.restartHub("restarting the hub") }
}
