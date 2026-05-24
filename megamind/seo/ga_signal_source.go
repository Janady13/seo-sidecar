package seo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GASignalSource consumes Google Analytics data from BUBBLES and converts
// it into feedback signals for the schema optimization engine.
type GASignalSource struct {
	mu sync.RWMutex

	SidecarURL   string // Main sidecar URL (port 9090)
	GAProxyURL   string // GA proxy URL (port 9091)
	AuthToken    string
	HTTPClient   *http.Client

	// Cached report data
	lastReport    *GAReportPack
	lastFetchTime time.Time
	cacheDuration time.Duration

	// Page-level metrics indexed by domain -> path -> metrics
	pageMetrics map[string]map[string]*GAPageMetrics
}

// GAReportPack matches the JSON structure from ga4-report-pack.py
type GAReportPack struct {
	GeneratedAt   string              `json:"generatedAt"`
	DateRange     GADateRange         `json:"dateRange"`
	DataAPIStatus string              `json:"dataApiStatus"`
	OverallStatus string              `json:"overallStatus"`
	Domains       map[string]GADomain `json:"domains"`
}

type GADateRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Days  int    `json:"days"`
}

type GADomain struct {
	Source      string       `json:"source"`
	Status      string       `json:"status"`
	Summary     GASummary    `json:"summary"`
	TopPages    []GAPage     `json:"topPages"`
	TopEvents   []GAEvent    `json:"topEvents,omitempty"`
	TopSources  []GASource   `json:"topSources,omitempty"`
	Top404      []GA404      `json:"top404,omitempty"`
	TopReferrers []GAReferrer `json:"topReferrers,omitempty"`
}

type GASummary struct {
	// GA4 API fields
	Sessions        int     `json:"sessions,omitempty"`
	TotalUsers      int     `json:"totalUsers,omitempty"`
	NewUsers        int     `json:"newUsers,omitempty"`
	ScreenPageViews int     `json:"screenPageViews,omitempty"`
	EngagedSessions int     `json:"engagedSessions,omitempty"`
	EventCount      int     `json:"eventCount,omitempty"`

	// Nginx fallback fields
	Hits           int     `json:"hits,omitempty"`
	BotHits        int     `json:"botHits,omitempty"`
	HumanHits      int     `json:"humanHits,omitempty"`
	PageViews      int     `json:"pageViews,omitempty"`
	UniqueVisitors int     `json:"uniqueVisitors,omitempty"`
	BotHitRate     float64 `json:"botHitRate,omitempty"`
}

type GAPage struct {
	PagePath        string `json:"pagePath"`
	ScreenPageViews int    `json:"screenPageViews,omitempty"`
	PageViews       int    `json:"pageViews,omitempty"`
	EngagedSessions int    `json:"engagedSessions,omitempty"`
	EventCount      int    `json:"eventCount,omitempty"`
}

type GAEvent struct {
	EventName  string `json:"eventName"`
	EventCount int    `json:"eventCount"`
}

type GASource struct {
	SessionSourceMedium string `json:"sessionSourceMedium"`
	Sessions            int    `json:"sessions"`
}

type GA404 struct {
	Path string `json:"path"`
	Hits int    `json:"hits"`
}

type GAReferrer struct {
	ReferrerHost string `json:"referrerHost"`
	Hits         int    `json:"hits"`
}

// GAPageMetrics contains processed metrics for a single page
type GAPageMetrics struct {
	Domain          string
	Path            string
	PageViews       int
	EngagedSessions int
	EventCount      int
	LastUpdated     time.Time
}

// GASignal represents a feedback signal derived from GA data
type GASignal struct {
	SiteKey         string
	PagePath        string
	SignalType      string // "pageview_change", "engagement_spike", "traffic_drop", etc.
	Value           float64
	BaselineValue   float64
	PercentChange   float64
	Timestamp       time.Time
	DataSource      string // "ga4_api" or "nginx_logs_fallback"
}

// NewGASignalSource creates a new GA signal source.
// sidecarURL is the main sidecar (port 9090), gaProxyURL is the GA proxy (port 9091).
// If gaProxyURL is empty, it defaults to sidecarURL with port 9091.
func NewGASignalSource(sidecarURL, authToken string) *GASignalSource {
	// Default GA proxy URL to port 9091 on same host
	gaProxyURL := sidecarURL
	if strings.Contains(sidecarURL, ":9090") {
		gaProxyURL = strings.Replace(sidecarURL, ":9090", ":9091", 1)
	} else if !strings.Contains(sidecarURL, ":9091") {
		gaProxyURL = sidecarURL + ":9091"
	}

	return &GASignalSource{
		SidecarURL:    sidecarURL,
		GAProxyURL:    gaProxyURL,
		AuthToken:     authToken,
		cacheDuration: 15 * time.Minute,
		pageMetrics:   make(map[string]map[string]*GAPageMetrics),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewGASignalSourceWithProxy creates a GA signal source with explicit proxy URL.
func NewGASignalSourceWithProxy(sidecarURL, gaProxyURL, authToken string) *GASignalSource {
	return &GASignalSource{
		SidecarURL:    sidecarURL,
		GAProxyURL:    gaProxyURL,
		AuthToken:     authToken,
		cacheDuration: 15 * time.Minute,
		pageMetrics:   make(map[string]map[string]*GAPageMetrics),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FetchReport retrieves the latest GA report from BUBBLES GA proxy
func (g *GASignalSource) FetchReport() (*GAReportPack, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Return cached if fresh
	if g.lastReport != nil && time.Since(g.lastFetchTime) < g.cacheDuration {
		return g.lastReport, nil
	}

	// Fetch from GA proxy endpoint
	url := fmt.Sprintf("%s/ga/report-latest", g.GAProxyURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.AuthToken)

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch report: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sidecar returned %d: %s", resp.StatusCode, string(body))
	}

	var report GAReportPack
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		return nil, fmt.Errorf("decode report: %w", err)
	}

	// Update cache
	g.lastReport = &report
	g.lastFetchTime = time.Now()

	// Index page metrics
	g.indexPageMetrics(&report)

	return &report, nil
}

// indexPageMetrics processes the report and indexes metrics by domain/path
func (g *GASignalSource) indexPageMetrics(report *GAReportPack) {
	for domain, domainData := range report.Domains {
		if g.pageMetrics[domain] == nil {
			g.pageMetrics[domain] = make(map[string]*GAPageMetrics)
		}

		for _, page := range domainData.TopPages {
			pageViews := page.PageViews
			if page.ScreenPageViews > 0 {
				pageViews = page.ScreenPageViews
			}

			g.pageMetrics[domain][page.PagePath] = &GAPageMetrics{
				Domain:          domain,
				Path:            page.PagePath,
				PageViews:       pageViews,
				EngagedSessions: page.EngagedSessions,
				EventCount:      page.EventCount,
				LastUpdated:     time.Now(),
			}
		}
	}
}

// GetPageMetrics returns metrics for a specific page
func (g *GASignalSource) GetPageMetrics(domain, path string) *GAPageMetrics {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if domainPages, ok := g.pageMetrics[domain]; ok {
		return domainPages[path]
	}
	return nil
}

// GetDomainSummary returns summary metrics for a domain
func (g *GASignalSource) GetDomainSummary(domain string) *GASummary {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.lastReport == nil {
		return nil
	}

	if domainData, ok := g.lastReport.Domains[domain]; ok {
		return &domainData.Summary
	}
	return nil
}

// GenerateSignals analyzes GA data and produces feedback signals
// This compares current metrics to baselines to detect changes
func (g *GASignalSource) GenerateSignals(baselines map[string]map[string]*GAPageMetrics) []GASignal {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var signals []GASignal

	if g.lastReport == nil {
		return signals
	}

	for domain, domainData := range g.lastReport.Domains {
		dataSource := domainData.Source

		baselineDomain := baselines[domain]
		if baselineDomain == nil {
			baselineDomain = make(map[string]*GAPageMetrics)
		}

		for _, page := range domainData.TopPages {
			currentViews := page.PageViews
			if page.ScreenPageViews > 0 {
				currentViews = page.ScreenPageViews
			}

			baseline := baselineDomain[page.PagePath]
			if baseline == nil {
				// New page - no baseline, report as new traffic
				if currentViews > 10 {
					signals = append(signals, GASignal{
						SiteKey:       domainToSiteKey(domain),
						PagePath:      page.PagePath,
						SignalType:    "new_traffic",
						Value:         float64(currentViews),
						BaselineValue: 0,
						PercentChange: 100,
						Timestamp:     time.Now(),
						DataSource:    dataSource,
					})
				}
				continue
			}

			// Calculate percent change
			if baseline.PageViews > 0 {
				percentChange := float64(currentViews-baseline.PageViews) / float64(baseline.PageViews) * 100

				// Significant traffic increase (>25%)
				if percentChange > 25 && currentViews > 20 {
					signals = append(signals, GASignal{
						SiteKey:       domainToSiteKey(domain),
						PagePath:      page.PagePath,
						SignalType:    "traffic_increase",
						Value:         float64(currentViews),
						BaselineValue: float64(baseline.PageViews),
						PercentChange: percentChange,
						Timestamp:     time.Now(),
						DataSource:    dataSource,
					})
				}

				// Significant traffic drop (>25%)
				if percentChange < -25 && baseline.PageViews > 20 {
					signals = append(signals, GASignal{
						SiteKey:       domainToSiteKey(domain),
						PagePath:      page.PagePath,
						SignalType:    "traffic_drop",
						Value:         float64(currentViews),
						BaselineValue: float64(baseline.PageViews),
						PercentChange: percentChange,
						Timestamp:     time.Now(),
						DataSource:    dataSource,
					})
				}
			}

			// Engagement signals (if available from GA4 API)
			if baseline.EngagedSessions > 0 && page.EngagedSessions > 0 {
				engagementChange := float64(page.EngagedSessions-baseline.EngagedSessions) / float64(baseline.EngagedSessions) * 100

				if engagementChange > 30 {
					signals = append(signals, GASignal{
						SiteKey:       domainToSiteKey(domain),
						PagePath:      page.PagePath,
						SignalType:    "engagement_increase",
						Value:         float64(page.EngagedSessions),
						BaselineValue: float64(baseline.EngagedSessions),
						PercentChange: engagementChange,
						Timestamp:     time.Now(),
						DataSource:    dataSource,
					})
				}
			}
		}
	}

	return signals
}

// GetTopPages returns the top N pages for a domain by pageviews
func (g *GASignalSource) GetTopPages(domain string, limit int) []GAPage {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.lastReport == nil {
		return nil
	}

	if domainData, ok := g.lastReport.Domains[domain]; ok {
		if limit <= 0 || limit > len(domainData.TopPages) {
			return domainData.TopPages
		}
		return domainData.TopPages[:limit]
	}
	return nil
}

// domainToSiteKey converts a domain to a site key
func domainToSiteKey(domain string) string {
	// Map common domains to site keys
	domainMap := map[string]string{
		"thatdeveloperguy.com":   "thatdeveloperguy",
		"thatcoputerdude.com":    "thatcomputerdude",
		"thatwebhostingguy.com":  "thatwebhostingguy",
		"thataiguy.org":          "thataiguy",
		"feedthejoe.com":         "feedthejoe",
		"freeaicharity.org":      "freeaicharity",
		"aimusicinteraction.org": "aimusicinteraction",
		"tcbfightfactory.com":    "tcbfightfactory",
		"tcbcombatsports.com":    "tcbcombatsports",
	}

	if key, ok := domainMap[domain]; ok {
		return key
	}

	// Fallback: strip TLD and www
	return domain
}

// SnapshotBaselines captures current metrics as a baseline for future comparison
func (g *GASignalSource) SnapshotBaselines() map[string]map[string]*GAPageMetrics {
	g.mu.RLock()
	defer g.mu.RUnlock()

	snapshot := make(map[string]map[string]*GAPageMetrics)
	for domain, pages := range g.pageMetrics {
		snapshot[domain] = make(map[string]*GAPageMetrics)
		for path, metrics := range pages {
			// Deep copy
			snapshot[domain][path] = &GAPageMetrics{
				Domain:          metrics.Domain,
				Path:            metrics.Path,
				PageViews:       metrics.PageViews,
				EngagedSessions: metrics.EngagedSessions,
				EventCount:      metrics.EventCount,
				LastUpdated:     metrics.LastUpdated,
			}
		}
	}
	return snapshot
}
