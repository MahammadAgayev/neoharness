package claude

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"

	"github.com/MahammadAgayev/neoharness/internal/agent"
)

// The models depend on the account and the version of claude, not on the
// thread, so one answer per command and host does.
var models = struct {
	sync.Mutex
	got map[string][]agent.Model
}{got: map[string][]agent.Model{}}

// aliases are what --model takes when claude does not answer.
var aliases = []agent.Model{{Name: "opus"}, {Name: "sonnet"}, {Name: "haiku"}}

// Models asks claude for its /model choices, as its own picker shows them:
// the initialize request of the stream-json protocol answers with them.
func (a *Agent) Models(ctx context.Context) []agent.Model {
	key := strings.Join(append(append([]string{}, a.Command...), a.Host), "\x00")
	models.Lock()
	got, ok := models.got[key]
	models.Unlock()
	if ok {
		return got
	}
	got, err := a.askModels(ctx)
	if err != nil || len(got) == 0 {
		return aliases
	}
	models.Lock()
	models.got[key] = got
	models.Unlock()
	return got
}

func (a *Agent) askModels(ctx context.Context) ([]agent.Model, error) {
	args := append(append([]string{}, a.Command...), "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose")
	s, err := start(a.command(args, ""), "")
	if err != nil {
		return nil, err
	}
	defer s.kill()
	if err := s.send(map[string]any{"type": "control_request", "request_id": "models", "request": map[string]any{"subtype": "initialize"}}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case line, open := <-s.lines:
			if !open {
				return nil, nil
			}
			var r struct {
				Type     string `json:"type"`
				Response struct {
					RequestID string `json:"request_id"`
					Response  struct {
						Models []struct {
							Value                 string   `json:"value"`
							ResolvedModel         string   `json:"resolvedModel"`
							DisplayName           string   `json:"displayName"`
							Description           string   `json:"description"`
							SupportedEffortLevels []string `json:"supportedEffortLevels"`
						} `json:"models"`
					} `json:"response"`
				} `json:"response"`
			}
			if json.Unmarshal(line, &r) != nil || r.Type != "control_response" || r.Response.RequestID != "models" {
				continue
			}
			var out []agent.Model
			for _, m := range r.Response.Response.Models {
				out = append(out, agent.Model{
					Name:        m.Value,
					Label:       plain(m.DisplayName),
					Version:     m.ResolvedModel,
					Description: plain(m.Description),
					Efforts:     m.SupportedEffortLevels,
				})
			}
			return out, nil
		}
	}
}

// terminal matches the escapes claude writes for a terminal: colours, and
// links that show only their text.
var terminal = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[A-Za-z]`)

func plain(s string) string { return terminal.ReplaceAllString(s, "") }
