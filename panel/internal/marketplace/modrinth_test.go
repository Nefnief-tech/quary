package marketplace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"quarry.dev/quarry/internal/ssrf"
)

func testClient(t *testing.T) *http.Client {
	t.Helper()
	g, err := ssrf.NewGuard([]string{"127.0.0.0/8", "::1/128"})
	if err != nil {
		t.Fatal(err)
	}
	return g.HTTPClient()
}

func TestModrinthSearchFacetsAndHits(t *testing.T) {
	var gotQuery, gotFacets string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotQuery = q.Get("query")
		gotFacets = q.Get("facets")
		_, _ = w.Write([]byte(`{"hits":[
		  {"project_id":"p1","slug":"essentialsx","title":"EssentialsX","author":"EssentialsX team",
		   "description":" essentials","downloads":3000000,"icon_url":"", "server_side":"required"}
		],"total_hits":42}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	m := NewModrinthWithBase(ts.URL, testClient(t))
	hits, total, err := m.Search(context.Background(), "essentials", "1.21.8", "paper", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 42 || len(hits) != 1 || hits[0].ProjectID != "p1" {
		t.Fatalf("hits = %+v, total = %d", hits, total)
	}
	if gotQuery != "essentials" {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotFacets != `[["project_type:plugin"],["versions:1.21.8"],["categories:paper"]]` {
		t.Fatalf("facets = %q", gotFacets)
	}
}

func TestModrinthResolvePrefersPrimaryFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/project/p1/version", func(w http.ResponseWriter, r *http.Request) {
		gv := r.URL.Query().Get("game_versions")
		ld := r.URL.Query().Get("loaders")
		if gv != `["1.21.8"]` || ld != `["paper"]` {
			t.Errorf("version filters = %q, %q", gv, ld)
		}
		_, _ = w.Write([]byte(`[
		  {"version_number":"2.1.0","files":[
		    {"url":"https://cdn.example/essentials-2.1.0.jar","filename":"EssentialsX-2.1.0.jar",
		     "primary":true,"hashes":{"sha1":"aaa","sha512":"bbb"}},
		    {"url":"https://cdn.example/other.jar","filename":"other.jar",
		     "primary":false,"hashes":{"sha1":"ccc","sha512":"ddd"}}
		  ]},
		  {"version_number":"2.0.9","files":[]}
		]`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	m := NewModrinthWithBase(ts.URL, testClient(t))
	dl, err := m.Resolve(context.Background(), "p1", "1.21.8", "paper")
	if err != nil {
		t.Fatal(err)
	}
	if dl.Filename != "EssentialsX-2.1.0.jar" || dl.SHA512 != "bbb" || dl.Version != "2.1.0" {
		t.Fatalf("download = %+v", dl)
	}
}

func TestModrinthResolveNoCompatibleVersion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/project/p1/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	m := NewModrinthWithBase(ts.URL, testClient(t))
	if _, err := m.Resolve(context.Background(), "p1", "9.9.9", "paper"); err == nil {
		t.Fatal("no compatible version must fail")
	}
}
