package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/slidebolt/plugin-jellyfin/internal/jellyfin"
	contract "github.com/slidebolt/sb-contract"
	domain "github.com/slidebolt/sb-domain"
	media "github.com/slidebolt/sb-media-sdk"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	runtime "github.com/slidebolt/sb-runtime"
	storage "github.com/slidebolt/sb-storage-sdk"
)

const PluginID = "plugin-jellyfin"
const jellyfinServerResource = "jellyfin-server"

type serverState struct {
	ID        string
	Info      jellyfin.SystemInfo
	Libraries map[string]jellyfin.Library
	Players   map[string]string // SlideBolt device ID -> Jellyfin session ID
	Entities  map[string]domain.EntityKey
	Devices   map[string]domain.DeviceKey
}

type App struct {
	msg      messenger.Messenger
	store    storage.Storage
	cmds     *messenger.Commands
	subs     []messenger.Subscription
	config   config
	client   *jellyfin.Client
	ctx      context.Context
	cancel   context.CancelFunc
	reporter runtime.ResourceReporter
	mu       sync.RWMutex
	server   *serverState
}

func New() *App { return &App{} }

var _ media.Provider = (*App)(nil)
var _ runtime.ResourceAware = (*App)(nil)

func (a *App) Hello() contract.HelloResponse {
	setup := contract.SelectableSetup("Media",
		contract.SetupField{Key: "url", Environment: "JELLYFIN_URL", Name: "Jellyfin URL", Description: "Base URL of the Jellyfin server.", Control: contract.SetupText, Format: "uri", Required: true},
		contract.SetupField{Key: "token", Environment: "JELLYFIN_TOKEN", Name: "Jellyfin token", Description: "API token used to access Jellyfin.", Control: contract.SetupSecret, Required: true, Sensitive: true},
		contract.SetupField{Key: "refresh_interval", Environment: "JELLYFIN_REFRESH_INTERVAL", Name: "Refresh interval", Description: "Interval between library and session refreshes.", Control: contract.SetupText, Advanced: true, Default: json.RawMessage(`"30s"`)},
	)
	setup.Resources = []contract.ExternalResourceDefinition{{
		ID: jellyfinServerResource, Kind: contract.ExternalResourceService, Name: "Jellyfin server",
		Description: "Supplies media libraries, catalog items, artwork, playback streams, sessions, and player controls.",
		Fields:      []string{"url", "token", "refresh_interval"}, Activation: contract.ResourceActivation{Mode: contract.ResourceActivationConfigured, Fields: []string{"url", "token"}},
	}}
	definitions := contract.DescribePlugin("Jellyfin", "Supplies Jellyfin media catalogs, artwork, direct streams, sessions, and generic player controls.", "media_library", "media_player", "media_server", "media_session")
	definitions.Collections = []contract.CollectionDefinition{{ID: "sessions", Name: "Playback sessions", Description: "Playback that is active on the media server now.", EntityTypes: []string{"media_session"}, Mode: contract.CollectionAuthoritativeSnapshot, OnMissing: contract.CollectionMissingDelete}}
	return contract.HelloResponse{
		ID: PluginID, Kind: contract.KindPlugin, ContractVersion: contract.ContractVersion,
		DependsOn:   []string{"messenger", "storage"},
		Definitions: definitions,
		Setup:       setup,
	}
}

func (a *App) SetResourceReporter(reporter runtime.ResourceReporter) { a.reporter = reporter }

func (a *App) OnStart(deps map[string]json.RawMessage) (json.RawMessage, error) {
	config, err := loadConfig()
	if errors.Is(err, errNotConfigured) {
		log.Println("plugin-jellyfin: disabled; JELLYFIN_URL and JELLYFIN_TOKEN are not configured")
		return nil, nil
	}
	if err != nil {
		a.resourceDegraded(err)
		return nil, err
	}
	msg, err := messenger.Connect(deps)
	if err != nil {
		return nil, fmt.Errorf("connect messenger: %w", err)
	}
	store, err := storage.Connect(deps)
	if err != nil {
		msg.Close()
		return nil, fmt.Errorf("connect storage: %w", err)
	}
	a.msg, a.store, a.config = msg, store, config
	a.client, err = jellyfin.New(a.config.URL, a.config.Token, 20*time.Second)
	if err != nil {
		a.resourceDegraded(err)
		a.closeDependencies()
		return nil, err
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	if err := a.sync(a.ctx); err != nil {
		a.resourceDegraded(err)
		a.closeDependencies()
		return nil, fmt.Errorf("initial Jellyfin sync: %w", err)
	}
	a.resourceReady()
	a.cmds = messenger.NewCommands(msg, domain.LookupCommand)
	sub, err := a.cmds.Receive(PluginID+".>", a.handleCommand)
	if err != nil {
		a.closeDependencies()
		return nil, fmt.Errorf("subscribe commands: %w", err)
	}
	a.subs = append(a.subs, sub)
	providerSubs, err := media.ServeProvider(msg, PluginID, a)
	if err != nil {
		a.closeDependencies()
		return nil, fmt.Errorf("serve media provider: %w", err)
	}
	a.subs = append(a.subs, providerSubs...)
	if err := msg.Flush(); err != nil {
		a.closeDependencies()
		return nil, fmt.Errorf("flush subscriptions: %w", err)
	}
	go a.loop()
	log.Println("plugin-jellyfin: started")
	return nil, nil
}

func (a *App) OnShutdown() error {
	if a.cancel != nil {
		a.cancel()
	}
	for _, sub := range a.subs {
		_ = sub.Unsubscribe()
	}
	a.closeDependencies()
	return nil
}

func (a *App) closeDependencies() {
	if a.store != nil {
		a.store.Close()
		a.store = nil
	}
	if a.msg != nil {
		a.msg.Close()
		a.msg = nil
	}
}

func (a *App) loop() {
	t := time.NewTicker(a.config.Refresh)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
			if err := a.sync(a.ctx); err != nil {
				a.resourceDegraded(err)
				continue
			}
			a.resourceReady()
		}
	}
}

func (a *App) resourceReady() {
	if a.reporter != nil {
		a.reporter.Ready(jellyfinServerResource)
	}
}
func (a *App) resourceDegraded(err error) {
	if a.reporter != nil {
		a.reporter.Degraded(jellyfinServerResource, "server_unavailable", err.Error(), true)
	}
}

func stableID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if id := strings.Trim(b.String(), "-"); id != "" && len(id) <= 64 {
		return id
	}
	h := sha256.Sum256([]byte(value))
	return "id-" + hex.EncodeToString(h[:8])
}

func (a *App) state() *serverState { a.mu.RLock(); defer a.mu.RUnlock(); return a.server }
