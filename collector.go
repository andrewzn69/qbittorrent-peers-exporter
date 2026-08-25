package main

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	peersDesc = prometheus.NewDesc(
		"qbittorrent_peers",
		"Peers currently connected, by country.",
		[]string{"country", "country_iso3"}, nil)
	downDesc = prometheus.NewDesc(
		"qbittorrent_peer_download_bytes_per_second",
		"Download rate summed across peers in a country.",
		[]string{"country", "country_iso3"}, nil)
	upDesc = prometheus.NewDesc(
		"qbittorrent_peer_upload_bytes_per_second",
		"Upload rate summed across peers in a country.",
		[]string{"country", "country_iso3"}, nil)
	unresolvedDesc = prometheus.NewDesc(
		"qbittorrent_peers_unresolved",
		"Peers whose country code did not map to a known country.", nil, nil)
	okDesc = prometheus.NewDesc(
		"qbittorrent_peers_refresh_success",
		"1 if the last refresh completed.", nil, nil)
	tsDesc = prometheus.NewDesc(
		"qbittorrent_peers_last_refresh_timestamp_seconds",
		"Unix time of the last successful refresh.", nil, nil)
)

// stat is one country's share of the swarm
type stat struct {
	country string
	iso3    string
	peers   int
	down    int64
	up      int64
}

type Collector struct {
	client  *Client
	filter  string
	workers int

	mu         sync.RWMutex
	stats      []stat
	unresolved int
	ok         float64
	last       time.Time
}

func NewCollector(c *Client, filter string, workers int) *Collector {
	return &Collector{client: c, filter: filter, workers: workers}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- peersDesc
	ch <- downDesc
	ch <- upDesc
	ch <- unresolvedDesc
	ch <- okDesc
	ch <- tsDesc
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ch <- prometheus.MustNewConstMetric(okDesc, prometheus.GaugeValue, c.ok)
	ch <- prometheus.MustNewConstMetric(tsDesc, prometheus.GaugeValue, float64(c.last.Unix()))
	ch <- prometheus.MustNewConstMetric(unresolvedDesc, prometheus.GaugeValue, float64(c.unresolved))
	for _, s := range c.stats {
		ch <- prometheus.MustNewConstMetric(peersDesc, prometheus.GaugeValue, float64(s.peers), s.country, s.iso3)
		ch <- prometheus.MustNewConstMetric(downDesc, prometheus.GaugeValue, float64(s.down), s.country, s.iso3)
		ch <- prometheus.MustNewConstMetric(unresolvedDesc, prometheus.GaugeValue, float64(s.up), s.country, s.iso3)
	}
}

// run refreshes on an interval until ctx is cancelled
func (c *Collector) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := c.refresh(ctx); err != nil {
			slog.Error("refresh failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Collector) refresh(ctx context.Context) error {
	start := time.Now()

	hashes, err := c.client.Hashes(ctx, c.filter)
	if err != nil {
		c.mu.Lock()
		c.ok = 0
		c.mu.Unlock()
		return err
	}

	// one request per torrent, bounded so a big library cant open hundreds of connections at once
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		all  []Peer
		sem  = make(chan struct{}, c.workers)
		fail int
	)
	for _, h := range hashes {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			peers, err := c.client.Peers(ctx, h)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fail++
				return
			}
			all = append(all, peers...)
		}(h)
	}
	wg.Wait()

	if fail > 0 {
		slog.Warn("some torrents did not report peers",
			"failed", fail, "torrents", len(hashes))
	}

	stats, unresolved := aggregate(all)

	total := 0
	for _, s := range stats {
		total += s.peers
	}
	slog.Info("refreshed",
		"torrents", len(hashes),
		"countries", len(stats),
		"peers", total,
		"unresolved", unresolved,
		"took", time.Since(start).Round(time.Millisecond).String())

	c.mu.Lock()
	c.stats, c.unresolved, c.ok, c.last = stats, unresolved, 1, time.Now()
	c.mu.Unlock()
	return nil
}

// aggregate turns every peer seen across all torrents into one row per country.
// the same ip appears once per shared torrent, so peers are counted unique by ip
// while speeds stay summed - the bytes on each torrent connection are real
func aggregate(peers []Peer) ([]stat, int) {
	type acc struct {
		country string
		down    int64
		up      int64
		ips     map[string]struct{}
	}

	byCode := make(map[string]*acc)
	unresolved := 0

	for _, p := range peers {
		code, ok := iso3[strings.ToLower(p.CountryCode)]
		if !ok {
			// there is no shape to draw and an empty label matches nothing
			// so these are counted separately instead of becoming a phantom series
			unresolved++
			continue
		}

		a := byCode[code]
		if a == nil {
			name := p.Country
			if name == "" {
				name = code
			}
			a = &acc{country: name, ips: make(map[string]struct{})}
			byCode[code] = a
		}
		a.down += p.DLSpeed
		a.up += p.UPSpeed
		a.ips[p.IP] = struct{}{}
	}

	stats := make([]stat, 0, len(byCode))
	for code, a := range byCode {
		stats = append(stats, stat{
			country: a.country,
			iso3:    code,
			peers:   len(a.ips),
			down:    a.down,
			up:      a.up,
		})
	}
	// stable order keeps /metrics diffable between scrapes
	sort.Slice(stats, func(i, j int) bool { return stats[i].iso3 < stats[j].iso3 })

	return stats, unresolved
}
