// Command daemon is the Quarry node agent. It dials out to the panel over
// mTLS gRPC, enrolls with a single-use token on first boot, and keeps a
// heartbeat stream alive.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"quarry.dev/quarry/daemon/internal/client"
	"quarry.dev/quarry/daemon/internal/config"
	"quarry.dev/quarry/daemon/internal/logging"
	"quarry.dev/quarry/daemon/internal/runtime"
	"quarry.dev/quarry/daemon/internal/servers"
	"quarry.dev/quarry/daemon/internal/state"
)

func main() {
	log := logging.New(os.Getenv("QUARRY_ENV") == "dev")

	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("quarry-daemon %s\n", client.Version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Error("config failed", "err", err)
		os.Exit(1)
	}
	store, err := state.NewStore(cfg.DataDir)
	if err != nil {
		log.Error("state init failed", "err", err)
		os.Exit(1)
	}

	rt, err := runtime.NewDocker(cfg.ContainerUID)
	if err != nil {
		log.Error("docker runtime init failed", "err", err)
		os.Exit(1)
	}
	manager, err := servers.NewManager(servers.Config{
		ServersRoot:        cfg.ServersRoot,
		ArtifactAllowCIDRs: cfg.ArtifactAllowCIDRs,
	}, rt, log)
	if err != nil {
		log.Error("server manager init failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("daemon starting",
		"panel", cfg.PanelAddr,
		"node", cfg.NodeName,
		"data_dir", cfg.DataDir,
	)
	if err := client.New(cfg, store, log, manager).Run(ctx); err != nil {
		log.Error("daemon failed", "err", err)
		os.Exit(1)
	}
	log.Info("daemon stopped")
}
