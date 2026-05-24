package seo

import (
	"regexp"
	"strings"
)

// PageClassifier determines the schema type for a page.
type PageClassifier interface {
	Classify(content []byte, url string) string
}

// RuleBasedClassifier uses heuristics to classify pages.
type RuleBasedClassifier struct {
	registry *SiteRegistry
	rules    []ClassificationRule
}

// ClassificationRule defines a classification heuristic.
type ClassificationRule struct {
	Name        string
	Priority    int
	PathPattern *regexp.Regexp
	ContentPattern *regexp.Regexp
	SchemaType  string
}

// NewRuleBasedClassifier creates a classifier with default rules.
func NewRuleBasedClassifier(registry *SiteRegistry) *RuleBasedClassifier {
	c := &RuleBasedClassifier{
		registry: registry,
	}
	c.initDefaultRules()
	return c
}

func (c *RuleBasedClassifier) initDefaultRules() {
	c.rules = []ClassificationRule{
		// High priority: specific content patterns
		{
			Name:           "faq-content",
			Priority:       100,
			ContentPattern: regexp.MustCompile(`(?i)(<dt[^>]*>.*?</dt>\s*<dd|class=["']faq|id=["']faq|question.*answer)`),
			SchemaType:     "FAQPage",
		},
		{
			Name:           "article-tag",
			Priority:       90,
			ContentPattern: regexp.MustCompile(`<article[^>]*>`),
			SchemaType:     "Article",
		},
		{
			Name:           "blog-date",
			Priority:       85,
			ContentPattern: regexp.MustCompile(`(?i)(published|posted|date).*\d{4}`),
			PathPattern:    regexp.MustCompile(`(?i)(blog|post|article|news)`),
			SchemaType:     "BlogPosting",
		},
		{
			Name:           "event-date",
			Priority:       80,
			ContentPattern: regexp.MustCompile(`(?i)(event|when|date|time|location|venue).*\d{1,2}[/-]\d{1,2}`),
			PathPattern:    regexp.MustCompile(`(?i)(event|calendar|schedule)`),
			SchemaType:     "Event",
		},

		// Medium priority: path patterns
		{
			Name:        "about-path",
			Priority:    50,
			PathPattern: regexp.MustCompile(`(?i)/about`),
			SchemaType:  "AboutPage",
		},
		{
			Name:        "contact-path",
			Priority:    50,
			PathPattern: regexp.MustCompile(`(?i)/contact`),
			SchemaType:  "ContactPage",
		},
		{
			Name:        "services-path",
			Priority:    50,
			PathPattern: regexp.MustCompile(`(?i)/services?`),
			SchemaType:  "Service",
		},
		{
			Name:        "faq-path",
			Priority:    50,
			PathPattern: regexp.MustCompile(`(?i)/faq`),
			SchemaType:  "FAQPage",
		},
		{
			Name:        "team-path",
			Priority:    45,
			PathPattern: regexp.MustCompile(`(?i)/(team|staff|people)`),
			SchemaType:  "ProfilePage",
		},
		{
			Name:        "pricing-path",
			Priority:    45,
			PathPattern: regexp.MustCompile(`(?i)/pricing`),
			SchemaType:  "WebPage",
		},

		// Low priority: generic patterns
		{
			Name:           "product-pattern",
			Priority:       30,
			ContentPattern: regexp.MustCompile(`(?i)(price|buy|add.to.cart|\$\d+)`),
			SchemaType:     "Product",
		},
		{
			Name:           "recipe-pattern",
			Priority:       30,
			ContentPattern: regexp.MustCompile(`(?i)(ingredients|instructions|prep.time|cook.time)`),
			SchemaType:     "Recipe",
		},
	}
}

// Classify determines the best schema type for the content.
func (c *RuleBasedClassifier) Classify(content []byte, url string) string {
	contentStr := string(content)

	// Check site-specific rules first
	if site := c.registry.GetBySiteURL(url); site != nil {
		for _, rule := range site.PagePatternRules {
			if matched, _ := regexp.MatchString(rule.Pattern, url); matched {
				return rule.SchemaType
			}
		}
	}

	// Apply general rules by priority
	var bestMatch string
	bestPriority := -1

	for _, rule := range c.rules {
		if rule.Priority <= bestPriority {
			continue
		}

		pathMatch := rule.PathPattern == nil || rule.PathPattern.MatchString(url)
		contentMatch := rule.ContentPattern == nil || rule.ContentPattern.MatchString(contentStr)

		if pathMatch && contentMatch {
			// Both patterns must match if both are specified
			if rule.PathPattern != nil && rule.ContentPattern != nil {
				bestMatch = rule.SchemaType
				bestPriority = rule.Priority
			} else if rule.ContentPattern != nil && rule.ContentPattern.MatchString(contentStr) {
				bestMatch = rule.SchemaType
				bestPriority = rule.Priority
			} else if rule.PathPattern != nil && rule.PathPattern.MatchString(url) {
				bestMatch = rule.SchemaType
				bestPriority = rule.Priority
			}
		}
	}

	if bestMatch != "" {
		return bestMatch
	}

	// Default to WebPage
	return "WebPage"
}

// AddRule adds a custom classification rule.
func (c *RuleBasedClassifier) AddRule(rule ClassificationRule) {
	c.rules = append(c.rules, rule)
}

// ExtractPageMetadata extracts useful metadata from page content.
func ExtractPageMetadata(content []byte) map[string]string {
	meta := make(map[string]string)
	contentStr := string(content)

	// Extract title
	if matches := regexp.MustCompile(`<title>([^<]+)</title>`).FindStringSubmatch(contentStr); len(matches) > 1 {
		meta["title"] = strings.TrimSpace(matches[1])
	}

	// Extract meta description
	if matches := regexp.MustCompile(`<meta[^>]+name=["']description["'][^>]+content=["']([^"']+)["']`).FindStringSubmatch(contentStr); len(matches) > 1 {
		meta["description"] = strings.TrimSpace(matches[1])
	}

	// Extract canonical URL
	if matches := regexp.MustCompile(`<link[^>]+rel=["']canonical["'][^>]+href=["']([^"']+)["']`).FindStringSubmatch(contentStr); len(matches) > 1 {
		meta["canonical"] = strings.TrimSpace(matches[1])
	}

	// Extract author
	if matches := regexp.MustCompile(`<meta[^>]+name=["']author["'][^>]+content=["']([^"']+)["']`).FindStringSubmatch(contentStr); len(matches) > 1 {
		meta["author"] = strings.TrimSpace(matches[1])
	}

	// Extract publish date
	if matches := regexp.MustCompile(`(?i)<time[^>]+datetime=["']([^"']+)["']`).FindStringSubmatch(contentStr); len(matches) > 1 {
		meta["datePublished"] = strings.TrimSpace(matches[1])
	}

	return meta
}

// ExtractFAQItems extracts Q&A pairs from page content.
func ExtractFAQItems(content []byte) []map[string]string {
	var items []map[string]string
	contentStr := string(content)

	// Pattern 1: dt/dd pairs
	dtddPattern := regexp.MustCompile(`(?is)<dt[^>]*>(.*?)</dt>\s*<dd[^>]*>(.*?)</dd>`)
	matches := dtddPattern.FindAllStringSubmatch(contentStr, -1)
	for _, m := range matches {
		if len(m) > 2 {
			items = append(items, map[string]string{
				"question": stripHTML(m[1]),
				"answer":   stripHTML(m[2]),
			})
		}
	}

	// Pattern 2: heading + paragraph
	if len(items) == 0 {
		hpPattern := regexp.MustCompile(`(?is)<h[3-4][^>]*>(.*?)</h[3-4]>\s*<p[^>]*>(.*?)</p>`)
		matches = hpPattern.FindAllStringSubmatch(contentStr, -1)
		for _, m := range matches {
			if len(m) > 2 && strings.Contains(m[1], "?") {
				items = append(items, map[string]string{
					"question": stripHTML(m[1]),
					"answer":   stripHTML(m[2]),
				})
			}
		}
	}

	return items
}

// stripHTML is defined in schema_page_fetcher.go
