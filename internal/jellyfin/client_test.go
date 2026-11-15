package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientUsesFullMediaBrowserAuthorization(t *testing.T) {
	const token = "test-token-value"
	var auth, rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, rawQuery = r.Header.Get("Authorization"), r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Id":"server"}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, token, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var got SystemInfo
	if err := c.Get(context.Background(), "/System/Info", url.Values{"limit": {"1"}}, &got); err != nil {
		t.Fatal(err)
	}
	want := `MediaBrowser Client="SlideBolt", Device="SlideBolt", DeviceId="slidebolt-jellyfin", Version="1", Token="` + token + `"`
	if auth != want {
		t.Fatalf("authorization mismatch: %q", auth)
	}
	if strings.Contains(rawQuery, token) {
		t.Fatal("ordinary API request leaked token in URL")
	}
}

func TestErrorsDoNotExposeToken(t *testing.T) {
	const token = "never-print-this"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, token, http.StatusUnauthorized) }))
	defer srv.Close()
	c, err := New(srv.URL, token, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var out SystemInfo
	err = c.Get(context.Background(), "/System/Info", nil, &out)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("error exposed token")
	}
}

func TestDirectStreamURL(t *testing.T) {
	c, err := New("https://jellyfin.example.com/base", "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.StreamURL("item id", "Audio", "source id"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/base/Audio/item id/stream" {
		t.Fatalf("path = %q", u.Path)
	}
	if u.Query().Get("Static") != "true" || u.Query().Get("MediaSourceId") != "source id" || u.Query().Get("api_key") != "secret" {
		t.Fatalf("query = %v", u.Query())
	}
}

func TestMediaFoldersDecodeJellyfinIDField(t *testing.T) {
	var libraries Libraries
	if err := json.Unmarshal([]byte(`{"Items":[{"Id":"library-id","Name":"Movies","CollectionType":"movies"}]}`), &libraries); err != nil {
		t.Fatal(err)
	}
	if len(libraries.Items) != 1 || libraries.Items[0].ID != "library-id" {
		t.Fatalf("libraries = %+v", libraries)
	}
}
