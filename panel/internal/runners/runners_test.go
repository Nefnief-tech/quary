package runners

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"quarry.dev/quarry/internal/ssrf"
)

// testClient returns an SSRF-guarded client that may talk to httptest
// (loopback) servers.
func testClient(t *testing.T) *http.Client {
	t.Helper()
	g, err := ssrf.NewGuard([]string{"127.0.0.0/8", "::1/128"})
	if err != nil {
		t.Fatal(err)
	}
	return g.HTTPClient()
}

func TestJavaForMC(t *testing.T) {
	cases := map[string]int{
		"26.3":   25,
		"26.1.2": 25,
		"1.21.4": 21,
		"1.20.5": 21,
		"1.20.4": 17,
		"1.18.2": 17,
		"1.18":   17,
		"1.17.1": 17,
		"1.16.5": 8,
		"1.8.9":  8,
		"borked": 8, // unknown → conservative oldest
	}
	for mc, want := range cases {
		if got := JavaForMC(mc); got != want {
			t.Errorf("JavaForMC(%q) = %d, want %d", mc, got, want)
		}
	}
}

func TestImageForJava(t *testing.T) {
	if got := ImageForJava(21); got != "eclipse-temurin:21-jre-noble" {
		t.Fatalf("ImageForJava(21) = %q", got)
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess("1.9", "1.10") {
		t.Error("1.9 must sort before 1.10")
	}
	if !versionLess("1.20.4", "1.20.5") {
		t.Error("1.20.4 must sort before 1.20.5")
	}
	if versionLess("1.21.1", "1.21.1") {
		t.Error("equal versions are not less")
	}
}

func TestVanillaListAndResolve(t *testing.T) {
	var ts *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{
		  "latest": {"release": "1.21.1"},
		  "versions": [
		    {"id": "1.21.1", "type": "release", "url": "%s/meta/1.21.1.json",
		     "releaseTime": "2024-08-08T15:24:08+00:00"},
		    {"id": "24w33a", "type": "snapshot", "url": "%s/meta/24w33a.json",
		     "releaseTime": "2024-08-15T12:00:00+00:00"},
		    {"id": "b1.7.3", "type": "old_beta", "url": "%s/meta/b1.7.3.json",
		     "releaseTime": "2011-07-08T00:00:00+00:00"}
		  ]
		}`, ts.URL, ts.URL, ts.URL)
	})
	mux.HandleFunc("/meta/1.21.1.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{
		  "downloads": {
		    "server": {"url": "%s/server/1.21.1.jar", "sha1": "abc123", "size": 50000}
		  }
		}`, ts.URL)
	})
	mux.HandleFunc("/server/1.21.1.jar", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("jarbytes"))
	})
	ts = httptest.NewServer(mux)
	defer ts.Close()

	wantJarURL := ts.URL + "/server/1.21.1.jar"

	v := NewVanillaWithBase(ts.URL+"/manifest.json", testClient(t))
	ctx := context.Background()

	versions, err := v.ListVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 {
		t.Fatalf("got %d versions, want 3", len(versions))
	}
	stable := map[string]bool{}
	for _, vv := range versions {
		stable[vv.MCVersion] = vv.Stable
	}
	if !stable["1.21.1"] || stable["24w33a"] || stable["b1.7.3"] {
		t.Fatalf("stability flags wrong: %v", stable)
	}

	artifact, err := v.Resolve(ctx, "1.21.1")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.URL != wantJarURL || artifact.SHA1 != "abc123" || artifact.Name != "server.jar" {
		t.Fatalf("artifact = %+v", artifact)
	}
	if _, err := v.Resolve(ctx, "9.9.9"); err == nil {
		t.Fatal("unknown version must fail")
	}
}

func TestPaperListAndResolveLatestBuild(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/projects/paper", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"project":{"id":"paper"},"versions":{
		  "1.21":["1.21.11-rc3","1.21.8","1.21.1"],
		  "1.20":["1.20.6","1.20.1"]
		}}`))
	})
	mux.HandleFunc("/projects/paper/versions/1.21.8/builds/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":60,"channel":"STABLE","downloads":{"server:default":{
		  "name":"paper-1.21.8-60.jar",
		  "checksums":{"sha256":"abc256"},
		  "url":"https://fill-data.example/paper-1.21.8-60.jar"
		}}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := NewPaperWithBase(ts.URL, testClient(t))
	ctx := context.Background()

	versions, err := p.ListVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stable := map[string]bool{}
	for _, v := range versions {
		stable[v.MCVersion] = v.Stable
	}
	if !stable["1.21.8"] || stable["1.21.11-rc3"] {
		t.Fatalf("stability flags wrong: %v", stable)
	}

	artifact, err := p.Resolve(ctx, "1.21.8")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.SHA256 != "abc256" || artifact.URL != "https://fill-data.example/paper-1.21.8-60.jar" {
		t.Fatalf("artifact = %+v", artifact)
	}
	if p.PluginKind() != Plugins {
		t.Fatal("paper must load plugins")
	}
}

func TestPaperResolveFailsWithoutDownload(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/projects/paper/versions/9.9.9/builds/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1,"downloads":{}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := NewPaperWithBase(ts.URL, testClient(t))
	if _, err := p.Resolve(context.Background(), "9.9.9"); err == nil {
		t.Fatal("missing download must fail")
	}
}

func TestFabricResolveComputesSHA256(t *testing.T) {
	launcher := []byte("fabric-launcher-bytes")
	sum := sha256.Sum256(launcher)

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/versions/game", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"version":"1.21.1","stable":true},{"version":"1.21.1-pre1","stable":false}]`))
	})
	mux.HandleFunc("/v2/versions/loader/1.21.1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
		  {"loader":{"version":"0.16.5","stable":false}},
		  {"loader":{"version":"0.16.4","stable":true}}
		]`))
	})
	mux.HandleFunc("/v2/versions/installer", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"version":"1.0.1","stable":true},{"version":"1.1.0","stable":false}]`))
	})
	mux.HandleFunc("/v2/versions/loader/1.21.1/0.16.4/1.0.1/server/jar", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(launcher)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	f := NewFabricWithBase(ts.URL+"/v2", testClient(t))
	ctx := context.Background()

	versions, err := f.ListVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || !versions[0].Stable {
		t.Fatalf("versions = %+v", versions)
	}

	artifact, err := f.Resolve(ctx, "1.21.1")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %q, want %q", artifact.SHA256, hex.EncodeToString(sum[:]))
	}
	if artifact.URL != ts.URL+"/v2/versions/loader/1.21.1/0.16.4/1.0.1/server/jar" {
		t.Fatalf("url = %q", artifact.URL)
	}
	if f.PluginKind() != Mods {
		t.Fatal("fabric must load mods")
	}
}
