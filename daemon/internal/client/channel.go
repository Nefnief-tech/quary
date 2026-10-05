package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// eventQueueDepth bounds in-flight events; overflow drops stats first via
// the emit helpers' non-blocking sends for high-volume payloads.
const eventQueueDepth = 8192

// emitter serializes events onto the Channel stream through a single sender
// goroutine.
type emitter struct {
	ch  chan *nodesv1.ChannelRequest
	log *slog.Logger
}

func newEmitter(log *slog.Logger) *emitter {
	return &emitter{ch: make(chan *nodesv1.ChannelRequest, eventQueueDepth), log: log}
}

func (e *emitter) queue(req *nodesv1.ChannelRequest) {
	select {
	case e.ch <- req:
	default:
		e.log.Warn("event queue full; dropping event")
	}
}

func (e *emitter) event(id uuid.UUID, payload *nodesv1.Event) {
	payload.ServerId = id.String()
	e.queue(&nodesv1.ChannelRequest{Payload: &nodesv1.ChannelRequest_Event{Event: payload}})
}

func (e *emitter) EmitConsole(id uuid.UUID, line string) {
	e.event(id, &nodesv1.Event{Payload: &nodesv1.Event_Console{Console: &nodesv1.ConsoleLine{
		AtUnixMilli: time.Now().UnixMilli(),
		Line:        line,
	}}})
}

func (e *emitter) EmitStats(id uuid.UUID, s *nodesv1.ServerStats) {
	e.event(id, &nodesv1.Event{Payload: &nodesv1.Event_Stats{Stats: s}})
}

func (e *emitter) EmitStatus(id uuid.UUID, status, detail string) {
	e.event(id, &nodesv1.Event{Payload: &nodesv1.Event_Status{Status: &nodesv1.StatusChange{
		Status: status, Detail: detail,
	}}})
}

func (e *emitter) EmitInstallProgress(id uuid.UUID, step string, percent float64, detail string) {
	e.event(id, &nodesv1.Event{Payload: &nodesv1.Event_InstallProgress{InstallProgress: &nodesv1.InstallProgress{
		Step: step, Percent: percent, Detail: detail,
	}}})
}

// result sends the terminal CommandResult for a correlation id.
func (e *emitter) result(correlationID string, ok bool, errMsg string, body []byte) {
	e.queue(&nodesv1.ChannelRequest{Payload: &nodesv1.ChannelRequest_Result{Result: &nodesv1.CommandResult{
		CorrelationId: correlationID,
		Ok:            ok,
		Error:         errMsg,
		Body:          body,
	}}})
}

// pump drains the queue onto the stream; it exits when ctx is cancelled.
func (e *emitter) pump(ctx context.Context, stream nodesv1.NodeAgentService_ChannelClient) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-e.ch:
			if err := stream.Send(req); err != nil {
				e.log.Warn("send event failed", "err", err)
				return
			}
		}
	}
}

// runChannel opens the command channel, reconciles local servers, and
// dispatches panel commands until the stream or context ends.
func (c *Client) runChannel(ctx context.Context, api nodesv1.NodeAgentServiceClient) error {
	stream, err := api.Channel(ctx)
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	em := newEmitter(c.log)
	go em.pump(ctx, stream)

	// Long-lived pumps must follow reconnects.
	c.manager.SetEmitter(em)

	// Adopt containers left running by a previous daemon instance.
	c.manager.Reconcile(ctx, em)

	for {
		msg, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("channel recv: %w", err)
		}
		cmd := msg.GetCommand()
		if cmd == nil {
			continue
		}
		// Commands run concurrently; results carry the correlation id.
		go c.handleCommand(ctx, em, cmd)
	}
}

func (c *Client) handleCommand(ctx context.Context, em *emitter, cmd *nodesv1.Command) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	var body []byte
	err := c.dispatch(ctx, cmd, em, &body)
	if err != nil {
		c.log.Warn("command failed", "correlation_id", cmd.GetCorrelationId(), "err", err)
		em.result(cmd.GetCorrelationId(), false, err.Error(), nil)
		return
	}
	em.result(cmd.GetCorrelationId(), true, "", body)
}

func (c *Client) dispatch(ctx context.Context, cmd *nodesv1.Command, em *emitter, body *[]byte) error {
	m := c.manager
	switch action := cmd.GetAction().(type) {
	case *nodesv1.Command_InstallServer:
		return m.Install(ctx, action.InstallServer, em)
	case *nodesv1.Command_StartServer:
		return m.Start(ctx, action.StartServer, em)
	case *nodesv1.Command_StopServer:
		return m.Stop(ctx, action.StopServer, em)
	case *nodesv1.Command_KillServer:
		return m.Kill(ctx, action.KillServer, em)
	case *nodesv1.Command_SendCommand:
		return m.SendCommand(action.SendCommand)
	case *nodesv1.Command_ListFiles:
		serverID, path, err := parseFileTarget(action.ListFiles.GetServerId(), action.ListFiles.GetPath())
		if err != nil {
			return err
		}
		entries, err := m.ListFiles(serverID, path)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		*body = raw
		return nil
	case *nodesv1.Command_ReadFile:
		serverID, path, err := parseFileTarget(action.ReadFile.GetServerId(), action.ReadFile.GetPath())
		if err != nil {
			return err
		}
		content, err := m.ReadFile(serverID, path, int64(action.ReadFile.GetMaxBytes()))
		if err != nil {
			return err
		}
		*body = content
		return nil
	case *nodesv1.Command_WriteFile:
		serverID, path, err := parseFileTarget(action.WriteFile.GetServerId(), action.WriteFile.GetPath())
		if err != nil {
			return err
		}
		return m.WriteFile(serverID, path, action.WriteFile.GetContent(), action.WriteFile.GetCreateDirs())
	case *nodesv1.Command_DeleteFile:
		serverID, path, err := parseFileTarget(action.DeleteFile.GetServerId(), action.DeleteFile.GetPath())
		if err != nil {
			return err
		}
		return m.DeletePath(serverID, path, action.DeleteFile.GetRecursive())
	default:
		return fmt.Errorf("unknown command action")
	}
}

func parseFileTarget(serverIDStr, path string) (uuid.UUID, string, error) {
	id, err := uuid.Parse(serverIDStr)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("invalid server id: %w", err)
	}
	if path == "" {
		path = "."
	}
	return id, path, nil
}
