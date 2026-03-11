# Solo Grafana Dashboard

This directory contains an importable Grafana dashboard for Solo metrics exposed by:

- `solo dashboard --addr :8081`
- metrics endpoint: `http://localhost:8081/metrics`

## 1) Start Solo dashboard endpoint

```bash
solo dashboard --addr :8081
```

## 2) Configure Prometheus to scrape Solo

Add a scrape job:

```yaml
scrape_configs:
  - job_name: "solo"
    scrape_interval: 15s
    static_configs:
      - targets: ["localhost:8081"]
    metrics_path: /metrics
```

Reload Prometheus.

## 3) Add Prometheus datasource in Grafana

Use your existing Prometheus endpoint (for example `http://prometheus:9090`).

## 4) Import dashboard

In Grafana:

1. Dashboards → Import
2. Upload `grafana/solo-dashboard.json`
3. Select your Prometheus datasource
4. Save

## Metrics included

- `solo_tasks_total`
- `solo_tasks_by_status{status="..."}`
- `solo_active_sessions`
- `solo_active_reservations`
- `solo_pending_handoffs`
- `solo_worktrees_active`
- `solo_worktrees_cleanup_pending`
- `solo_db_size_bytes`
- `solo_zombie_sessions`
