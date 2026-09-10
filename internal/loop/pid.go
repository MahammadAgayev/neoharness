package loop

import "os"

// processID is the pid recorded in state.json while a run is live.
//
// Its own function so tests can reason about the field without caring how it
// is obtained, and so the one place that decides "what identifies this process"
// is named.
func processID() int { return os.Getpid() }
