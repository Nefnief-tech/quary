//go:build integration

package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"quarry.dev/quarry/internal/seal"
	"quarry.dev/quarry/panel/internal/api"
	"quarry.dev/quarry/panel/internal/audit"
	"quarry.dev/quarry/panel/internal/auth"
	"quarry.dev/quarry/panel/internal/config"
	"quarry.dev/quarry/panel/internal/db/gen"
	"quarry.dev/quarry/panel/internal/hub"
	"quarry.dev/quarry/panel/internal/nodes"
	"quarry.dev/quarry/panel/internal/runners"
	"quarry.dev/quarry/panel/internal/testutil"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	pool := testutil.StartPostgres(t)
	q := gen.New(pool)

	box, err := seal.NewBox(bytes.Repeat([]byte{1}, seal.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	ca, _, err := nodes.LoadOrCreateCA(t.TempDir(), box)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.Default()
	nodeSvc := nodes.NewService(pool, q, ca, audit.New(q, log), log)

	cfg := config.Config{Env: "dev", SessionLifetime: time.Hour}
	srv := api.New(api.Deps{
		Cfg:      cfg,
		Log:      log,
		Pool:     pool,
		Sessions: auth.NewSessions(q, cfg.SessionLifetime),
		Keys:     auth.NewAPIKeys(q),
		AuditLog: audit.New(q, log),
		Nodes:    nodeSvc,
		Catalog:  runners.NewCatalog(q, log),
		Hub:      hub.New(),
		SealKey:  bytes.Repeat([]byte{2}, seal.KeySize),
	})
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	return ts
}

// client talks to the test server with a cookie jar (sessions) and an
// optional API key (bearer).
type client struct {
	t      *testing.T
	base   string
	hc     *http.Client
	apiKey string
}

func newClient(t *testing.T, ts *httptest.Server) *client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &client{
		t:    t,
		base: ts.URL,
		hc:   &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}
}

// do performs a request and asserts the status code, returning the decoded
// JSON body (object, array, or nil).
func (c *client) do(method, path string, body any, wantStatus int) any {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, encodeBody(c.t, body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		c.t.Fatalf("%s %s: status = %d, want %d", method, path, resp.StatusCode, wantStatus)
	}
	var out any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// obj asserts v is a JSON object.
func obj(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected JSON object, got %T", v)
	}
	return m
}

func encodeBody(t *testing.T, body any) *bytes.Reader {
	t.Helper()
	if body == nil {
		return bytes.NewReader(nil)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(raw)
}

func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("response field %q missing or not a string: %v", key, m)
	}
	return v
}

// TestAuthFlowAndRBAC walks the whole Phase 0 permission story.
func TestAuthFlowAndRBAC(t *testing.T) {
	ts := newTestServer(t)

	admin := newClient(t, ts)

	// First registration becomes admin and starts logged in.
	resp := obj(t, admin.do("POST", "/api/v1/auth/register", map[string]string{
		"username": "root", "password": "super-secret-1",
	}, http.StatusCreated))
	if str(t, resp, "role_name") != "admin" {
		t.Fatal("first user must be admin")
	}
	me := obj(t, admin.do("GET", "/api/v1/auth/me", nil, http.StatusOK))
	if str(t, me, "username") != "root" {
		t.Fatal("session cookie from registration does not resolve")
	}

	// Second anonymous registration is rejected.
	anon := newClient(t, ts)
	anon.do("POST", "/api/v1/auth/register", map[string]string{
		"username": "sneaky", "password": "super-secret-2",
	}, http.StatusUnauthorized)

	// Anonymous access to protected routes is 401.
	anon.do("GET", "/api/v1/nodes", nil, http.StatusUnauthorized)

	// Admin creates a regular user; that user lacks node permissions.
	admin.do("POST", "/api/v1/users", map[string]string{
		"username": "bob", "password": "bob-password-1", "role": "user",
	}, http.StatusCreated)

	bob := newClient(t, ts)
	bob.do("POST", "/api/v1/auth/login", map[string]string{
		"username": "bob", "password": "bob-password-1",
	}, http.StatusOK)
	me = obj(t, bob.do("GET", "/api/v1/auth/me", nil, http.StatusOK))
	if str(t, me, "role_name") != "user" {
		t.Fatalf("bob role = %v, want user", me)
	}
	bob.do("GET", "/api/v1/nodes", nil, http.StatusForbidden)

	// Wrong password is rejected and does not create a session.
	bad := newClient(t, ts)
	bad.do("POST", "/api/v1/auth/login", map[string]string{
		"username": "bob", "password": "wrong-password",
	}, http.StatusUnauthorized)
	bad.do("GET", "/api/v1/auth/me", nil, http.StatusUnauthorized)

	// Admin creates a node; the enrollment token is returned exactly once.
	nodeResp := obj(t, admin.do("POST", "/api/v1/nodes", map[string]string{
		"name": "test-node-1", "description": "integration",
	}, http.StatusCreated))
	token := str(t, nodeResp, "enrollment_token")
	if token == "" {
		t.Fatal("no enrollment token returned")
	}
	nodesList := admin.do("GET", "/api/v1/nodes", nil, http.StatusOK)

	// Admin creates a scoped API key; node.read only.
	keyResp := obj(t, admin.do("POST", "/api/v1/keys", map[string]any{
		"name": "ci", "scopes": []string{"node.read"},
	}, http.StatusCreated))
	key := str(t, keyResp, "key")

	// The key can read nodes…
	reader := newClient(t, ts)
	reader.apiKey = key
	reader.do("GET", "/api/v1/nodes", nil, http.StatusOK)

	// …but not create them (scope does not cover node.create).
	reader.do("POST", "/api/v1/nodes", map[string]string{"name": "nope"}, http.StatusForbidden)

	// Node list visible via the key contains the created node.
	raw, err := json.Marshal(nodesList)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("test-node-1")) {
		t.Fatal("created node not in list")
	}

	// A scope exceeding the creator's power is rejected ("*" requires admin).
	bob.do("POST", "/api/v1/keys", map[string]any{
		"name": "escalate", "scopes": []string{"*"},
	}, http.StatusBadRequest)

	// Audit log records sensitive actions.
	auditList := admin.do("GET", "/api/v1/audit?limit=100", nil, http.StatusOK)
	rawAudit, _ := json.Marshal(auditList)
	for _, want := range []string{"auth.login", "node.created", "apikey.created", "user.created"} {
		if !bytes.Contains(rawAudit, []byte(want)) {
			t.Fatalf("audit log missing %q", want)
		}
	}

	// Logout kills the session.
	admin.do("POST", "/api/v1/auth/logout", nil, http.StatusOK)
	admin.do("GET", "/api/v1/auth/me", nil, http.StatusUnauthorized)
}
