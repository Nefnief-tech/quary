//go:build integration

package servers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"quarry.dev/quarry/daemon/internal/runtime"
	"quarry.dev/quarry/daemon/internal/servers"
	nodesv1 "quarry.dev/quarry/proto/gen/go/quarry/node/v1"
)

// fakeEmitter records events with waitable signals.
type fakeEmitter struct {
	mu      sync.Mutex
	console chan string
	status  chan string
}

func newFakeEmitter() *fakeEmitter {
	return &fakeEmitter{console: make(chan string, 256), status: make(chan string, 64)}
}

func (e *fakeEmitter) EmitConsole(id uuid.UUID, line string) {
	select {
	case e.console <- line:
	default:
	}
}

func (e *fakeEmitter) EmitStats(id uuid.UUID, s *nodesv1.ServerStats)                  {}
func (e *fakeEmitter) EmitInstallProgress(id uuid.UUID, s string, f float64, d string) {}

func (e *fakeEmitter) EmitStatus(id uuid.UUID, status, detail string) {
	select {
	case e.status <- status:
	default:
	}
}

// waitForStatus drains statuses until want arrives.
func waitForStatus(t *testing.T, e *fakeEmitter, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case got := <-e.status:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("status %q not observed within %s", want, timeout)
		}
	}
}

// waitForConsoleLine drains console lines until prefix matches.
func waitForConsoleLine(t *testing.T, e *fakeEmitter, prefix string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case line := <-e.console:
			if len(line) >= len(prefix) && line[:len(prefix)] == prefix {
				return line
			}
		case <-deadline:
			t.Fatalf("console line %q not observed within %s", prefix, timeout)
		}
	}
}

// TestLifecycleWithDocker exercises the full server lifecycle against the
// real Docker daemon: install (download + checksum + config files), start
// with hardened container, console round-trip, graceful stop, files.
// It uses a busybox stub "server" instead of real Minecraft to stay fast
// and deterministic; the runners themselves are covered by fixture tests
// and a live Paper smoke test.
func TestLifecycleWithDocker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Artifact server (loopback; allowlisted for this test).
	jar := []byte("fake-mc-server-jar-bytes")
	sum := sha256.Sum256(jar)
	artifactSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(jar)
	}))
	defer artifactSrv.Close()

	rt, err := runtime.NewDocker(os.Getuid())
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	log := slog.Default()
	mgr, err := servers.NewManager(servers.Config{
		ServersRoot:        t.TempDir(),
		ArtifactAllowCIDRs: []string{"127.0.0.0/8", "::1/128"},
		StatsInterval:      time.Second,
	}, rt, log)
	if err != nil {
		t.Fatal(err)
	}

	emit := newFakeEmitter()
	mgr.SetEmitter(emit)
	serverID := uuid.New()
	// Grab a free host port to avoid clashes with parallel runs.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bindPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	// --- Install ---
	err = mgr.Install(ctx, &nodesv1.InstallServer{
		ServerId:       serverID.String(),
		Runner:         "stub",
		McVersion:      "0.0.0",
		ArtifactUrl:    artifactSrv.URL + "/server.jar",
		ArtifactSha256: hex.EncodeToString(sum[:]),
		ArtifactName:   "server.jar",
		Image:          "busybox:latest",
		Launch:         []string{"sh", "-c", "echo stub-started; cat"},
		MemoryMb:       128,
		BindIp:         "0.0.0.0",
		BindPort:       int32(bindPort),
	}, emit)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	waitForStatus(t, emit, "offline", 5*time.Second) // install complete

	// Installed files exist.
	dir, err := mgr.ServerDir(serverID, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"server.jar", "eula.txt", "server.properties"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s missing after install: %v", name, err)
		}
	}

	// Corrupted checksums must be rejected.
	badSum := sha256.Sum256([]byte("other-bytes"))
	err = mgr.Install(ctx, &nodesv1.InstallServer{
		ServerId:       serverID.String(),
		ArtifactUrl:    artifactSrv.URL + "/server.jar",
		ArtifactSha256: hex.EncodeToString(badSum[:]),
		ArtifactName:   "server.jar",
		BindPort:       int32(bindPort),
	}, emit)
	if err == nil {
		t.Fatal("install with wrong checksum must fail")
	}

	// --- Start + console round-trip ---
	err = mgr.Start(ctx, &nodesv1.StartServer{
		ServerId:   serverID.String(),
		Image:      "busybox:latest",
		Launch:     []string{"sh", "-c", "echo stub-started; cat"},
		MemoryMb:   128,
		CpuPercent: 100,
		BindIp:     "0.0.0.0",
		BindPort:   int32(bindPort),
	}, emit)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	started := waitForConsoleLine(t, emit, "stub-started", 30*time.Second)
	_ = started

	if err := mgr.SendCommand(&nodesv1.SendCommand{ServerId: serverID.String(), Input: "ping-echo-test"}); err != nil {
		t.Fatalf("send command: %v", err)
	}
	got := waitForConsoleLine(t, emit, "ping-echo-test", 10*time.Second)
	_ = got

	// Stats sampling works while running.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := rt.StatsOneShot(ctx, serverID); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// --- File manager operations ---
	entries, err := mgr.ListFiles(serverID, ".")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name] = true
	}
	if !found["server.jar"] || !found["eula.txt"] {
		t.Fatalf("listing incomplete: %v", found)
	}
	if err := mgr.WriteFile(serverID, "config/test.yml", []byte("hello: world"), true); err != nil {
		t.Fatalf("write file: %v", err)
	}
	content, err := mgr.ReadFile(serverID, "config/test.yml", 1024)
	if err != nil || string(content) != "hello: world" {
		t.Fatalf("read file: %v %q", err, content)
	}
	// Path traversal is refused.
	if _, err := mgr.ReadFile(serverID, "../../../etc/passwd", 1024); err == nil {
		t.Fatal("traversal path accepted")
	}

	// --- Graceful stop (console stop is a no-op for the stub; escalates) ---
	go func() {
		stopCtx, stopCancel := context.WithTimeout(ctx, 60*time.Second)
		defer stopCancel()
		_ = mgr.Stop(stopCtx, &nodesv1.StopServer{
			ServerId: serverID.String(), TimeoutSeconds: 3,
		}, emit)
	}()
	waitForStatus(t, emit, "offline", 45*time.Second)

	// Removal may lag the offline event slightly; poll briefly.
	stateDeadline := time.Now().Add(15 * time.Second)
	for {
		state, _ := rt.State(ctx, serverID)
		if state == "" {
			break
		}
		if time.Now().After(stateDeadline) {
			t.Fatalf("container still present after stop: %q", state)
		}
		time.Sleep(250 * time.Millisecond)
	}

	// --- Delete wipes the data directory ---
	if err := mgr.DeletePath(serverID, ".", true); err != nil {
		t.Fatalf("delete server data: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("server data dir still present after delete: %v", err)
	}
}
