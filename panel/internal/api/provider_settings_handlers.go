// Admin settings for tunnel providers (frp). The frps auth token is
// sealed at rest and never returned to the client.
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/settings"
)

// getExposeSettings returns the tunnel provider settings for the admin UI.
// The token is reported only as token_set.
func (s *Server) getExposeSettings(w http.ResponseWriter, r *http.Request) {
	if !rbacHas(Identity(r), "*") {
		apiError(w, http.StatusForbidden, "admin only")
		return
	}
	frp, err := s.settingsSvc.Frp(r.Context())
	if err != nil {
		// A corrupt row (old encoding) reads as unconfigured so the admin
		// can re-save.
		frp = settings.FrpConfig{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"frp": map[string]any{
			"enabled":     frp.Enabled,
			"server_addr": frp.ServerAddr,
			"server_port": frp.ServerPort,
			"token_set":   frp.Token != "",
		},
	})
}

type exposeSettingsRequest struct {
	Frp struct {
		Enabled    *bool   `json:"enabled"`
		ServerAddr *string `json:"server_addr"`
		ServerPort *int32  `json:"server_port"`
		Token      *string `json:"token"`
	} `json:"frp"`
}

// setExposeSettings updates the tunnel provider settings. An empty token
// keeps the stored one; enabling requires address and token to be set.
func (s *Server) setExposeSettings(w http.ResponseWriter, r *http.Request) {
	if !rbacHas(Identity(r), "*") {
		apiError(w, http.StatusForbidden, "admin only")
		return
	}
	req, ok := decode[exposeSettingsRequest](w, r)
	if !ok {
		return
	}
	frp, err := s.settingsSvc.Frp(r.Context())
	if err != nil {
		// Corrupt rows read as unconfigured; saving overwrites them.
		frp = settings.FrpConfig{}
	}
	if req.Frp.Enabled != nil {
		frp.Enabled = *req.Frp.Enabled
	}
	if req.Frp.ServerAddr != nil {
		frp.ServerAddr = strings.TrimSpace(*req.Frp.ServerAddr)
	}
	if req.Frp.ServerPort != nil {
		frp.ServerPort = *req.Frp.ServerPort
	}
	if req.Frp.Token != nil && *req.Frp.Token != "" {
		frp.Token = strings.TrimSpace(*req.Frp.Token)
	}
	if frp.Enabled && (frp.ServerAddr == "" || frp.Token == "") {
		apiError(w, http.StatusBadRequest, "enabling frp requires the frps address and auth token")
		return
	}
	if frp.ServerAddr != "" && frp.ServerPort == 0 {
		frp.ServerPort = 7000
	}
	if frp.ServerPort != 0 && (frp.ServerPort < 1 || frp.ServerPort > 65535) {
		apiError(w, http.StatusBadRequest, "frps port must be 1-65535")
		return
	}
	if err := s.settingsSvc.SetFrp(r.Context(), frp); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditLog.Record(r.Context(), audit.Entry{
		Actor: audit.ActorUser, Action: "settings.expose_updated",
		UserID: actorID(r), ResourceType: "settings", ResourceID: "expose",
		IP: clientIP(r), Metadata: map[string]any{"frp_enabled": frp.Enabled},
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"frp": map[string]any{
			"enabled":     frp.Enabled,
			"server_addr": frp.ServerAddr,
			"server_port": frp.ServerPort,
			"token_set":   frp.Token != "",
		},
	})
}

// frpForNode returns the frp provider config if it is usable (enabled +
// configured), normalizing the address for frpc (host only).
func (s *Server) frpForNode(ctx context.Context) (settings.FrpConfig, error) {
	frp, err := s.settingsSvc.Frp(ctx)
	if err != nil {
		return frp, err
	}
	if !frp.Enabled {
		return frp, errors.New("frp is disabled in the admin settings")
	}
	if frp.ServerAddr == "" || frp.Token == "" {
		return frp, errors.New("frp is not fully configured in the admin settings")
	}
	return frp, nil
}
