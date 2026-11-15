package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

func New(baseURL, token string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid Jellyfin URL")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("Jellyfin token is required")
	}
	return &Client{base: u, token: token, http: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	req, err := c.request(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Jellyfin GET %s: request failed", path)
	}
	defer resp.Body.Close()
	if err := responseError(path, resp); err != nil {
		return err
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out); err != nil {
		return fmt.Errorf("Jellyfin GET %s: invalid response", path)
	}
	return nil
}

func (c *Client) Bytes(ctx context.Context, path string, query url.Values) ([]byte, string, error) {
	req, err := c.request(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("Jellyfin GET %s: request failed", path)
	}
	defer resp.Body.Close()
	if err := responseError(path, resp); err != nil {
		return nil, "", err
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, "", fmt.Errorf("Jellyfin GET %s: read failed", path)
	}
	return b, resp.Header.Get("Content-Type"), nil
}

func (c *Client) Post(ctx context.Context, path string, query url.Values, body any) error {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := c.request(ctx, http.MethodPost, path, query, data)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Jellyfin POST %s: request failed", path)
	}
	defer resp.Body.Close()
	return responseError(path, resp)
}

func (c *Client) StreamURL(itemID, mediaType, sourceID string) string {
	path := "/Videos/" + url.PathEscape(itemID) + "/stream"
	if strings.EqualFold(mediaType, "Audio") {
		path = "/Audio/" + url.PathEscape(itemID) + "/stream"
	}
	q := url.Values{"Static": {"true"}, "api_key": {c.token}}
	if sourceID != "" {
		q.Set("MediaSourceId", sourceID)
	}
	return c.url(path, q).String()
}

func (c *Client) ImageURL(itemID string) string {
	return c.url("/Items/"+url.PathEscape(itemID)+"/Images/Primary", url.Values{"api_key": {c.token}}).String()
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path, query).String(), reader)
	if err != nil {
		return nil, fmt.Errorf("Jellyfin %s %s: create request", method, path)
	}
	req.Header.Set("Authorization", `MediaBrowser Client="SlideBolt", Device="SlideBolt", DeviceId="slidebolt-jellyfin", Version="1", Token="`+c.token+`"`)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) url(path string, query url.Values) *url.URL {
	u := *c.base
	rawPath := strings.TrimRight(c.base.EscapedPath(), "/") + "/" + strings.TrimLeft(path, "/")
	decodedPath, err := url.PathUnescape(rawPath)
	if err == nil {
		u.Path, u.RawPath = decodedPath, rawPath
	} else {
		u.Path = rawPath
	}
	u.RawQuery = query.Encode()
	return &u
}

func responseError(path string, resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("Jellyfin %s: HTTP %d", path, resp.StatusCode)
}
