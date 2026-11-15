package app

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/slidebolt/plugin-jellyfin/internal/jellyfin"
	domain "github.com/slidebolt/sb-domain"
)

var playerCommands = []string{"media_play", "media_pause", "media_stop", "media_seek", "media_next_track", "media_previous_track", "media_set_volume", "media_mute"}

func (a *App) sync(ctx context.Context) error {
	var info jellyfin.SystemInfo
	if err := a.client.Get(ctx, "/System/Info", nil, &info); err != nil {
		return err
	}
	serverID := stableID(info.ID)
	if info.ID == "" {
		serverID = hashID(a.config.URL)
	}
	now := time.Now().UTC()
	current := &serverState{ID: serverID, Info: info, Libraries: map[string]jellyfin.Library{}, Players: map[string]string{}, Entities: map[string]domain.EntityKey{}, Devices: map[string]domain.DeviceKey{}}
	if err := a.store.Save(a.healthyDevice(domain.Device{ID: serverID, Plugin: PluginID, Class: "media_server", Name: first(info.Name, "Jellyfin"), Entities: []domain.Entity{}})); err != nil {
		return fmt.Errorf("save server: %w", err)
	}
	current.Devices[PluginID+"."+serverID] = domain.DeviceKey{Plugin: PluginID, ID: serverID}
	serverEntity := a.healthyEntity(domain.Entity{ID: "server", Plugin: PluginID, DeviceID: serverID, Type: "media_server", Name: first(info.Name, "Jellyfin"), State: domain.MediaServer{Connected: true, Name: info.Name, Version: info.Version, LastSyncAt: &now}})
	if err := a.store.Save(serverEntity); err != nil {
		return fmt.Errorf("save server entity: %w", err)
	}
	current.Entities[serverEntity.Key()] = domain.EntityKey{Plugin: PluginID, DeviceID: serverID, ID: serverEntity.ID}
	if err := a.syncLibraries(ctx, current, now); err != nil {
		return err
	}
	if err := a.syncSessions(ctx, current); err != nil {
		return err
	}
	a.mu.Lock()
	old := a.server
	a.server = current
	a.mu.Unlock()
	if old != nil {
		for key, entity := range old.Entities {
			if _, ok := current.Entities[key]; !ok {
				_ = a.store.Delete(entity)
			}
		}
		for key, device := range old.Devices {
			if _, ok := current.Devices[key]; !ok {
				_ = a.store.Delete(device)
			}
		}
	}
	return nil
}

func (a *App) syncLibraries(ctx context.Context, state *serverState, now time.Time) error {
	var result jellyfin.Libraries
	if err := a.client.Get(ctx, "/Library/MediaFolders", nil, &result); err != nil {
		return fmt.Errorf("load libraries: %w", err)
	}
	for _, lib := range result.Items {
		if lib.ID == "" {
			continue
		}
		state.Libraries[lib.ID] = lib
		var count jellyfin.Items
		q := url.Values{"ParentId": {lib.ID}, "Recursive": {"true"}, "Limit": {"1"}}
		if err := a.client.Get(ctx, "/Items", q, &count); err != nil {
			return fmt.Errorf("count library: %w", err)
		}
		entity := a.healthyEntity(domain.Entity{ID: "library-" + stableID(lib.ID), Plugin: PluginID, DeviceID: state.ID, Type: "media_library", Name: lib.Name, State: domain.MediaLibrary{ServerID: state.ID, LibraryID: lib.ID, Kind: libraryKind(lib.CollectionType), ItemCount: count.TotalRecordCount, CanBrowse: true, CanSearch: true, UpdatedAt: &now}})
		if err := a.store.Save(entity); err != nil {
			return fmt.Errorf("save library: %w", err)
		}
		state.Entities[entity.Key()] = domain.EntityKey{Plugin: PluginID, DeviceID: state.ID, ID: entity.ID}
	}
	return nil
}

func (a *App) syncSessions(ctx context.Context, state *serverState) error {
	var sessions []jellyfin.Session
	if err := a.client.Get(ctx, "/Sessions", nil, &sessions); err != nil {
		return fmt.Errorf("load sessions: %w", err)
	}
	for _, session := range sessions {
		if session.SupportsRemoteControl {
			deviceID := "player-" + stableID(first(session.DeviceID, session.ID))
			state.Players[deviceID] = session.ID
			name := first(session.DeviceName, session.Client, "Jellyfin player")
			if err := a.store.Save(a.healthyDevice(domain.Device{ID: deviceID, Plugin: PluginID, Class: "media_player", Name: name, Entities: []domain.Entity{}})); err != nil {
				return fmt.Errorf("save player: %w", err)
			}
			state.Devices[PluginID+"."+deviceID] = domain.DeviceKey{Plugin: PluginID, ID: deviceID}
			player := playerState(state.ID, session)
			entity := a.healthyEntity(domain.Entity{ID: "player", Plugin: PluginID, DeviceID: deviceID, Type: "media_player", Name: name, Commands: playerCommands, State: player})
			if err := a.store.Save(entity); err != nil {
				return fmt.Errorf("save player entity: %w", err)
			}
			state.Entities[entity.Key()] = domain.EntityKey{Plugin: PluginID, DeviceID: deviceID, ID: entity.ID}
		}
		if session.NowPlayingItem == nil {
			continue
		}
		item := mediaItem(state.ID, "", *session.NowPlayingItem)
		ref := item.Ref
		sessionEntity := a.healthyEntity(domain.Entity{ID: "session-" + stableID(session.ID), Plugin: PluginID, DeviceID: state.ID, Type: "media_session", Name: first(session.NowPlayingItem.Name, session.DeviceName, "Jellyfin session"), State: domain.MediaSession{ServerID: state.ID, PlayerID: session.DeviceID, User: session.UserName, Item: &ref, Title: session.NowPlayingItem.Name, State: playbackState(session), PositionMS: ticksToMS(session.PlayState.PositionTicks), DurationMS: ticksToMS(session.NowPlayingItem.RunTimeTicks)}})
		if err := a.store.Save(sessionEntity); err != nil {
			return fmt.Errorf("save session: %w", err)
		}
		state.Entities[sessionEntity.Key()] = domain.EntityKey{Plugin: PluginID, DeviceID: state.ID, ID: sessionEntity.ID}
	}
	return nil
}

func playerState(serverID string, s jellyfin.Session) domain.MediaPlayer {
	p := domain.MediaPlayer{State: playbackState(s), VolumeLevel: clamp(float64(s.PlayState.VolumeLevel) / 100), IsVolumeMuted: s.PlayState.IsMuted, ServerID: serverID, Available: true, PositionMS: ticksToMS(s.PlayState.PositionTicks)}
	if s.NowPlayingItem != nil {
		item := mediaItem(serverID, "", *s.NowPlayingItem)
		p.Item = &item.Ref
		p.MediaTitle = item.Title
		p.DurationMS = item.DurationMS
	}
	return p
}

func playbackState(s jellyfin.Session) string {
	if s.NowPlayingItem == nil {
		return "idle"
	}
	if s.PlayState.IsPaused {
		return "paused"
	}
	return "playing"
}

func libraryKind(kind string) string {
	switch strings.ToLower(kind) {
	case "movies":
		return "movie"
	case "tvshows":
		return "show"
	case "music":
		return "music"
	case "photos", "homevideos":
		return "photo"
	default:
		return "mixed"
	}
}

func hashID(value string) string {
	return "server-" + stableID(strconv.FormatUint(uint64(simpleHash(value)), 16))
}
func simpleHash(s string) uint32 {
	var h uint32 = 2166136261
	for i := range []byte(s) {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func ticksToMS(v int64) int64 {
	if v <= 0 {
		return 0
	}
	return v / 10000
}
func msToTicks(v int64) int64 {
	if v <= 0 {
		return 0
	}
	if v > (1<<63-1)/10000 {
		return 1<<63 - 1
	}
	return v * 10000
}
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
