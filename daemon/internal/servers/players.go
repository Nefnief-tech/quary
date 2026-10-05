// Player list support: the daemon runs `list` on the console and parses
// the reply. All loaders we ship (Vanilla, Paper, Fabric) print the same
// "There are N of a max of M players online: A, B" line.
package servers

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/daemon/internal/runtime"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// listRe tolerates log prefixes ("[12:34:56] [Server thread/INFO]: ").
var listRe = regexp.MustCompile(`There are (\d+) of a max of (\d+) players online(?::\s*(.*))?`)

// PlayerList is the parsed `list` output (JSON-encoded in results).
type PlayerList struct {
	Names  []string `json:"names"`
	Online int      `json:"online"`
	Max    int      `json:"max"`
}

// tapConsole registers a channel that receives every console line of a
// running server until the returned release func is called.
func (m *Manager) tapConsole(id uuid.UUID) (<-chan string, func(), bool) {
	p := m.get(id)
	if p == nil {
		return nil, nil, false
	}
	ch := make(chan string, 64)
	p.mu.Lock()
	if p.taps == nil {
		p.taps = map[chan string]struct{}{}
	}
	p.taps[ch] = struct{}{}
	p.mu.Unlock()
	return ch, func() {
		p.mu.Lock()
		delete(p.taps, ch)
		p.mu.Unlock()
	}, true
}

// feedTaps fans a console line out to all taps; slow taps drop the line.
func feedTaps(p *process, line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.taps {
		select {
		case ch <- line:
		default:
		}
	}
}

// PollPlayers writes `list` to the console and parses the reply line out of
// the tapped console stream. Polling keeps the feature loader-agnostic — no
// plugin or query protocol required.
func (m *Manager) PollPlayers(ctx context.Context, cmd *nodesv1.PollPlayers) (*PlayerList, error) {
	id, err := uuid.Parse(cmd.GetServerId())
	if err != nil {
		return nil, fmt.Errorf("invalid server id: %w", err)
	}
	p := m.get(id)
	if p == nil || p.console == nil {
		return nil, runtime.ErrNotRunning
	}
	tap, done, ok := m.tapConsole(id)
	if !ok {
		return nil, runtime.ErrNotRunning
	}
	defer done()
	if err := p.console.WriteLine("list"); err != nil {
		return nil, fmt.Errorf("send list: %w", err)
	}

	window := 3 * time.Second
	if deadline, has := ctx.Deadline(); has {
		if d := time.Until(deadline); d < window {
			window = d
		}
	}
	timer := time.NewTimer(window)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, fmt.Errorf("no list response within %s", window)
		case line := <-tap:
			if pl, ok := parseListLine(line); ok {
				return pl, nil
			}
		}
	}
}

// parseListLine extracts the player list from one console line.
func parseListLine(line string) (*PlayerList, bool) {
	match := listRe.FindStringSubmatch(line)
	if match == nil {
		return nil, false
	}
	online, _ := strconv.Atoi(match[1])
	max, _ := strconv.Atoi(match[2])
	var names []string
	if raw := strings.TrimSpace(match[3]); raw != "" {
		for _, n := range strings.Split(raw, ", ") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	}
	return &PlayerList{Names: names, Online: online, Max: max}, true
}
