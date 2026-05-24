package seo

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
)

// SiteRegistry stores configuration for all managed sites.
type SiteRegistry struct {
	mu       sync.RWMutex
	sites    map[string]*SiteProfile
	filePath string
}

// NewSiteRegistry creates a site registry.
func NewSiteRegistry(filePath string) *SiteRegistry {
	registry := &SiteRegistry{
		sites:    make(map[string]*SiteProfile),
		filePath: filePath,
	}

	registry.load()

	return registry
}

// Get retrieves a site profile by key.
func (r *SiteRegistry) Get(siteKey string) *SiteProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	site, ok := r.sites[siteKey]
	if !ok {
		return nil
	}

	// Return a copy
	copy := *site
	return &copy
}

// GetBySiteURL finds a site by URL.
func (r *SiteRegistry) GetBySiteURL(url string) *SiteProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, site := range r.sites {
		if strings.HasPrefix(url, site.BaseURL) {
			copy := *site
			return &copy
		}
	}

	return nil
}

// Register adds or updates a site profile.
func (r *SiteRegistry) Register(site *SiteProfile) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sites[site.SiteKey] = site
}

// Unregister removes a site.
func (r *SiteRegistry) Unregister(siteKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.sites, siteKey)
}

// ListSites returns all registered sites.
func (r *SiteRegistry) ListSites() []SiteProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]SiteProfile, 0, len(r.sites))
	for _, site := range r.sites {
		result = append(result, *site)
	}

	return result
}

// ListByCategory returns sites matching a category.
func (r *SiteRegistry) ListByCategory(category string) []SiteProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []SiteProfile
	for _, site := range r.sites {
		if site.SiteCategory == category {
			result = append(result, *site)
		}
	}

	return result
}

// ListBySchemaType returns sites that support a schema type.
func (r *SiteRegistry) ListBySchemaType(schemaType string) []SiteProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []SiteProfile
	for _, site := range r.sites {
		for _, t := range site.AllowedSchemaTypes {
			if t == schemaType {
				result = append(result, *site)
				break
			}
		}
	}

	return result
}

// SupportsSchemaType checks if a site supports a schema type.
func (r *SiteRegistry) SupportsSchemaType(siteKey, schemaType string) bool {
	site := r.Get(siteKey)
	if site == nil {
		return false
	}

	// If no allowed types specified, allow all
	if len(site.AllowedSchemaTypes) == 0 {
		return true
	}

	for _, t := range site.AllowedSchemaTypes {
		if t == schemaType {
			return true
		}
	}

	return false
}

// GetPagePattern finds a matching page pattern rule.
func (r *SiteRegistry) GetPagePattern(siteKey, path string) *PagePatternRule {
	site := r.Get(siteKey)
	if site == nil {
		return nil
	}

	for _, rule := range site.PagePatternRules {
		if matchPattern(rule.Pattern, path) {
			return &rule
		}
	}

	return nil
}

func matchPattern(pattern, path string) bool {
	// Simple pattern matching
	// Supports * wildcards and exact matches
	if pattern == "*" {
		return true
	}

	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(path, prefix)
	}

	return pattern == path
}

// Save persists the registry to disk.
func (r *SiteRegistry) Save() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.filePath == "" {
		return nil
	}

	data, err := json.MarshalIndent(r.sites, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(r.filePath, data, 0644)
}

func (r *SiteRegistry) load() error {
	if r.filePath == "" {
		return nil
	}

	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, &r.sites)
}

// LoadDefaults loads the default site configurations.
func (r *SiteRegistry) LoadDefaults() {
	defaults := []SiteProfile{
		{
			SiteKey:      "thatdeveloperguy",
			BaseURL:      "https://thatdeveloperguy.com",
			SiteCategory: "professional-service",
			AllowedSchemaTypes: []string{
				"WebSite", "Organization", "Person", "ProfilePage",
				"Article", "BlogPosting", "FAQPage", "Service",
			},
			PagePatternRules: []PagePatternRule{
				{Pattern: "/about*", SchemaType: "ProfilePage", SchemaKey: "thatdeveloperguy-about"},
				{Pattern: "/blog/*", SchemaType: "BlogPosting"},
				{Pattern: "/services*", SchemaType: "Service"},
			},
			SidecarKeyStrategy:   "site-path",
			ChangeDetectionModes: []string{"sitemap", "hash"},
			PushPolicy: PushPolicy{
				MinDiffScore:      0.05,
				CooldownMinutes:   60,
				RequireValidation: true,
			},
			Priority: 10,
		},
		{
			SiteKey:      "thatcoputerdude",
			BaseURL:      "https://thatcoputerdude.com",
			SiteCategory: "local-business",
			AllowedSchemaTypes: []string{
				"WebSite", "LocalBusiness", "Service", "FAQPage",
			},
			PagePatternRules: []PagePatternRule{
				{Pattern: "/services*", SchemaType: "Service", SchemaKey: "thatcoputerdude-services"},
				{Pattern: "/faq*", SchemaType: "FAQPage", SchemaKey: "thatcoputerdude-faq"},
			},
			SidecarKeyStrategy:   "site-path",
			ChangeDetectionModes: []string{"hash"},
			PushPolicy: PushPolicy{
				MinDiffScore:      0.05,
				CooldownMinutes:   60,
				RequireValidation: true,
			},
			Priority: 8,
		},
		{
			SiteKey:      "tcbfightfactory",
			BaseURL:      "https://tcbfightfactory.com",
			SiteCategory: "sports-facility",
			AllowedSchemaTypes: []string{
				"WebSite", "SportsActivityLocation", "Event", "Service",
			},
			PagePatternRules: []PagePatternRule{
				{Pattern: "/classes*", SchemaType: "Service", SchemaKey: "tcbfightfactory-classes"},
				{Pattern: "/events*", SchemaType: "Event"},
			},
			SidecarKeyStrategy:   "site-path",
			ChangeDetectionModes: []string{"hash"},
			PushPolicy: PushPolicy{
				MinDiffScore:      0.05,
				CooldownMinutes:   30,
				RequireValidation: true,
			},
			Priority: 7,
		},
		{
			SiteKey:      "freeaicharity",
			BaseURL:      "https://freeaicharity.org",
			SiteCategory: "nonprofit",
			AllowedSchemaTypes: []string{
				"WebSite", "NGO", "Article", "FAQPage",
			},
			PagePatternRules: []PagePatternRule{
				{Pattern: "/about*", SchemaType: "WebPage", SchemaKey: "freeaicharity-about"},
			},
			SidecarKeyStrategy:   "site-path",
			ChangeDetectionModes: []string{"hash"},
			PushPolicy: PushPolicy{
				MinDiffScore:      0.05,
				CooldownMinutes:   120,
				RequireValidation: true,
			},
			Priority: 6,
		},
	}

	for _, site := range defaults {
		r.Register(&site)
	}
}

// Count returns the number of registered sites.
func (r *SiteRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.sites)
}
