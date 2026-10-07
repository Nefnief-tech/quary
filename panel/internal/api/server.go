// Package api is the panel's REST API: one stdlib mux, one authn middleware,
// one authz middleware. Handlers stay thin; permission checks are declared
// per route in router.go.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/auth"
	"quarry.dev/quarry/panel/internal/config"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/hub"
	"quarry.dev/quarry/panel/internal/marketplace"
	"quarry.dev/quarry/panel/internal/nodes"
	"quarry.dev/quarry/panel/internal/router"
	"quarry.dev/quarry/panel/internal/runners"
	"quarry.dev/quarry/panel/internal/servers"
	"quarry.dev/quarry/panel/internal/settings"
)

// Server wires the API's dependencies together.
type Server struct {
	cfg         config.Config
	log         *slog.Logger
	pool        *pgxpool.Pool
	q           *gen.Queries
	sessions    *auth.Sessions
	keys        *auth.APIKeys
	auditLog    *audit.Logger
	nodeSvc     *nodes.Service
	serverSvc   *servers.Service
	channels    *nodes.ChannelManager
	catalog     *runners.Catalog
	modrinth    *marketplace.Modrinth
	hub         *hub.Hub
	sealKey     []byte
	limiter     *auth.LoginLimiter
	settingsSvc *settings.Service
	routerSvc   *router.Service
}

// Deps bundles everything the API needs.
type Deps struct {
	Cfg         config.Config
	Log         *slog.Logger
	Pool        *pgxpool.Pool
	Sessions    *auth.Sessions
	Keys        *auth.APIKeys
	AuditLog    *audit.Logger
	Nodes       *nodes.Service
	Servers     *servers.Service
	Channels    *nodes.ChannelManager
	Catalog     *runners.Catalog
	Modrinth    *marketplace.Modrinth
	Hub         *hub.Hub
	SealKey     []byte
	SettingsSvc *settings.Service
	RouterSvc   *router.Service
}

func New(d Deps) *Server {
	return &Server{
		cfg:         d.Cfg,
		log:         d.Log,
		pool:        d.Pool,
		q:           gen.New(d.Pool),
		sessions:    d.Sessions,
		keys:        d.Keys,
		auditLog:    d.AuditLog,
		nodeSvc:     d.Nodes,
		serverSvc:   d.Servers,
		channels:    d.Channels,
		catalog:     d.Catalog,
		modrinth:    d.Modrinth,
		hub:         d.Hub,
		sealKey:     d.SealKey,
		limiter:     auth.NewLoginLimiter(15*time.Second, 5),
		settingsSvc: d.SettingsSvc,
		routerSvc:   d.RouterSvc,
	}
}

// Run serves HTTP until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           s.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening", "addr", s.cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return errors.Join(ctx.Err(), err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
