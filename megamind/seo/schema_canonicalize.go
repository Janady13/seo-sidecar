package seo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// Canonicalizer normalizes JSON-LD for comparison.
type Canonicalizer struct {
	// Fields to ignore during comparison
	IgnoreFields []string
}

// NewCanonicalizer creates a canonicalizer with default ignored fields.
func NewCanonicalizer() *Canonicalizer {
	return &Canonicalizer{
		IgnoreFields: []string{
			"dateModified",
			"updated_at",
			"lastModified",
			"timestamp",
			"generatedAt",
		},
	}
}

// Canonicalize normalizes JSON-LD for comparison.
func (c *Canonicalizer) Canonicalize(jsonld []byte) ([]byte, error) {
	var parsed any
	if err := json.Unmarshal(jsonld, &parsed); err != nil {
		return nil, err
	}

	normalized := c.normalizeValue(parsed)
	return json.Marshal(normalized)
}

// Hash returns a SHA256 hash of the canonicalized JSON-LD.
func (c *Canonicalizer) Hash(jsonld []byte) (string, error) {
	canonical, err := c.Canonicalize(jsonld)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

// Equal compares two JSON-LD documents for semantic equality.
func (c *Canonicalizer) Equal(a, b []byte) (bool, error) {
	hashA, err := c.Hash(a)
	if err != nil {
		return false, err
	}

	hashB, err := c.Hash(b)
	if err != nil {
		return false, err
	}

	return hashA == hashB, nil
}

// DiffScore returns a similarity score between 0 and 1.
func (c *Canonicalizer) DiffScore(oldJSONLD, newJSONLD []byte) (float64, error) {
	var oldParsed, newParsed any
	if err := json.Unmarshal(oldJSONLD, &oldParsed); err != nil {
		return 1.0, err
	}
	if err := json.Unmarshal(newJSONLD, &newParsed); err != nil {
		return 1.0, err
	}

	oldFields := c.extractFields(oldParsed, "")
	newFields := c.extractFields(newParsed, "")

	// Calculate Jaccard similarity
	intersection := 0
	for k, v := range oldFields {
		if newFields[k] == v {
			intersection++
		}
	}

	union := len(oldFields)
	for k := range newFields {
		if _, ok := oldFields[k]; !ok {
			union++
		}
	}

	if union == 0 {
		return 0, nil
	}

	similarity := float64(intersection) / float64(union)
	return 1 - similarity, nil // Return diff score (0 = same, 1 = completely different)
}

func (c *Canonicalizer) normalizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return c.normalizeObject(val)
	case []any:
		return c.normalizeArray(val)
	case string:
		return strings.TrimSpace(val)
	default:
		return val
	}
}

func (c *Canonicalizer) normalizeObject(obj map[string]any) map[string]any {
	result := make(map[string]any)

	// Get sorted keys
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if !c.shouldIgnore(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		result[k] = c.normalizeValue(obj[k])
	}

	return result
}

func (c *Canonicalizer) normalizeArray(arr []any) []any {
	result := make([]any, len(arr))
	for i, v := range arr {
		result[i] = c.normalizeValue(v)
	}

	// Sort arrays of primitives for consistent ordering
	if c.isPrimitiveArray(result) {
		sort.Slice(result, func(i, j int) bool {
			return c.primitiveString(result[i]) < c.primitiveString(result[j])
		})
	}

	return result
}

func (c *Canonicalizer) shouldIgnore(field string) bool {
	for _, ignored := range c.IgnoreFields {
		if strings.EqualFold(field, ignored) {
			return true
		}
	}
	return false
}

func (c *Canonicalizer) isPrimitiveArray(arr []any) bool {
	for _, v := range arr {
		switch v.(type) {
		case map[string]any, []any:
			return false
		}
	}
	return true
}

func (c *Canonicalizer) primitiveString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}

func (c *Canonicalizer) extractFields(v any, prefix string) map[string]string {
	fields := make(map[string]string)

	switch val := v.(type) {
	case map[string]any:
		for k, v := range val {
			if c.shouldIgnore(k) {
				continue
			}
			newPrefix := prefix + "." + k
			if prefix == "" {
				newPrefix = k
			}
			for fk, fv := range c.extractFields(v, newPrefix) {
				fields[fk] = fv
			}
		}
	case []any:
		for i, v := range val {
			newPrefix := prefix + "[" + string(rune('0'+i)) + "]"
			for fk, fv := range c.extractFields(v, newPrefix) {
				fields[fk] = fv
			}
		}
	default:
		fields[prefix] = c.primitiveString(val)
	}

	return fields
}

// CanonicalHash is a convenience function for quick hashing.
func CanonicalHash(jsonld []byte) (string, error) {
	c := NewCanonicalizer()
	return c.Hash(jsonld)
}

// CanonicalEqual is a convenience function for quick comparison.
func CanonicalEqual(a, b []byte) (bool, error) {
	c := NewCanonicalizer()
	return c.Equal(a, b)
}
