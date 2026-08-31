package server

import (
	"freebuff-proxy/backend/internal/dashboard"
	"net/http"
)

// registerAdminRoutes mounts all admin HTTP routes from the AdminRoutes table.
func (s *Server) registerAdminRoutes(mux *http.ServeMux) {
	for _, r := range dashboard.AdminRoutes {
		h := s.adminHandler(r)
		if r.Method == http.MethodPost {
			h = s.adminCSRF(h)
		}
		switch r.Auth {
		case dashboard.AuthNone:
			// No auth wrapper: login page, logout, static assets.
		case dashboard.AuthDashboard:
			h = s.dashboardAuth(h)
		case dashboard.AuthSensitive:
			h = s.adminSensitive(h)
			h = s.dashboardAuth(h)
		case dashboard.AuthAdminToken:
			h = s.adminSensitive(h)
			h = s.requireAdminToken(h)
		default:
			panic("server: unknown admin auth level " + r.Auth)
		}
		mux.Handle(r.Method+" "+r.Path, h)
	}
}

// adminHandler resolves one AdminRoutes row to its handler implementation,
// before auth wrapping.
func (s *Server) adminHandler(r dashboard.AdminRoute) http.Handler {
	switch r.Method + " " + r.Path {
	case "POST /admin/reload":
		return http.HandlerFunc(s.handleReload)
	case "GET /admin/login":
		return http.HandlerFunc(s.handleAdminLogin)
	case "POST /admin/login":
		return http.HandlerFunc(s.handleAdminLogin)
	case "GET /admin/logout":
		return http.HandlerFunc(s.handleAdminLogout)
	case "POST /admin/logout":
		return http.HandlerFunc(s.handleAdminLogout)
	case "GET /admin/api/overview":
		return s.dash.APIHandler("overview")
	case "GET /admin/api/tokens":
		return s.dash.APIHandler("tokens")
	case "GET /admin/api/models":
		return s.dash.APIHandler("models")
	case "GET /admin/api/traces":
		return s.dash.APIHandler("traces")
	case "GET /admin/api/setup":
		return s.dash.APIHandler("setup")
	case "GET /admin/api/config":
		return s.dash.APIHandler("config")
	case "GET /admin/api/config/meta":
		return http.HandlerFunc(s.dash.APIConfigMeta)
	case "GET /admin/api/logs":
		return s.dash.APIHandler("logs")
	case "GET /admin/api/metrics":
		return s.dash.APIHandler("metrics")
	case "GET /admin/api/version":
		return http.HandlerFunc(s.dash.APIVersion)
	case "GET /admin/api/upstream-drift":
		return s.dash.APIHandler("upstream")
	case "GET /admin/api/auth/status":
		return http.HandlerFunc(s.handleAdminAuthStatus)
	case "GET /admin", "GET /admin/", "GET /admin/tokens", "GET /admin/models", "GET /admin/traces",
		"GET /admin/setup", "GET /admin/playground", "GET /admin/config", "GET /admin/logs", "GET /admin/metrics":
		return http.HandlerFunc(s.dash.ServeSPA)
	case "POST /admin/playground/chat":
		return http.HandlerFunc(s.handlePlaygroundChat)
	case "POST /admin/login/start":
		return http.HandlerFunc(s.handleLoginStart)
	case "GET /admin/login/status":
		return http.HandlerFunc(s.handleLoginStatus)
	case "POST /admin/config":
		return http.HandlerFunc(s.handleConfigSave)
	case "POST /admin/tokens/{id}/unlock":
		return http.HandlerFunc(s.handleTokenUnlock)
	case "POST /admin/tokens/{id}/lock":
		return http.HandlerFunc(s.handleTokenLock)
	case "POST /admin/tokens/{id}/unlock-lock":
		return http.HandlerFunc(s.handleTokenUnlockLock)
	case "POST /admin/bridge-tokens/{key}/lock":
		return http.HandlerFunc(s.handleBridgeTokenLock)
	case "POST /admin/bridge-tokens/{key}/unlock":
		return http.HandlerFunc(s.handleBridgeTokenUnlock)
	case "POST /admin/tokens/{id}/finish":
		return http.HandlerFunc(s.handleTokenFinish)
	case "POST /admin/tokens/{id}/test":
		return http.HandlerFunc(s.handleTokenTest)
	case "POST /admin/tokens/{id}/session":
		return http.HandlerFunc(s.handleTokenSpawnSession)
	case "POST /admin/tokens/test-all":
		return http.HandlerFunc(s.handleTokenTestAll)
	case "POST /admin/tokens/add":
		return http.HandlerFunc(s.handleTokenAdd)
	case "POST /admin/tokens/remove":
		return http.HandlerFunc(s.handleTokenRemove)
	case "POST /admin/tokens/remove-specific":
		return http.HandlerFunc(s.handleTokenRemoveSpecific)
	case "GET /admin/tokens/list":
		return http.HandlerFunc(s.handleTokenList)
	case "POST /admin/mode":
		return http.HandlerFunc(s.handleModeSwitch)
	case "POST /admin/diag":
		return http.HandlerFunc(s.handleDiag)
	case "POST /admin/api/change-password":
		return http.HandlerFunc(s.handleAdminChangePassword)
	case "POST /admin/smoke":
		return http.HandlerFunc(s.handleSmoke)
	case "GET /admin/assets/":
		return noDirListing(http.StripPrefix("/admin/assets/", http.FileServerFS(mustSubFS(dashboard.DistFS(), "assets"))))
	default:
		panic("server: no admin handler for " + r.Method + " " + r.Path)
	}
}