package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"strconv"
	"time"

	"github.com/slidebolt/plugin-jellyfin/internal/jellyfin"
	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
)

func (a *App) handleCommand(addr messenger.Address, command any) {
	switch command.(type) {
	case domain.MediaPlay, domain.MediaPause, domain.MediaStop, domain.MediaSeek, domain.MediaNextTrack, domain.MediaPreviousTrack, domain.MediaSetVolume, domain.MediaMute:
		if err := a.controlPlayer(addr.DeviceID, command); err != nil {
			_ = a.savePlayerError(addr.DeviceID, err)
			log.Printf("plugin-jellyfin: player command failed: %v", err)
		}
	}
}

func (a *App) controlPlayer(deviceID string, command any) error {
	state := a.state()
	if state == nil {
		return fmt.Errorf("Jellyfin server is not available")
	}
	sessionID := state.Players[deviceID]
	if sessionID == "" {
		return fmt.Errorf("Jellyfin player is not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base := "/Sessions/" + url.PathEscape(sessionID)
	var err error
	switch cmd := command.(type) {
	case domain.MediaPlay:
		if cmd.Source != nil && cmd.Source.Item != nil {
			item := cmd.Source.Item
			if item.Provider != PluginID || item.ServerID != state.ID {
				return fmt.Errorf("Jellyfin player requires an item from its Jellyfin server")
			}
			q := url.Values{"playCommand": {"PlayNow"}, "itemIds": {item.ItemID}}
			if cmd.PositionMS > 0 {
				q.Set("startPositionTicks", strconv.FormatInt(msToTicks(cmd.PositionMS), 10))
			}
			err = a.client.Post(ctx, base+"/Playing", q, nil)
		} else {
			err = a.client.Post(ctx, base+"/Playing/Unpause", nil, nil)
		}
	case domain.MediaPause:
		err = a.client.Post(ctx, base+"/Playing/Pause", nil, nil)
	case domain.MediaStop:
		err = a.client.Post(ctx, base+"/Playing/Stop", nil, nil)
	case domain.MediaSeek:
		err = a.client.Post(ctx, base+"/Playing/Seek", url.Values{"seekPositionTicks": {strconv.FormatInt(msToTicks(cmd.PositionMS), 10)}}, nil)
	case domain.MediaNextTrack:
		err = a.client.Post(ctx, base+"/Playing/NextTrack", nil, nil)
	case domain.MediaPreviousTrack:
		err = a.client.Post(ctx, base+"/Playing/PreviousTrack", nil, nil)
	case domain.MediaSetVolume:
		volume := int(math.Round(clamp(cmd.VolumeLevel) * 100))
		err = a.client.Post(ctx, base+"/Command", nil, jellyfin.GeneralCommand{Name: "SetVolume", Arguments: map[string]string{"Volume": strconv.Itoa(volume)}})
	case domain.MediaMute:
		name := "Unmute"
		if cmd.Mute {
			name = "Mute"
		}
		err = a.client.Post(ctx, base+"/Command", nil, jellyfin.GeneralCommand{Name: name})
	default:
		return fmt.Errorf("unsupported player command %T", command)
	}
	if err != nil {
		return err
	}
	return a.sync(context.Background())
}

func (a *App) savePlayerError(deviceID string, cause error) error {
	key := domain.EntityKey{Plugin: PluginID, DeviceID: deviceID, ID: "player"}
	raw, err := a.store.Get(key)
	if err != nil {
		return err
	}
	var entity domain.Entity
	if err := json.Unmarshal(raw, &entity); err != nil {
		return err
	}
	player, ok := entity.State.(domain.MediaPlayer)
	if !ok {
		return fmt.Errorf("player entity has invalid state")
	}
	player.LastError = cause.Error()
	entity.State, entity.Status = player, a.health("error", cause.Error())
	return a.store.Save(entity)
}
