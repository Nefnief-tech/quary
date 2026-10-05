// Console capture support: the daemon writes a line to a running server's
// console and captures reply lines matching a pattern. Panel-side consumers
// parse the captured lines (player lists, whitelist, tunnel claim URLs).
package servers

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/daemon/internal/runtime"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// captureWindow bounds how long RunConsole waits for a matching reply.
const captureWindow = 3 * time.Second

// RunConsoleResult is the JSON-encoded command body.
type RunConsoleResult struct {
	Lines []string `json:"lines"`
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

// RunConsole runs one console input and captures reply lines matching the
// expect pattern. Polling the console keeps features loader-agnostic: no
// plugin or query protocol required.
func (m *Manager) RunConsole(ctx context.Context, cmd *nodesv1.RunConsole) (*RunConsoleResult, error) {
	id, err := uuid.Parse(cmd.GetServerId())
	if err != nil {
		return nil, fmt.Errorf("invalid server id: %w", err)
	}
	p := m.get(id)
	if p == nil || p.console == nil {
		return nil, runtime.ErrNotRunning
	}
	if len(cmd.GetInput()) > 256 || len(cmd.GetInput()) == 0 {
		return nil, fmt.Errorf("invalid console input")
	}
	if len(cmd.GetExpect()) > 256 {
		return nil, fmt.Errorf("invalid expect pattern")
	}
	expect, err := regexp.Compile(cmd.GetExpect())
	if err != nil {
		return nil, fmt.Errorf("expect pattern: %w", err)
	}
	maxLines := int(cmd.GetMaxLines())
	if maxLines <= 0 {
		maxLines = 1
	}
	if maxLines > 32 {
		maxLines = 32
	}

	tap, done, ok := m.tapConsole(id)
	if !ok {
		return nil, runtime.ErrNotRunning
	}
	defer done()
	if err := p.console.WriteLine(cmd.GetInput()); err != nil {
		return nil, fmt.Errorf("send console input: %w", err)
	}

	window := captureWindow
	if deadline, has := ctx.Deadline(); has {
		if d := time.Until(deadline); d < window {
			window = d
		}
	}
	timer := time.NewTimer(window)
	defer timer.Stop()

	out := &RunConsoleResult{Lines: []string{}}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return out, nil
		case line := <-tap:
			if expect.MatchString(line) {
				out.Lines = append(out.Lines, line)
				if len(out.Lines) >= maxLines {
					return out, nil
				}
			}
		}
	}
}
