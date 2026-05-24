package seo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"time"
)

// SchemaGenerator creates JSON-LD schemas from page content.
type SchemaGenerator struct {
	registry     *SiteRegistry
	templateDir  string
	templates    map[string]*template.Template
}

// NewSchemaGenerator creates a generator with template support.
func NewSchemaGenerator(registry *SiteRegistry, templateDir string) (*SchemaGenerator, error) {
	g := &SchemaGenerator{
		registry:    registry,
		templateDir: templateDir,
		templates:   make(map[string]*template.Template),
	}

	if err := g.loadTemplates(); err != nil {
		return nil, err
	}

	return g, nil
}

func (g *SchemaGenerator) loadTemplates() error {
	schemaTypes := []string{"article", "faq", "service", "profile", "event", "product", "webpage"}

	for _, st := range schemaTypes {
		path := filepath.Join(g.templateDir, st+".jsonld")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// Template doesn't exist, will use default
			continue
		}

		tmpl, err := template.ParseFiles(path)
		if err != nil {
			return fmt.Errorf("parse template %s: %w", st, err)
		}
		g.templates[st] = tmpl
	}

	return nil
}

// Generate creates a SchemaCandidate for the given page.
func (g *SchemaGenerator) Generate(schemaType string, content []byte, url string, siteKey string) (*SchemaCandidate, error) {
	meta := ExtractPageMetadata(content)
	site := g.registry.Get(siteKey)

	data := g.buildTemplateData(schemaType, meta, url, site)

	var jsonld string
	var err error

	// Try template first
	if tmpl, ok := g.templates[normalizeType(schemaType)]; ok {
		var buf bytes.Buffer
		if err = tmpl.Execute(&buf, data); err == nil {
			jsonld = buf.String()
		}
	}

	// Fall back to programmatic generation
	if jsonld == "" {
		jsonld, err = g.generateDefault(schemaType, data, content)
		if err != nil {
			return nil, err
		}
	}

	schemaKey := g.deriveSchemaKey(siteKey, url, site)

	return &SchemaCandidate{
		URL:         url,
		SiteKey:     siteKey,
		SchemaKey:   schemaKey,
		JSONLD:      jsonld,
		Type:        schemaType,
		GeneratedAt: time.Now(),
	}, nil
}

func (g *SchemaGenerator) buildTemplateData(schemaType string, meta map[string]string, url string, site *SiteProfile) map[string]any {
	data := map[string]any{
		"url":           url,
		"title":         meta["title"],
		"description":   meta["description"],
		"canonical":     meta["canonical"],
		"author":        meta["author"],
		"datePublished": meta["datePublished"],
		"dateModified":  time.Now().Format(time.RFC3339),
		"schemaType":    schemaType,
	}

	if site != nil {
		data["siteKey"] = site.SiteKey
		data["baseURL"] = site.BaseURL
		data["siteCategory"] = site.SiteCategory
	}

	if data["canonical"] == "" {
		data["canonical"] = url
	}

	return data
}

func (g *SchemaGenerator) deriveSchemaKey(siteKey, url string, site *SiteProfile) string {
	if site == nil || site.SidecarKeyStrategy == "" || site.SidecarKeyStrategy == "site-only" {
		return siteKey
	}

	// Path-based key strategy
	if site.SidecarKeyStrategy == "site-path" {
		path := extractPath(url)
		if path != "" && path != "/" {
			return siteKey + "-" + sanitizePath(path)
		}
	}

	return siteKey
}

func (g *SchemaGenerator) generateDefault(schemaType string, data map[string]any, content []byte) (string, error) {
	var schema map[string]any

	switch schemaType {
	case "Article", "BlogPosting":
		schema = g.generateArticle(data)
	case "FAQPage":
		schema = g.generateFAQ(data, content)
	case "Service":
		schema = g.generateService(data)
	case "ProfilePage", "AboutPage":
		schema = g.generateProfile(data)
	case "Event":
		schema = g.generateEvent(data)
	case "Product":
		schema = g.generateProduct(data)
	default:
		schema = g.generateWebPage(data)
	}

	jsonBytes, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}

func (g *SchemaGenerator) generateArticle(data map[string]any) map[string]any {
	return map[string]any{
		"@context": "https://schema.org",
		"@type":    "Article",
		"headline": data["title"],
		"url":      data["canonical"],
		"author": map[string]any{
			"@type": "Person",
			"name":  data["author"],
		},
		"datePublished": data["datePublished"],
		"dateModified":  data["dateModified"],
		"description":   data["description"],
	}
}

func (g *SchemaGenerator) generateFAQ(data map[string]any, content []byte) map[string]any {
	items := ExtractFAQItems(content)
	mainEntity := make([]map[string]any, 0, len(items))

	for _, item := range items {
		mainEntity = append(mainEntity, map[string]any{
			"@type": "Question",
			"name":  item["question"],
			"acceptedAnswer": map[string]any{
				"@type": "Answer",
				"text":  item["answer"],
			},
		})
	}

	return map[string]any{
		"@context":   "https://schema.org",
		"@type":      "FAQPage",
		"mainEntity": mainEntity,
	}
}

func (g *SchemaGenerator) generateService(data map[string]any) map[string]any {
	return map[string]any{
		"@context":    "https://schema.org",
		"@type":       "Service",
		"name":        data["title"],
		"url":         data["canonical"],
		"description": data["description"],
		"provider": map[string]any{
			"@type": "Organization",
			"url":   data["baseURL"],
		},
	}
}

func (g *SchemaGenerator) generateProfile(data map[string]any) map[string]any {
	return map[string]any{
		"@context":    "https://schema.org",
		"@type":       "ProfilePage",
		"name":        data["title"],
		"url":         data["canonical"],
		"description": data["description"],
	}
}

func (g *SchemaGenerator) generateEvent(data map[string]any) map[string]any {
	return map[string]any{
		"@context":    "https://schema.org",
		"@type":       "Event",
		"name":        data["title"],
		"url":         data["canonical"],
		"description": data["description"],
	}
}

func (g *SchemaGenerator) generateProduct(data map[string]any) map[string]any {
	return map[string]any{
		"@context":    "https://schema.org",
		"@type":       "Product",
		"name":        data["title"],
		"url":         data["canonical"],
		"description": data["description"],
	}
}

func (g *SchemaGenerator) generateWebPage(data map[string]any) map[string]any {
	return map[string]any{
		"@context":    "https://schema.org",
		"@type":       "WebPage",
		"name":        data["title"],
		"url":         data["canonical"],
		"description": data["description"],
	}
}

func normalizeType(t string) string {
	switch t {
	case "Article", "BlogPosting":
		return "article"
	case "FAQPage":
		return "faq"
	case "Service":
		return "service"
	case "ProfilePage", "AboutPage":
		return "profile"
	case "Event":
		return "event"
	case "Product":
		return "product"
	default:
		return "webpage"
	}
}

func extractPath(url string) string {
	// Simple path extraction
	for i, c := range url {
		if c == '/' && i > 8 { // after https://
			return url[i:]
		}
	}
	return "/"
}

func sanitizePath(path string) string {
	// Remove leading slash and replace remaining with dashes
	result := ""
	for _, c := range path {
		if c == '/' {
			if result != "" {
				result += "-"
			}
		} else if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			result += string(c)
		}
	}
	return result
}
