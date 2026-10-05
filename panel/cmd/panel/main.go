// Command panel is the Quarry control plane binary.
//
// Usage:
//
//	panel serve                 run the API + node gRPC server
//	panel migrate               apply database migrations and exit
//	panel admin create          create an admin user non-interactively
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"quarry.dev/quarry/internal/seal"
	"quarry.dev/quarry/panel/internal/api"
	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/auth"
	"quarry.dev/quarry/panel/internal/config"
	"quarry.dev/quarry/panel/internal/consolelog"
	"quarry.dev/quarry/panel/internal/db"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/hub"
	"quarry.dev/quarry/panel/internal/logging"
	"quarry.dev/quarry/panel/internal/marketplace"
	"quarry.dev/quarry/panel/internal/nodes"
	"quarry.dev/quarry/panel/internal/players"
	"quarry.dev/quarry/panel/internal/runners"
	"quarry.dev/quarry/panel/internal/servers"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		cmdServe()
	case "migrate":
		cmdMigrate()
	case "admin":
		if len(os.Args) < 3 || os.Args[2] != "create" {
			usage()
		}
		cmdAdminCreate(os.Args[3:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: panel serve | panel migrate | panel admin create -username u -password p")
	os.Exit(2)
}

// runtime bundles the long-lived services the commands share.
type runtime struct {
	pool     *pgxpool.Pool
	q        *gen.Queries
	sessions *auth.Sessions
	keys     *auth.APIKeys
	auditLog *audit.Logger
	ca       *nodes.CA
	sealKey  []byte
}

// boot loads config, connects, migrates and builds the runtime.
func boot(log *slog.Logger) (config.Config, *runtime, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return cfg, nil, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return cfg, nil, err
	}

	q := gen.New(pool)

	sealKey, generated, err := seal.LoadOrCreate(cfg.DataDir, cfg.SecretsKey)
	if err != nil {
		pool.Close()
		return cfg, nil, fmt.Errorf("resolve secrets key: %w", err)
	}
	if generated {
		log.Warn("generated a random secrets key in the data dir; set QUARRY_SECRETS_KEY for anything durable")
	}
	box, err := seal.NewBox(sealKey)
	if err != nil {
		pool.Close()
		return cfg, nil, err
	}

	ca, created, err := nodes.LoadOrCreateCA(cfg.DataDir, box)
	if err != nil {
		pool.Close()
		return cfg, nil, fmt.Errorf("load CA: %w", err)
	}
	if created {
		log.Info("generated new panel CA", "dir", cfg.DataDir)
	}

	return cfg, &runtime{
		pool:     pool,
		q:        q,
		sessions: auth.NewSessions(q, cfg.SessionLifetime),
		keys:     auth.NewAPIKeys(q),
		auditLog: audit.New(q, log),
		ca:       ca,
		sealKey:  sealKey,
	}, nil
}

func cmdServe() {
	log := logging.New(os.Getenv("QUARRY_ENV") == "dev")

	cfg, rt, err := boot(log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	defer rt.pool.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Realtime hub + node channel registry + runner catalog
	hub := hub.New()
	chanMgr := nodes.NewChannelManager(log)
	catalog := runners.NewCatalog(rt.q, log)

	// Server orchestration (create/power/events)
	modrinth := marketplace.NewModrinth()
	consolePersister := consolelog.NewPersister(rt.pool, rt.q, log)
	serverSvc := servers.NewService(rt.pool, rt.q, hub, chanMgr, catalog, modrinth, consolePersister, log)
	go consolePersister.Run(ctx)

	// Keeps online-player snapshots fresh for watched servers.
	playerSvc := players.NewService(rt.q, chanMgr, hub, log)
	go playerSvc.Run(ctx)

	// HTTP API + node services
	nodeSvc := nodes.NewService(rt.pool, rt.q, rt.ca, rt.auditLog, log)
	apiServer := api.New(api.Deps{
		Cfg:      cfg,
		Log:      log,
		Pool:     rt.pool,
		Sessions: rt.sessions,
		Keys:     rt.keys,
		AuditLog: rt.auditLog,
		Nodes:    nodeSvc,
		Servers:  serverSvc,
		Channels: chanMgr,
		Catalog:  catalog,
		Modrinth: modrinth,
		Hub:      hub,
		SealKey:  rt.sealKey,
	})
	go func() {
		if err := apiServer.Run(ctx); err != nil {
			log.Error("http server failed", "err", err)
			stop()
		}
	}()

	// Node gRPC (mTLS); daemon events flow into the hub/server service.
	lis, gs, err := nodes.NewListener(cfg, rt.ca)
	if err != nil {
		log.Error("grpc listener failed", "err", err)
		os.Exit(1)
	}
	grpcServer := nodes.NewGRPCServer(nodeSvc, log, chanMgr, serverSvc.HandleEvent)
	nodesv1.RegisterNodeAgentServiceServer(gs, grpcServer)
	go func() {
		log.Info("node grpc listening", "addr", cfg.GRPCAddr)
		if err := gs.Serve(lis); err != nil {
			log.Error("grpc server failed", "err", err)
			stop()
		}
	}()

	go cleanupLoop(ctx, rt.q, log)

	<-ctx.Done()
	gs.GracefulStop()
	log.Info("panel stopped")
}

func cleanupLoop(ctx context.Context, q *gen.Queries, log *slog.Logger) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := q.DeleteExpiredSessions(ctx); err != nil {
				log.Warn("session cleanup failed", "err", err)
			}
			if _, err := q.PruneConsoleLines(ctx); err != nil {
				log.Warn("console prune failed", "err", err)
			}
		}
	}
}

func cmdMigrate() {
	log := logging.New(true)
	cfg, err := config.Load()
	if err != nil {
		log.Error("config failed", "err", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		log.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	log.Info("migrations applied")
}

// admin create -username X -password Y (or QUARRY_ADMIN_PASSWORD env to keep
// the password out of shell history).
func cmdAdminCreate(args []string) {
	log := logging.New(true)
	fs := flag.NewFlagSet("admin create", flag.ExitOnError)
	username := fs.String("username", "", "admin username")
	password := fs.String("password", os.Getenv("QUARRY_ADMIN_PASSWORD"), "admin password (prefer QUARRY_ADMIN_PASSWORD)")
	email := fs.String("email", "", "optional email")
	_ = fs.Parse(args)

	if *username == "" || *password == "" {
		log.Error("-username and -password (or QUARRY_ADMIN_PASSWORD) are required")
		os.Exit(2)
	}

	_, rt, err := boot(log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	defer rt.pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	role, err := rt.q.GetRoleByName(ctx, "admin")
	if err != nil {
		log.Error("admin role missing", "err", err)
		os.Exit(1)
	}
	hash, err := auth.HashPassword(*password)
	if err != nil {
		log.Error("password hashing failed", "err", err)
		os.Exit(1)
	}
	var emailPtr *string
	if *email != "" {
		emailPtr = email
	}
	user, err := rt.q.CreateUser(ctx, gen.CreateUserParams{
		ID: uuid.New(), Username: *username, Email: emailPtr, PasswordHash: hash, RoleID: role.ID,
	})
	if err != nil {
		log.Error("create admin failed (already exists?)", "err", err)
		os.Exit(1)
	}
	rt.auditLog.Record(ctx, audit.Entry{
		Actor: audit.ActorSystem, Action: "user.created",
		ResourceType: "user", ResourceID: user.ID.String(),
		Metadata: map[string]any{"username": user.Username, "via": "cli"},
	})
	log.Info("admin created", "username", user.Username)
}
