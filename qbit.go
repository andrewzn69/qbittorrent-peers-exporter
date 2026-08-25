package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// peer is one connected peer from /api/v2/sync/torrentPeers
type Peer struct {
	IP          string `json:"ip"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	DLSpeed     int64  `json:"dl_speed"`
	UPSpeed     int64  `json:"up_speed"`
}

type peersResponse struct {
	Peers map[string]Peer `json:"peers"`
}

type torrent struct {
	Hash string `json:"hash"`
}

type Client struct {
	base string
	user string
	pass string
	hc   *http.Client
}

func NewClient(base, user, pass string, timeout time.Duration) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		base: strings.TrimRight(base, "/"),
		user: user,
		pass: pass,
		hc:   &http.Client{Jar: jar, Timeout: timeout},
	}, nil
}

// login exchanges the creds for a SID cookie, which the jar then carries
func (c *Client) Login(ctx context.Context) error {
	form := url.Values{"username": {c.user}, "password": {c.pass}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// qbittorrent returns 401 unless referer matches the host it was reached on
	req.Header.Set("Referer", c.base)

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login: http %d", resp.StatusCode)
	}
	// wrong creds still return 200, the body is the only signal
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if s := strings.TrimSpace(string(body)); s != "Ok." {
		return fmt.Errorf("login rejected: %s", s)
	}
	return nil
}

// get calls the API, re-authenticating once if the session has expired
func (c *Client) get(ctx context.Context, path string, out any) error {
	do := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Referer", c.base)
		return c.hc.Do(req)
	}

	resp, err := do()
	if err != nil {
		return err
	}
	// sessions expire server-side, so one 403 is routine rather than fatal
	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		if err := c.Login(ctx); err != nil {
			return err
		}
		if resp, err = do(); err != nil {
			return err
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: http %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// hashes lists the torrents worth asking about, since each one costs a round trip
func (c *Client) Hashes(ctx context.Context, filter string) ([]string, error) {
	var ts []torrent
	if err := c.get(ctx, "/api/v2/torrents/info?filter="+url.QueryEscape(filter), &ts); err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(ts))
	for _, t := range ts {
		hashes = append(hashes, t.Hash)
	}
	return hashes, nil
}

// peers returns the peers currently connected for one torrent
func (c *Client) Peers(ctx context.Context, hash string) ([]Peer, error) {
	var r peersResponse
	if err := c.get(ctx, "/api/v2/sync/torrentPeers?hash="+url.QueryEscape(hash), &r); err != nil {
		return nil, err
	}
	peers := make([]Peer, 0, len(r.Peers))
	for _, p := range r.Peers {
		peers = append(peers, p)
	}
	return peers, nil
}
