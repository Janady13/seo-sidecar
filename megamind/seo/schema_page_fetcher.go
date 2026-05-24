package seo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// PageFetcher retrieves and extracts data from web pages.
type PageFetcher struct {
	httpClient *http.Client
	userAgent  string
	cache      map[string]*FetchResult
	cacheTTL   time.Duration
}

// FetchResult contains fetched page data.
type FetchResult struct {
	URL          string            `json:"url"`
	StatusCode   int               `json:"status_code"`
	ContentHash  string            `json:"content_hash"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Canonical    string            `json:"canonical"`
	Author       string            `json:"author"`
	DatePublished string           `json:"date_published"`
	DateModified string            `json:"date_modified"`
	Language     string            `json:"language"`
	RawHTML      []byte            `json:"-"`
	FAQItems     []FAQItem         `json:"faq_items,omitempty"`
	StructuredHints []StructuredHint `json:"structured_hints,omitempty"`
	FetchedAt    time.Time         `json:"fetched_at"`
	Error        string            `json:"error,omitempty"`
}

// FAQItem represents an extracted Q&A pair.
type FAQItem struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// StructuredHint is a detected schema.org type hint.
type StructuredHint struct {
	Type       string `json:"type"`
	Confidence float64 `json:"confidence"`
	Source     string `json:"source"` // path, content, existing_schema
}

// NewPageFetcher creates a fetcher.
func NewPageFetcher() *PageFetcher {
	return &PageFetcher{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		userAgent: "MEGAMIND-SEO/1.0 (Schema Generator)",
		cache:     make(map[string]*FetchResult),
		cacheTTL:  5 * time.Minute,
	}
}

// Fetch retrieves and parses a page.
func (f *PageFetcher) Fetch(url string) (*FetchResult, error) {
	// Check cache
	if cached := f.getFromCache(url); cached != nil {
		return cached, nil
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return &FetchResult{
			URL:       url,
			Error:     err.Error(),
			FetchedAt: time.Now(),
		}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	result := f.parseHTML(url, resp.StatusCode, body)
	f.putInCache(url, result)

	return result, nil
}

// FetchWithHash fetches and returns both result and content hash.
func (f *PageFetcher) FetchWithHash(url string) (*FetchResult, string, error) {
	result, err := f.Fetch(url)
	if err != nil {
		return result, "", err
	}

	return result, result.ContentHash, nil
}

func (f *PageFetcher) parseHTML(url string, statusCode int, body []byte) *FetchResult {
	result := &FetchResult{
		URL:        url,
		StatusCode: statusCode,
		RawHTML:    body,
		FetchedAt:  time.Now(),
	}

	// Content hash
	hash := sha256.Sum256(body)
	result.ContentHash = hex.EncodeToString(hash[:])

	content := string(body)

	// Title
	if matches := regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`).FindStringSubmatch(content); len(matches) > 1 {
		result.Title = cleanText(matches[1])
	}

	// Meta description
	if matches := regexp.MustCompile(`(?i)<meta[^>]+name=["']description["'][^>]+content=["']([^"']+)["']`).FindStringSubmatch(content); len(matches) > 1 {
		result.Description = cleanText(matches[1])
	}
	// Also try reverse order
	if result.Description == "" {
		if matches := regexp.MustCompile(`(?i)<meta[^>]+content=["']([^"']+)["'][^>]+name=["']description["']`).FindStringSubmatch(content); len(matches) > 1 {
			result.Description = cleanText(matches[1])
		}
	}

	// Canonical URL
	if matches := regexp.MustCompile(`(?i)<link[^>]+rel=["']canonical["'][^>]+href=["']([^"']+)["']`).FindStringSubmatch(content); len(matches) > 1 {
		result.Canonical = matches[1]
	}

	// Author
	if matches := regexp.MustCompile(`(?i)<meta[^>]+name=["']author["'][^>]+content=["']([^"']+)["']`).FindStringSubmatch(content); len(matches) > 1 {
		result.Author = cleanText(matches[1])
	}

	// Date published
	if matches := regexp.MustCompile(`(?i)<meta[^>]+property=["']article:published_time["'][^>]+content=["']([^"']+)["']`).FindStringSubmatch(content); len(matches) > 1 {
		result.DatePublished = matches[1]
	}
	if result.DatePublished == "" {
		if matches := regexp.MustCompile(`(?i)<time[^>]+datetime=["']([^"']+)["']`).FindStringSubmatch(content); len(matches) > 1 {
			result.DatePublished = matches[1]
		}
	}

	// Language
	if matches := regexp.MustCompile(`(?i)<html[^>]+lang=["']([^"']+)["']`).FindStringSubmatch(content); len(matches) > 1 {
		result.Language = matches[1]
	}

	// Extract FAQ items
	result.FAQItems = f.extractFAQ(content)

	// Detect structured hints
	result.StructuredHints = f.detectStructuredHints(url, content)

	return result
}

func (f *PageFetcher) extractFAQ(content string) []FAQItem {
	var items []FAQItem

	// Pattern 1: dt/dd pairs
	dtddPattern := regexp.MustCompile(`(?is)<dt[^>]*>(.*?)</dt>\s*<dd[^>]*>(.*?)</dd>`)
	matches := dtddPattern.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		if len(m) > 2 {
			q := cleanText(stripHTML(m[1]))
			a := cleanText(stripHTML(m[2]))
			if len(q) > 10 && len(a) > 20 {
				items = append(items, FAQItem{Question: q, Answer: a})
			}
		}
	}

	// Pattern 2: FAQ-specific divs
	if len(items) == 0 {
		faqDivPattern := regexp.MustCompile(`(?is)<div[^>]*class=["'][^"']*faq[^"']*["'][^>]*>.*?<h[3-4][^>]*>(.*?)</h[3-4]>\s*<[^>]+>(.*?)</[^>]+>`)
		matches = faqDivPattern.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) > 2 {
				q := cleanText(stripHTML(m[1]))
				a := cleanText(stripHTML(m[2]))
				if len(q) > 10 && len(a) > 20 && strings.Contains(q, "?") {
					items = append(items, FAQItem{Question: q, Answer: a})
				}
			}
		}
	}

	// Pattern 3: Heading with question mark followed by paragraph
	if len(items) == 0 {
		hpPattern := regexp.MustCompile(`(?is)<h[2-4][^>]*>([^<]*\?[^<]*)</h[2-4]>\s*<p[^>]*>(.*?)</p>`)
		matches = hpPattern.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) > 2 {
				q := cleanText(stripHTML(m[1]))
				a := cleanText(stripHTML(m[2]))
				if len(q) > 10 && len(a) > 20 {
					items = append(items, FAQItem{Question: q, Answer: a})
				}
			}
		}
	}

	return items
}

func (f *PageFetcher) detectStructuredHints(url, content string) []StructuredHint {
	var hints []StructuredHint

	// Path-based hints
	pathHints := map[string]string{
		"/about":    "AboutPage",
		"/contact":  "ContactPage",
		"/faq":      "FAQPage",
		"/services": "Service",
		"/blog/":    "BlogPosting",
		"/article/": "Article",
		"/product/": "Product",
		"/event":    "Event",
		"/team":     "ProfilePage",
		"/staff":    "ProfilePage",
	}

	lowerURL := strings.ToLower(url)
	for path, schemaType := range pathHints {
		if strings.Contains(lowerURL, path) {
			hints = append(hints, StructuredHint{
				Type:       schemaType,
				Confidence: 0.7,
				Source:     "path",
			})
		}
	}

	// Content-based hints
	contentHints := []struct {
		pattern    string
		schemaType string
		confidence float64
	}{
		{`<article[^>]*>`, "Article", 0.8},
		{`(?i)class=["'][^"']*faq[^"']*["']`, "FAQPage", 0.85},
		{`(?i)class=["'][^"']*blog[^"']*["']`, "BlogPosting", 0.6},
		{`(?i)<time[^>]+datetime`, "Article", 0.5},
		{`(?i)add.to.cart|buy.now|price`, "Product", 0.7},
		{`(?i)event.date|when:.*\d{4}`, "Event", 0.7},
		{`(?i)ingredients|prep.time|cook.time`, "Recipe", 0.9},
	}

	for _, hint := range contentHints {
		if matched, _ := regexp.MatchString(hint.pattern, content); matched {
			hints = append(hints, StructuredHint{
				Type:       hint.schemaType,
				Confidence: hint.confidence,
				Source:     "content",
			})
		}
	}

	// Existing schema detection
	if strings.Contains(content, "application/ld+json") {
		jsonldPattern := regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
		matches := jsonldPattern.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			if len(m) > 1 {
				// Extract @type
				typePattern := regexp.MustCompile(`"@type"\s*:\s*"([^"]+)"`)
				if typeMatches := typePattern.FindStringSubmatch(m[1]); len(typeMatches) > 1 {
					hints = append(hints, StructuredHint{
						Type:       typeMatches[1],
						Confidence: 1.0,
						Source:     "existing_schema",
					})
				}
			}
		}
	}

	return hints
}

// Cache management

func (f *PageFetcher) getFromCache(url string) *FetchResult {
	result, ok := f.cache[url]
	if !ok {
		return nil
	}

	if time.Since(result.FetchedAt) > f.cacheTTL {
		delete(f.cache, url)
		return nil
	}

	return result
}

func (f *PageFetcher) putInCache(url string, result *FetchResult) {
	f.cache[url] = result

	// Simple cache size limit
	if len(f.cache) > 1000 {
		// Remove oldest entries
		for k, v := range f.cache {
			if time.Since(v.FetchedAt) > f.cacheTTL {
				delete(f.cache, k)
			}
		}
	}
}

// ClearCache removes all cached results.
func (f *PageFetcher) ClearCache() {
	f.cache = make(map[string]*FetchResult)
}

// Helper functions

func cleanText(s string) string {
	// Remove extra whitespace
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func stripHTML(s string) string {
	// Remove HTML tags
	s = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, "")
	// Decode common entities
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return s
}

// GetBestSchemaType returns the highest-confidence schema type hint.
func (r *FetchResult) GetBestSchemaType() string {
	if len(r.StructuredHints) == 0 {
		return "WebPage"
	}

	best := r.StructuredHints[0]
	for _, hint := range r.StructuredHints[1:] {
		if hint.Confidence > best.Confidence {
			best = hint
		}
	}

	return best.Type
}

// HasFAQ returns whether the page has extractable FAQ content.
func (r *FetchResult) HasFAQ() bool {
	return len(r.FAQItems) >= 2
}

// HasExistingSchema returns whether the page already has JSON-LD.
func (r *FetchResult) HasExistingSchema() bool {
	for _, hint := range r.StructuredHints {
		if hint.Source == "existing_schema" {
			return true
		}
	}
	return false
}
