package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/slidebolt/plugin-jellyfin/internal/jellyfin"
	domain "github.com/slidebolt/sb-domain"
	media "github.com/slidebolt/sb-media-sdk"
)

func (a *App) Browse(req media.CatalogRequest) media.CatalogPage {
	state := a.state()
	if state == nil || req.ServerID != state.ID {
		return media.CatalogPage{Error: "media server not found"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if req.ItemID != "" {
		item, err := a.loadItem(ctx, req.ItemID)
		if err != nil {
			return media.CatalogPage{Error: err.Error()}
		}
		return media.CatalogPage{Items: []media.MediaItem{mediaItem(state.ID, req.LibraryID, item)}, Total: 1}
	}
	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, err := strconv.Atoi(req.Cursor)
	if err != nil || offset < 0 {
		offset = 0
	}
	parent := req.ParentID
	if parent == "" {
		parent = req.LibraryID
	}
	q := url.Values{
		"ParentId": {parent}, "StartIndex": {strconv.Itoa(offset)}, "Limit": {strconv.Itoa(limit)},
		"Fields":       {"Overview,Genres,ProviderIds,MediaSources,PremiereDate,SortName"},
		"EnableImages": {"true"}, "EnableUserData": {"true"},
	}
	if req.ParentID != "" && strings.EqualFold(req.Kind, "episode") {
		q.Set("Recursive", "true")
	} else {
		q.Set("Recursive", "false")
	}
	if req.Query != "" {
		q.Set("SearchTerm", req.Query)
	}
	if include := jellyfinKind(req.Kind); include != "" {
		q.Set("IncludeItemTypes", include)
	}
	setSort(q, req.Sort)
	var result jellyfin.Items
	if err := a.client.Get(ctx, "/Items", q, &result); err != nil {
		return media.CatalogPage{Error: err.Error()}
	}
	items := make([]media.MediaItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, mediaItem(state.ID, req.LibraryID, item))
	}
	next := ""
	if offset+len(items) < result.TotalRecordCount {
		next = strconv.Itoa(offset + len(items))
	}
	return media.CatalogPage{Items: items, NextCursor: next, Total: result.TotalRecordCount}
}

func (a *App) Artwork(req media.ArtworkRequest) media.ArtworkReply {
	state := a.state()
	if state == nil || req.ServerID != state.ID {
		return media.ArtworkReply{Error: "media server not found"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	item, err := a.loadItem(ctx, req.ItemID)
	if err != nil {
		return media.ArtworkReply{Error: err.Error()}
	}
	imageType, index := "Primary", -1
	if req.Kind == "art" {
		if len(item.BackdropImageTags) == 0 {
			return media.ArtworkReply{Error: "artwork not available"}
		}
		imageType, index = "Backdrop", 0
	} else if item.ImageTags["Primary"] == "" && item.ImageTags["Thumb"] != "" {
		imageType = "Thumb"
	} else if item.ImageTags["Primary"] == "" {
		return media.ArtworkReply{Error: "artwork not available"}
	}
	q := url.Values{}
	if index >= 0 {
		q.Set("Index", strconv.Itoa(index))
	}
	b, contentType, err := a.client.Bytes(ctx, "/Items/"+url.PathEscape(item.ID)+"/Images/"+imageType, q)
	if err != nil {
		return media.ArtworkReply{Error: err.Error()}
	}
	return media.ArtworkReply{Data: b, ContentType: contentType}
}

func (a *App) Resolve(req media.ResolveRequest) media.ResolveReply {
	if req.Source.Item == nil || req.Source.Item.Provider != PluginID {
		return media.ResolveReply{Error: "Jellyfin resolves its own media items only"}
	}
	state := a.state()
	if state == nil || req.Source.Item.ServerID != state.ID {
		return media.ResolveReply{Error: "media server not found"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	item, err := a.loadItem(ctx, req.Source.Item.ItemID)
	if err != nil {
		return media.ResolveReply{Error: err.Error()}
	}
	if strings.EqualFold(item.MediaType, "Photo") || item.Type == "Photo" {
		return media.ResolveReply{URL: a.client.ImageURL(item.ID), Kind: "image", Format: imageFormat(item)}
	}
	if len(item.MediaSources) == 0 {
		return media.ResolveReply{Error: "media source not available"}
	}
	source := item.MediaSources[0]
	kind := "video"
	if strings.EqualFold(item.MediaType, "Audio") {
		kind = "audio"
	}
	return media.ResolveReply{URL: a.client.StreamURL(item.ID, item.MediaType, source.ID), Kind: kind, Format: source.Container}
}

func (a *App) Guide(req media.GuideRequest) media.GuidePage {
	state := a.state()
	if state == nil || req.ServerID != state.ID {
		return media.GuidePage{Error: "media server not found"}
	}
	return media.GuidePage{Channels: []media.GuideChannel{}, Programs: []domain.TVProgram{}}
}

func (a *App) loadItem(ctx context.Context, id string) (jellyfin.Item, error) {
	if strings.TrimSpace(id) == "" {
		return jellyfin.Item{}, fmt.Errorf("media item ID is required")
	}
	var result jellyfin.Items
	q := url.Values{"Ids": {id}, "Limit": {"1"}, "Fields": {"Overview,Genres,ProviderIds,MediaSources,PremiereDate,SortName"}}
	if err := a.client.Get(ctx, "/Items", q, &result); err != nil {
		return jellyfin.Item{}, err
	}
	if len(result.Items) == 0 || result.Items[0].ID == "" {
		return jellyfin.Item{}, fmt.Errorf("media item not found")
	}
	return result.Items[0], nil
}

func mediaItem(serverID, libraryID string, item jellyfin.Item) media.MediaItem {
	ref := domain.MediaItemRef{Provider: PluginID, ServerID: serverID, LibraryID: libraryID, ItemID: item.ID}
	var parent *domain.MediaItemRef
	if item.ParentID != "" {
		p := ref
		p.ItemID = item.ParentID
		parent = &p
	}
	metadata := map[string]json.RawMessage{}
	putMetadata(metadata, "sortTitle", item.SortName)
	putMetadata(metadata, "description", item.Overview)
	putMetadata(metadata, "year", item.ProductionYear)
	putMetadata(metadata, "parentTitle", item.SeriesName)
	putMetadata(metadata, "season", item.ParentIndexNumber)
	putMetadata(metadata, "episode", item.IndexNumber)
	putMetadata(metadata, "availableAt", item.PremiereDate)
	putMetadata(metadata, "genres", item.Genres)
	putMetadata(metadata, "providerIDs", item.ProviderIDs)
	assets := []domain.MediaAsset{}
	if item.ImageTags["Primary"] != "" || item.ImageTags["Thumb"] != "" {
		assets = append(assets, domain.MediaAsset{Kind: "thumbnail", Ref: "thumbnail"})
	}
	if len(item.BackdropImageTags) > 0 {
		assets = append(assets, domain.MediaAsset{Kind: "art", Ref: "art"})
	}
	kind := itemKind(item.Type)
	return media.MediaItem{Ref: ref, Kind: kind, Title: item.Name, Playable: playable(kind), DurationMS: ticksToMS(item.RunTimeTicks), Parent: parent, Metadata: metadata, Assets: assets}
}

func putMetadata(m map[string]json.RawMessage, key string, value any) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return
		}
	case int:
		if v == 0 {
			return
		}
	case []string:
		if len(v) == 0 {
			return
		}
	case map[string]string:
		if len(v) == 0 {
			return
		}
	}
	b, _ := json.Marshal(value)
	m[key] = b
}

func itemKind(kind string) string {
	switch kind {
	case "Movie":
		return "movie"
	case "Series":
		return "show"
	case "Season":
		return "season"
	case "Episode":
		return "episode"
	case "Audio":
		return "track"
	case "MusicAlbum":
		return "album"
	case "MusicArtist":
		return "artist"
	case "Photo":
		return "photo"
	case "Playlist":
		return "playlist"
	case "Folder", "CollectionFolder":
		return "folder"
	default:
		return strings.ToLower(kind)
	}
}

func playable(kind string) bool {
	switch kind {
	case "movie", "episode", "track", "photo":
		return true
	default:
		return false
	}
}
func jellyfinKind(kind string) string {
	switch strings.ToLower(kind) {
	case "movie":
		return "Movie"
	case "show", "series":
		return "Series"
	case "season":
		return "Season"
	case "episode":
		return "Episode"
	case "track", "audio":
		return "Audio"
	case "album":
		return "MusicAlbum"
	case "artist":
		return "MusicArtist"
	case "photo", "image":
		return "Photo"
	case "playlist":
		return "Playlist"
	case "folder":
		return "Folder"
	default:
		return ""
	}
}
func setSort(q url.Values, sort string) {
	switch strings.ToLower(sort) {
	case "title":
		q.Set("SortBy", "SortName")
		q.Set("SortOrder", "Ascending")
	case "title_desc":
		q.Set("SortBy", "SortName")
		q.Set("SortOrder", "Descending")
	case "date", "newest":
		q.Set("SortBy", "DateCreated")
		q.Set("SortOrder", "Descending")
	}
}
func imageFormat(item jellyfin.Item) string {
	if item.ImageTags["Primary"] != "" {
		return "image"
	}
	return "image"
}
