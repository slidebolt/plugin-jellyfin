package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slidebolt/plugin-jellyfin/internal/jellyfin"
	contract "github.com/slidebolt/sb-contract"
	domain "github.com/slidebolt/sb-domain"
	media "github.com/slidebolt/sb-media-sdk"
	storage "github.com/slidebolt/sb-storage-sdk"
)

func TestManifestIsSelfDescribing(t *testing.T) {
	h := New().Hello()
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
	if h.ID != PluginID || h.Setup.Mode != contract.SetupSelectable {
		t.Fatalf("unexpected hello: %+v", h)
	}
	if len(h.Setup.Resources) != 1 || h.Setup.Resources[0].ID != jellyfinServerResource {
		t.Fatalf("resources = %+v", h.Setup.Resources)
	}
	fields := map[string]contract.SetupField{}
	for _, f := range h.Setup.Fields {
		fields[f.Key] = f
	}
	if !fields["token"].Sensitive || fields["token"].Control != contract.SetupSecret {
		t.Fatal("token is not declared as a sensitive secret")
	}
}

func TestMediaItemNormalization(t *testing.T) {
	item := jellyfin.Item{ID: "episode-id", ParentID: "season-id", Name: "Pilot", Type: "Episode", SeriesName: "Example Show", ParentIndexNumber: 1, IndexNumber: 2, RunTimeTicks: 900000, ProductionYear: 2020, ImageTags: map[string]string{"Primary": "x"}, BackdropImageTags: []string{"y"}}
	got := mediaItem("server", "library", item)
	if got.Kind != "episode" || !got.Playable || got.DurationMS != 90 || got.Parent == nil || got.Parent.ItemID != "season-id" {
		t.Fatalf("item = %+v", got)
	}
	if len(got.Assets) != 2 {
		t.Fatalf("assets = %+v", got.Assets)
	}
	if string(got.Metadata["parentTitle"]) != `"Example Show"` {
		t.Fatalf("metadata = %s", got.Metadata["parentTitle"])
	}
}

func TestBrowseArtworkAndResolve(t *testing.T) {
	const token = "resolver-secret"
	var listQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/Items":
			listQuery = r.URL.Query()
			writeJSON(w, jellyfin.Items{Items: []jellyfin.Item{{ID: "item-1", Name: "Example", Type: "Movie", MediaType: "Video", RunTimeTicks: 20000, ImageTags: map[string]string{"Primary": "tag"}, MediaSources: []jellyfin.MediaSource{{ID: "source-1", Container: "mkv"}}}}, TotalRecordCount: 2})
		case r.URL.Path == "/Items/item-1":
			writeJSON(w, jellyfin.Item{ID: "item-1", Name: "Example", Type: "Movie", MediaType: "Video", ImageTags: map[string]string{"Primary": "tag"}, BackdropImageTags: []string{"backdrop"}, MediaSources: []jellyfin.MediaSource{{ID: "source-1", Container: "mkv"}}})
		case r.URL.Path == "/Items/item-1/Images/Primary":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := jellyfin.New(srv.URL, token, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{client: c, server: &serverState{ID: "server"}}
	page := a.Browse(media.CatalogRequest{ServerID: "server", LibraryID: "library", ParentID: "season", Query: "exam", Kind: "movie", Sort: "title", Limit: 1})
	if page.Error != "" || len(page.Items) != 1 || page.NextCursor != "1" {
		t.Fatalf("page = %+v", page)
	}
	if listQuery.Get("ParentId") != "season" || listQuery.Get("Recursive") != "false" || listQuery.Get("SearchTerm") != "exam" || listQuery.Get("IncludeItemTypes") != "Movie" {
		t.Fatalf("query = %v", listQuery)
	}
	art := a.Artwork(media.ArtworkRequest{ServerID: "server", ItemID: "item-1", Kind: "thumbnail"})
	if art.Error != "" || art.ContentType != "image/jpeg" || string(art.Data) != "jpeg" {
		t.Fatalf("art = %+v", art)
	}
	reply := a.Resolve(media.ResolveRequest{Source: domain.PlayableSource{Item: &domain.MediaItemRef{Provider: PluginID, ServerID: "server", ItemID: "item-1"}}})
	if reply.Error != "" || reply.Kind != "video" || reply.Format != "mkv" {
		t.Fatalf("resolve = %+v", reply)
	}
	u, _ := url.Parse(reply.URL)
	if u.Query().Get("api_key") != token || u.Query().Get("Static") != "true" || u.Query().Get("MediaSourceId") != "source-1" {
		t.Fatalf("stream query = %v", u.Query())
	}
}

func TestPlayerCommandsAndSync(t *testing.T) {
	var mu sync.Mutex
	var posts []requestRecord
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			posts = append(posts, requestRecord{Path: r.URL.Path, Query: r.URL.Query(), Body: body})
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch r.URL.Path {
		case "/System/Info":
			writeJSON(w, jellyfin.SystemInfo{ID: "server", Name: "Example", Version: "1"})
		case "/Library/MediaFolders":
			writeJSON(w, jellyfin.Libraries{})
		case "/Sessions":
			writeJSON(w, []jellyfin.Session{{ID: "session", DeviceID: "device", DeviceName: "Browser", SupportsRemoteControl: true, PlayState: jellyfin.PlayState{VolumeLevel: 50}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := jellyfin.New(srv.URL, "test-secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryStorage()
	a := &App{client: c, store: store, config: config{Refresh: time.Second}, server: &serverState{ID: "server", Players: map[string]string{"player-device": "session"}, Entities: map[string]domain.EntityKey{}}}
	commands := []any{
		domain.MediaPlay{Source: &domain.PlayableSource{Item: &domain.MediaItemRef{Provider: PluginID, ServerID: "server", ItemID: "item"}}, PositionMS: 12},
		domain.MediaPlay{}, domain.MediaPause{}, domain.MediaStop{}, domain.MediaSeek{PositionMS: 34}, domain.MediaNextTrack{}, domain.MediaPreviousTrack{}, domain.MediaSetVolume{VolumeLevel: 1.4}, domain.MediaMute{Mute: true}, domain.MediaMute{Mute: false},
	}
	for _, command := range commands {
		if err := a.controlPlayer("player-device", command); err != nil {
			t.Fatalf("%T: %v", command, err)
		}
	}
	mu.Lock()
	got := append([]requestRecord(nil), posts...)
	mu.Unlock()
	if len(got) != len(commands) {
		t.Fatalf("posts = %d, want %d", len(got), len(commands))
	}
	if got[0].Path != "/Sessions/session/Playing" || got[0].Query.Get("playCommand") != "PlayNow" || got[0].Query.Get("itemIds") != "item" || got[0].Query.Get("startPositionTicks") != "120000" {
		t.Fatalf("play = %+v", got[0])
	}
	if got[4].Path != "/Sessions/session/Playing/Seek" || got[4].Query.Get("seekPositionTicks") != "340000" {
		t.Fatalf("seek = %+v", got[4])
	}
	if got[7].Body["Name"] != "SetVolume" {
		t.Fatalf("volume = %+v", got[7])
	}
	args := got[7].Body["Arguments"].(map[string]any)
	if args["Volume"] != "100" {
		t.Fatalf("volume args = %+v", args)
	}
	if got[8].Body["Name"] != "Mute" || got[9].Body["Name"] != "Unmute" {
		t.Fatalf("mute = %+v %+v", got[8], got[9])
	}
	entries, _ := store.Search("*")
	joined, _ := json.Marshal(entries)
	if strings.Contains(string(joined), "test-secret") {
		t.Fatal("token leaked into stored entities")
	}
}

type requestRecord struct {
	Path  string
	Query url.Values
	Body  map[string]any
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

type memoryStorage struct {
	mu     sync.Mutex
	values map[string]json.RawMessage
}

func newMemoryStorage() *memoryStorage { return &memoryStorage{values: map[string]json.RawMessage{}} }
func (s *memoryStorage) Save(v storage.Keyed) error {
	b, err := json.Marshal(v)
	if err == nil {
		s.mu.Lock()
		s.values[v.Key()] = b
		s.mu.Unlock()
	}
	return err
}
func (s *memoryStorage) Get(k storage.Keyed) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.values[k.Key()]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), b...), nil
}
func (s *memoryStorage) Delete(k storage.Keyed) error {
	s.mu.Lock()
	delete(s.values, k.Key())
	s.mu.Unlock()
	return nil
}
func (s *memoryStorage) Search(pattern string) ([]storage.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]storage.Entry, 0, len(s.values))
	for k, v := range s.values {
		out = append(out, storage.Entry{Key: k, Data: v})
	}
	return out, nil
}
func (s *memoryStorage) SearchFiles(storage.StorageTarget, string) ([]storage.Entry, error) {
	return nil, nil
}
func (s *memoryStorage) Query(storage.Query) ([]storage.Entry, error) { return s.Search("*") }
func (s *memoryStorage) WriteFile(storage.StorageTarget, storage.Keyed, json.RawMessage) error {
	return nil
}
func (s *memoryStorage) ReadFile(storage.StorageTarget, storage.Keyed) (json.RawMessage, error) {
	return nil, errors.New("not found")
}
func (s *memoryStorage) DeleteFile(storage.StorageTarget, storage.Keyed) error { return nil }
func (s *memoryStorage) SetPrivate(storage.Keyed, json.RawMessage) error       { return nil }
func (s *memoryStorage) GetPrivate(storage.Keyed) (json.RawMessage, error) {
	return nil, errors.New("not found")
}
func (s *memoryStorage) DeletePrivate(storage.Keyed) error                { return nil }
func (s *memoryStorage) SetInternal(storage.Keyed, json.RawMessage) error { return nil }
func (s *memoryStorage) GetInternal(storage.Keyed) (json.RawMessage, error) {
	return nil, errors.New("not found")
}
func (s *memoryStorage) DeleteInternal(storage.Keyed) error              { return nil }
func (s *memoryStorage) SetProfile(storage.Keyed, json.RawMessage) error { return nil }
func (s *memoryStorage) Close()                                          {}

var _ storage.Storage = (*memoryStorage)(nil)
