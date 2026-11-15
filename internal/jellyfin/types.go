package jellyfin

type SystemInfo struct {
	ID      string `json:"Id"`
	Name    string `json:"ServerName"`
	Version string `json:"Version"`
}

type Library struct {
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	CollectionType string `json:"CollectionType"`
}

type Libraries struct {
	Items []Library `json:"Items"`
}

type Items struct {
	Items            []Item `json:"Items"`
	TotalRecordCount int    `json:"TotalRecordCount"`
}

type Item struct {
	ID                string                 `json:"Id"`
	Name              string                 `json:"Name"`
	SortName          string                 `json:"SortName"`
	Type              string                 `json:"Type"`
	MediaType         string                 `json:"MediaType"`
	ParentID          string                 `json:"ParentId"`
	ParentIndexNumber int                    `json:"ParentIndexNumber"`
	IndexNumber       int                    `json:"IndexNumber"`
	SeriesName        string                 `json:"SeriesName"`
	Overview          string                 `json:"Overview"`
	ProductionYear    int                    `json:"ProductionYear"`
	PremiereDate      string                 `json:"PremiereDate"`
	RunTimeTicks      int64                  `json:"RunTimeTicks"`
	Genres            []string               `json:"Genres"`
	ProviderIDs       map[string]string      `json:"ProviderIds"`
	ImageTags         map[string]string      `json:"ImageTags"`
	BackdropImageTags []string               `json:"BackdropImageTags"`
	MediaSources      []MediaSource          `json:"MediaSources"`
	UserData          map[string]interface{} `json:"UserData"`
}

type MediaSource struct {
	ID        string `json:"Id"`
	Container string `json:"Container"`
	Protocol  string `json:"Protocol"`
}

type Session struct {
	ID                    string    `json:"Id"`
	UserName              string    `json:"UserName"`
	Client                string    `json:"Client"`
	DeviceName            string    `json:"DeviceName"`
	DeviceID              string    `json:"DeviceId"`
	SupportsRemoteControl bool      `json:"SupportsRemoteControl"`
	NowPlayingItem        *Item     `json:"NowPlayingItem"`
	PlayState             PlayState `json:"PlayState"`
}

type PlayState struct {
	PositionTicks int64 `json:"PositionTicks"`
	IsPaused      bool  `json:"IsPaused"`
	IsMuted       bool  `json:"IsMuted"`
	VolumeLevel   int   `json:"VolumeLevel"`
}

type GeneralCommand struct {
	Name      string            `json:"Name"`
	Arguments map[string]string `json:"Arguments,omitempty"`
}
