package controller

import _ "embed"

// shopDashboardTemplate is a Grafana dashboard rendered per shop. __SHOP__ and
// __NS__ are substituted by shopDashboardJSON. It is kept in a separate .json
// file (embedded below) so the long JSON/PromQL lines do not trip the Go line
// linters, and so editors syntax-highlight it as JSON.
//
// It covers the §4.1 metrics: total/successful/failed HTTP requests over 24h,
// unique visitors, 404s by endpoint, total traffic in GB, request rate and
// latency, plus CPU/RAM/filesystem/network usage and the business counters.
//
//go:embed shop_dashboard.json
var shopDashboardTemplate string
