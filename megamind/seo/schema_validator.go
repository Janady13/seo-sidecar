package seo

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SchemaValidator validates JSON-LD schemas.
type SchemaValidator struct {
	registry *SiteRegistry
}

// ValidationResult contains validation outcome.
type ValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// NewSchemaValidator creates a validator.
func NewSchemaValidator(registry *SiteRegistry) *SchemaValidator {
	return &SchemaValidator{
		registry: registry,
	}
}

// Validate checks a schema candidate for validity.
func (v *SchemaValidator) Validate(candidate *SchemaCandidate) *ValidationResult {
	result := &ValidationResult{Valid: true}

	// 1. JSON validity
	var parsed map[string]any
	if err := json.Unmarshal([]byte(candidate.JSONLD), &parsed); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("invalid JSON: %v", err))
		return result
	}

	// 2. Schema.org context
	if err := v.validateContext(parsed); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, err.Error())
	}

	// 3. Required type
	if err := v.validateType(parsed, candidate.Type); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, err.Error())
	}

	// 4. Type-specific validation
	typeErrors := v.validateTypeSpecific(parsed, candidate.Type)
	result.Errors = append(result.Errors, typeErrors...)
	if len(typeErrors) > 0 {
		result.Valid = false
	}

	// 5. Site-specific policy
	if site := v.registry.Get(candidate.SiteKey); site != nil {
		policyErrors := v.validatePolicy(parsed, site.ValidationPolicy)
		result.Errors = append(result.Errors, policyErrors...)
		if len(policyErrors) > 0 {
			result.Valid = false
		}
	}

	// 6. Size check
	if len(candidate.JSONLD) > 100000 {
		result.Warnings = append(result.Warnings, "schema exceeds 100KB, may impact page load")
	}

	return result
}

func (v *SchemaValidator) validateContext(schema map[string]any) error {
	ctx, ok := schema["@context"]
	if !ok {
		return fmt.Errorf("missing @context")
	}

	ctxStr, ok := ctx.(string)
	if !ok {
		return fmt.Errorf("@context must be a string")
	}

	if !strings.Contains(ctxStr, "schema.org") {
		return fmt.Errorf("@context must reference schema.org")
	}

	return nil
}

func (v *SchemaValidator) validateType(schema map[string]any, expectedType string) error {
	schemaType, ok := schema["@type"]
	if !ok {
		return fmt.Errorf("missing @type")
	}

	typeStr, ok := schemaType.(string)
	if !ok {
		return fmt.Errorf("@type must be a string")
	}

	if typeStr != expectedType {
		return fmt.Errorf("@type mismatch: expected %s, got %s", expectedType, typeStr)
	}

	return nil
}

func (v *SchemaValidator) validateTypeSpecific(schema map[string]any, schemaType string) []string {
	var errors []string

	switch schemaType {
	case "Article", "BlogPosting":
		errors = v.validateArticle(schema)
	case "FAQPage":
		errors = v.validateFAQ(schema)
	case "Service":
		errors = v.validateService(schema)
	case "Event":
		errors = v.validateEvent(schema)
	case "Product":
		errors = v.validateProduct(schema)
	}

	return errors
}

func (v *SchemaValidator) validateArticle(schema map[string]any) []string {
	var errors []string

	if _, ok := schema["headline"]; !ok {
		errors = append(errors, "Article missing required field: headline")
	}

	return errors
}

func (v *SchemaValidator) validateFAQ(schema map[string]any) []string {
	var errors []string

	mainEntity, ok := schema["mainEntity"]
	if !ok {
		errors = append(errors, "FAQPage missing required field: mainEntity")
		return errors
	}

	items, ok := mainEntity.([]any)
	if !ok {
		errors = append(errors, "FAQPage mainEntity must be an array")
		return errors
	}

	if len(items) == 0 {
		errors = append(errors, "FAQPage mainEntity is empty")
	}

	for i, item := range items {
		q, ok := item.(map[string]any)
		if !ok {
			errors = append(errors, fmt.Sprintf("FAQPage item %d is not an object", i))
			continue
		}

		if _, ok := q["name"]; !ok {
			errors = append(errors, fmt.Sprintf("FAQPage item %d missing question name", i))
		}

		if _, ok := q["acceptedAnswer"]; !ok {
			errors = append(errors, fmt.Sprintf("FAQPage item %d missing acceptedAnswer", i))
		}
	}

	return errors
}

func (v *SchemaValidator) validateService(schema map[string]any) []string {
	var errors []string

	if _, ok := schema["name"]; !ok {
		errors = append(errors, "Service missing required field: name")
	}

	return errors
}

func (v *SchemaValidator) validateEvent(schema map[string]any) []string {
	var errors []string

	if _, ok := schema["name"]; !ok {
		errors = append(errors, "Event missing required field: name")
	}

	// Events should have a start date
	if _, ok := schema["startDate"]; !ok {
		errors = append(errors, "Event missing recommended field: startDate")
	}

	return errors
}

func (v *SchemaValidator) validateProduct(schema map[string]any) []string {
	var errors []string

	if _, ok := schema["name"]; !ok {
		errors = append(errors, "Product missing required field: name")
	}

	return errors
}

func (v *SchemaValidator) validatePolicy(schema map[string]any, policy ValidationPolicy) []string {
	var errors []string

	if policy.RequireMainEntity {
		if _, ok := schema["mainEntity"]; !ok {
			errors = append(errors, "policy requires mainEntity")
		}
	}

	if len(policy.AllowedTypes) > 0 {
		schemaType, _ := schema["@type"].(string)
		allowed := false
		for _, t := range policy.AllowedTypes {
			if t == schemaType {
				allowed = true
				break
			}
		}
		if !allowed {
			errors = append(errors, fmt.Sprintf("schema type %s not in allowed list", schemaType))
		}
	}

	return errors
}

// ValidateJSON checks if a string is valid JSON.
func ValidateJSON(s string) error {
	var js json.RawMessage
	return json.Unmarshal([]byte(s), &js)
}

// PrettyPrint formats JSON-LD for readability.
func PrettyPrint(jsonld string) (string, error) {
	var parsed any
	if err := json.Unmarshal([]byte(jsonld), &parsed); err != nil {
		return "", err
	}

	pretty, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return "", err
	}

	return string(pretty), nil
}
