package seo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ChangeDetector detects pages that need schema updates.
type ChangeDetector interface {
	DetectChanges() ([]PageObservation, error)
}

// MultiSourceDetector combines multiple detection strategies.
type MultiSourceDetector struct {
	registry   *SiteRegistry
	cache      *StateCache
	httpClient *http.Client
	detectors  []ChangeDetector
}

// NewMultiSourceDetector creates a detector with multiple sources.
func NewMultiSourceDetector(registry *SiteRegistry, cache *StateCache) *MultiSourceDetector {
	return &MultiSourceDetector{
		registry: registry,
		cache:    cache,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// DetectChanges aggregates changes from all sources.
func (d *MultiSourceDetector) DetectChanges() ([]PageObservation, error) {
	var changes []PageObservation

	for _, detector := range d.detectors {
		found, err := detector.DetectChanges()
		if err != nil {
			// Log but continue with other detectors
			continue
		}
		changes = append(changes, found...)
	}

	return d.deduplicate(changes), nil
}

func (d *MultiSourceDetector) deduplicate(obs []PageObservation) []PageObservation {
	seen := make(map[string]bool)
	var result []PageObservation
	for _, o := range obs {
		if !seen[o.URL] {
			seen[o.URL] = true
			result = append(result, o)
		}
	}
	return result
}

// SitemapDetector detects changes via sitemap comparison.
type SitemapDetector struct {
	registry   *SiteRegistry
	cache      *StateCache
	httpClient *http.Client
}

func NewSitemapDetector(registry *SiteRegistry, cache *StateCache) *SitemapDetector {
	return &SitemapDetector{
		registry: registry,
		cache:    cache,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (d *SitemapDetector) DetectChanges() ([]PageObservation, error) {
	var changes []PageObservation

	sites := d.registry.ListSites()
	for _, site := range sites {
		if !d.supportsMode(site, "sitemap") {
			continue
		}

		sitemapURL := site.BaseURL + "/sitemap.xml"
		urls, err := d.parseSitemap(sitemapURL)
		if err != nil {
			continue
		}

		for _, pageURL := range urls {
			hash, err := d.fetchPageHash(pageURL)
			if err != nil {
				continue
			}

			cached := d.cache.Get(pageURL)
			if cached == nil || cached.LastContentHash != hash {
				changes = append(changes, PageObservation{
					URL:         pageURL,
					SiteKey:     site.SiteKey,
					ContentHash: hash,
					LastSeen:    time.Now(),
				})
			}
		}
	}

	return changes, nil
}

func (d *SitemapDetector) supportsMode(site SiteProfile, mode string) bool {
	for _, m := range site.ChangeDetectionModes {
		if m == mode {
			return true
		}
	}
	return false
}

func (d *SitemapDetector) parseSitemap(url string) ([]string, error) {
	// TODO: Implement sitemap XML parsing
	return nil, nil
}

func (d *SitemapDetector) fetchPageHash(url string) (string, error) {
	resp, err := d.httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}

// PageHashDetector detects changes via direct page hash comparison.
type PageHashDetector struct {
	registry   *SiteRegistry
	cache      *StateCache
	httpClient *http.Client
	pageURLs   []string
}

func NewPageHashDetector(registry *SiteRegistry, cache *StateCache, urls []string) *PageHashDetector {
	return &PageHashDetector{
		registry: registry,
		cache:    cache,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		pageURLs: urls,
	}
}

func (d *PageHashDetector) DetectChanges() ([]PageObservation, error) {
	var changes []PageObservation

	for _, url := range d.pageURLs {
		hash, err := d.fetchHash(url)
		if err != nil {
			continue
		}

		cached := d.cache.Get(url)
		if cached == nil || cached.LastContentHash != hash {
			site := d.registry.GetBySiteURL(url)
			siteKey := ""
			if site != nil {
				siteKey = site.SiteKey
			}

			changes = append(changes, PageObservation{
				URL:         url,
				SiteKey:     siteKey,
				ContentHash: hash,
				LastSeen:    time.Now(),
			})
		}
	}

	return changes, nil
}

func (d *PageHashDetector) fetchHash(url string) (string, error) {
	resp, err := d.httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}

// WebhookDetector receives push notifications about changes.
type WebhookDetector struct {
	pending chan PageObservation
}

func NewWebhookDetector() *WebhookDetector {
	return &WebhookDetector{
		pending: make(chan PageObservation, 100),
	}
}

func (d *WebhookDetector) Push(obs PageObservation) {
	select {
	case d.pending <- obs:
	default:
		// Queue full, drop oldest
	}
}

func (d *WebhookDetector) DetectChanges() ([]PageObservation, error) {
	var changes []PageObservation
	for {
		select {
		case obs := <-d.pending:
			changes = append(changes, obs)
		default:
			return changes, nil
		}
	}
}
