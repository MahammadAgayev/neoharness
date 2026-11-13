// Package hubserver serves the hub's local API over a unix socket. It is the
// server half of hubapi and holds no logic of its own: every route is one hub
// call, or one call to the process that runs the hub.
package hubserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/hub"
	"github.com/MahammadAgayev/neoharness/internal/hubapi"
	"github.com/MahammadAgayev/neoharness/internal/model"
)

// Handler routes the API. stop asks the process that runs the hub to stop
// it; it must not wait for that, because the stop waits for this request.
func Handler(h *hub.Hub, info hubapi.Info, stop func()) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /hub", func(w http.ResponseWriter, r *http.Request) {
		reply(w, info, nil)
	})
	mux.HandleFunc("POST /hub/stop", func(w http.ResponseWriter, r *http.Request) {
		stop()
		reply(w, nil, nil)
	})

	mux.HandleFunc("GET /workspaces", func(w http.ResponseWriter, r *http.Request) {
		reply(w, h.Workspaces(), nil)
	})
	mux.HandleFunc("POST /workspaces", func(w http.ResponseWriter, r *http.Request) {
		var in hubapi.NewWorkspace
		if !decode(w, r, &in) {
			return
		}
		reply(w, nil, h.CreateWorkspace(in.Name, in.Pin))
	})
	mux.HandleFunc("PUT /workspaces/{p}/folder", func(w http.ResponseWriter, r *http.Request) {
		var in hubapi.Folder
		if !decode(w, r, &in) {
			return
		}
		reply(w, nil, h.SetFolder(r.PathValue("p"), in.Pin))
	})
	mux.HandleFunc("PUT /workspaces/{p}/local", func(w http.ResponseWriter, r *http.Request) {
		var in hubapi.Local
		if !decode(w, r, &in) {
			return
		}
		reply(w, nil, h.SetLocal(r.PathValue("p"), in.Option, in.Agent))
	})
	mux.HandleFunc("DELETE /workspaces/{p}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, h.DeleteWorkspace(r.PathValue("p")))
	})
	mux.HandleFunc("GET /deleted", func(w http.ResponseWriter, r *http.Request) {
		names, err := h.DeletedWorkspaces()
		reply(w, names, err)
	})
	mux.HandleFunc("POST /workspaces/{p}/restore", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, h.RestoreWorkspace(r.PathValue("p")))
	})
	mux.HandleFunc("GET /workspaces/{p}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		as, err := h.Artifacts(r.PathValue("p"))
		reply(w, as, err)
	})
	mux.HandleFunc("GET /workspaces/{p}/artifacts/{a}", func(w http.ResponseWriter, r *http.Request) {
		a, text, err := h.ReadArtifact(r.PathValue("p"), r.PathValue("a"))
		reply(w, hubapi.ArtifactText{Artifact: a, Text: text}, err)
	})
	mux.HandleFunc("PUT /workspaces/{p}/artifacts/{a}", func(w http.ResponseWriter, r *http.Request) {
		var in hubapi.ArtifactEdit
		if !decode(w, r, &in) {
			return
		}
		a, text, err := h.EditArtifact(r.PathValue("p"), r.PathValue("a"), in.Text, in.BaseText, in.Base, in.Force)
		reply(w, hubapi.ArtifactText{Artifact: a, Text: text}, err)
	})
	mux.HandleFunc("GET /workspaces/{p}/threads", func(w http.ResponseWriter, r *http.Request) {
		vs, err := h.Threads(r.PathValue("p"))
		reply(w, vs, err)
	})
	mux.HandleFunc("POST /workspaces/{p}/threads", func(w http.ResponseWriter, r *http.Request) {
		var in hubapi.NewThread
		if !decode(w, r, &in) {
			return
		}
		reply(w, nil, h.OpenThread(r.PathValue("p"), model.User, in.Name, in.Brief, in.Location))
	})
	mux.HandleFunc("GET /workspaces/{p}/threads/{t}/events", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("tail"))
		evs, err := h.Events(r.PathValue("p"), r.PathValue("t"), n)
		if evs == nil {
			evs = []model.Event{}
		}
		reply(w, evs, err)
	})
	mux.HandleFunc("GET /workspaces/{p}/threads/{t}/completions", func(w http.ResponseWriter, r *http.Request) {
		c, err := h.Completions(r.PathValue("p"), r.PathValue("t"), r.URL.Query().Get("q"))
		reply(w, c, err)
	})
	mux.HandleFunc("GET /spend", func(w http.ResponseWriter, r *http.Request) {
		sp, err := h.Spend(r.URL.Query().Get("workspace"))
		reply(w, sp, err)
	})
	mux.HandleFunc("GET /workspaces/{p}/threads/{t}/choices", func(w http.ResponseWriter, r *http.Request) {
		c, err := h.Choices(r.PathValue("p"), r.PathValue("t"))
		reply(w, c, err)
	})
	mux.HandleFunc("POST /workspaces/{p}/threads/{t}/messages", func(w http.ResponseWriter, r *http.Request) {
		var in hubapi.Message
		if !decode(w, r, &in) {
			return
		}
		if in.Now {
			reply(w, nil, h.SendNow(r.PathValue("p"), r.PathValue("t"), in.Text))
			return
		}
		reply(w, nil, h.Send(r.PathValue("p"), model.User, r.PathValue("t"), in.Text))
	})
	mux.HandleFunc("POST /workspaces/{p}/threads/{t}/stop", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, h.Stop(r.PathValue("p"), r.PathValue("t")))
	})
	mux.HandleFunc("DELETE /workspaces/{p}/threads/{t}/queue", func(w http.ResponseWriter, r *http.Request) {
		at, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("at"))
		if err != nil {
			reply(w, nil, fmt.Errorf("at: %w", err))
			return
		}
		reply(w, nil, h.Unqueue(r.PathValue("p"), r.PathValue("t"), at))
	})
	mux.HandleFunc("POST /workspaces/{p}/threads/{t}/resolve", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, h.Resolve(r.PathValue("p"), model.User, r.PathValue("t")))
	})
	mux.HandleFunc("DELETE /workspaces/{p}/threads/{t}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, h.Delete(r.PathValue("p"), r.PathValue("t")))
	})
	mux.HandleFunc("POST /workspaces/{p}/threads/{t}/reopen", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, h.Reopen(r.PathValue("p"), r.PathValue("t")))
	})
	mux.HandleFunc("POST /workspaces/{p}/threads/{t}/answer", func(w http.ResponseWriter, r *http.Request) {
		var in hubapi.Answer
		if !decode(w, r, &in) {
			return
		}
		reply(w, nil, h.Answer(r.PathValue("p"), r.PathValue("t"), in.ID, in.Decision, in.Message))
	})
	mux.HandleFunc("GET /updates", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		ch, stop := h.Subscribe()
		defer stop()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		for {
			select {
			case u, ok := <-ch:
				if !ok {
					return
				}
				data, _ := json.Marshal(u)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
				if u.Kind == model.UpdateStopped {
					return
				}
			case <-r.Context().Done():
				return
			}
		}
	})
	return mux
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		reply(w, nil, fmt.Errorf("bad request: %w", err))
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(hubapi.Error{Error: err.Error()})
		return
	}
	if v == nil {
		v = struct{}{}
	}
	_ = json.NewEncoder(w).Encode(v)
}
