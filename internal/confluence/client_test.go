package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aravindarc/cfmd/internal/config"
)

func TestParsePageIDFromURL(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"12345", "12345", false},
		{"https://x.atlassian.net/wiki/spaces/ENG/pages/12345/Title", "12345", false},
		{"https://x.atlassian.net/wiki/pages/viewpage.action?pageId=999", "999", false},
		{"https://x.atlassian.net/wiki/no/ids/here", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParsePageIDFromURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSameHostAsBase(t *testing.T) {
	cfg := &config.Config{BaseURL: "https://ok.atlassian.net/wiki"}
	cl := New(cfg)
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"https://ok.atlassian.net/wiki/spaces/x", false},
		{"https://evil.atlassian.net/wiki/spaces/x", true},
		{"12345", false},                               // bare id
		{"/wiki/spaces/x/pages/12345/Title", false},     // relative
	}
	for _, c := range cases {
		err := cl.SameHostAsBase(c.in)
		if c.wantErr && err == nil {
			t.Errorf("%q: expected error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
		}
	}
}

// TestGetPage_MockServer verifies auth header, URL path, and response decoding
// against a local httptest server.
func TestGetPage_MockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/rest/api/content/") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			t.Errorf("missing auth header, got %q", auth)
		}
		if q := r.URL.Query().Get("expand"); !strings.Contains(q, "body.storage") {
			t.Errorf("missing expand: %q", q)
		}
		p := Page{
			ID: "42", Type: "page", Title: "Hi",
			Space:   Space{Key: "ENG"},
			Version: Version{Number: 3},
			Body: Body{Storage: StorageBody{
				Value:          "<p>hi</p>",
				Representation: "storage",
			}},
		}
		json.NewEncoder(w).Encode(&p)
	}))
	defer srv.Close()

	cl := New(&config.Config{BaseURL: srv.URL, Username: "u", Token: "t", TimeoutSeconds: 5})
	p, err := cl.GetPage(context.Background(), "42")
	if err != nil {
		t.Fatalf("%v", err)
	}
	if p.ID != "42" || p.Title != "Hi" || p.Version.Number != 3 {
		t.Errorf("decode: %+v", p)
	}
	if p.Body.Storage.Value != "<p>hi</p>" {
		t.Errorf("body: %q", p.Body.Storage.Value)
	}
}

func TestAuthError_Classification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"bad token"}`))
	}))
	defer srv.Close()
	cl := New(&config.Config{BaseURL: srv.URL, Username: "u", Token: "t", TimeoutSeconds: 5})
	_, err := cl.GetPage(context.Background(), "1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrAuth) {
		t.Errorf("wanted ErrAuth, got %v", err)
	}
}

func TestConflictError_Classification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"message":"version"}`))
	}))
	defer srv.Close()
	cl := New(&config.Config{BaseURL: srv.URL, Username: "u", Token: "t", TimeoutSeconds: 5})
	_, err := cl.UpdatePage(context.Background(), "1", &PageUpdate{})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("wanted ErrConflict, got %v", err)
	}
}

func TestNotFoundError_Classification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	cl := New(&config.Config{BaseURL: srv.URL, Username: "u", Token: "t", TimeoutSeconds: 5})
	_, err := cl.GetPage(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("wanted ErrNotFound, got %v", err)
	}
}
