# qbittorrent-peers-exporter

Prometheus exporter for qBittorrent peers, grouped by country.

qBittorrent reports each connected peer's country through `/api/v2/sync/torrentPeers`.
This polls it, sums speeds per country, and exposes the result. No GeoIP database needed.

## Metrics

Country metrics carry two labels: `country` (name) and `country_iso3` (ISO 3166-1 alpha-3).

| Metric | Description |
| --- | --- |
| `qbittorrent_peers` | Distinct peers connected, counted by IP |
| `qbittorrent_peer_download_bytes_per_second` | Download rate summed over that country's peers |
| `qbittorrent_peer_upload_bytes_per_second` | Upload rate summed over that country's peers |
| `qbittorrent_peers_unresolved` | Peers whose country code was not recognised |
| `qbittorrent_peers_refresh_success` | `1` if the last poll completed |
| `qbittorrent_peers_last_refresh_timestamp_seconds` | When the last poll completed |

A peer sharing several torrents counts once, but its speeds sum across all of them.

## Config

| Variable | Default | |
| --- | --- | --- |
| `QBIT_URL` | `http://localhost:8080` | |
| `QBIT_USERNAME` | `admin` | |
| `QBIT_PASSWORD` | | required |
| `LISTEN_ADDR` | `:9714` | |
| `TORRENT_FILTER` | `active` | `all` includes idle torrents |
| `REFRESH_INTERVAL` | `30s` | |
| `HTTP_TIMEOUT` | `15s` | |
| `WORKERS` | `8` | one request per torrent |
| `LOG_LEVEL` | `info` | |

Peers are polled on a timer and served from cache, so a slow qBittorrent makes metrics
stale rather than failing scrapes.

## Run

```sh
docker run --rm -p 9714:9714 \
  -e QBIT_URL=http://qbittorrent:8080 \
  -e QBIT_PASSWORD=secret \
  ghcr.io/andrewzn69/qbittorrent-peers-exporter:latest
```

MIT
