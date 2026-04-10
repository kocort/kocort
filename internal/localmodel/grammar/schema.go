// Package grammar provides JSON Schema to GBNF grammar conversion.
// This is a pure Go implementation that does not depend on the llama.cpp
// C library, and is used to constrain model output to match a JSON Schema.
package grammar

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SchemaToGrammar converts a JSON Schema (as raw JSON bytes) into a GBNF grammar string.
// Returns nil if the schema is invalid or cannot be converted.
// This is a pure Go reimplementation of llama.cpp's json-schema-to-grammar.cpp.
func SchemaToGrammar(schema []byte) []byte {
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		return nil
	}

	g := &gbnfGenerator{
		rules:     make(map[string]string),
		ruleOrder: nil,
	}

	g.addPrimitiveRules()
	g.visit(parsed, "root")

	result := g.generate()
	if result == "" {
		return nil
	}
	return []byte(result)
}

// gbnfGenerator accumulates GBNF rules while walking a JSON Schema.
type gbnfGenerator struct {
	rules     map[string]string
	ruleOrder []string
	refCount  int
}

func (g *gbnfGenerator) addRule(name, body string) {
	if _, exists := g.rules[name]; !exists {
		g.ruleOrder = append(g.ruleOrder, name)
	}
	g.rules[name] = body
}

func (g *gbnfGenerator) newRuleName(base string) string {
	g.refCount++
	name := fmt.Sprintf("%s-%d", base, g.refCount)
	return name
}

func (g *gbnfGenerator) addPrimitiveRules() {
	g.addRule("ws", `( [ \t\n]+ )?`)
	g.addRule("string", `"\"" ( [^"\\\\\\x7F\\x00-\\x1F] | "\\\\" ( ["\\\\/bfnrt] | "u" [0-9a-fA-F]{4} ) )* "\""`)
	g.addRule("number", `("-"? ( "0" | [1-9] [0-9]* ) ) ( "." [0-9]+ )? ( [eE] [-+]? [0-9]+ )?`)
	g.addRule("integer", `"-"? ( "0" | [1-9] [0-9]* )`)
	g.addRule("boolean", `( "true" | "false" )`)
	g.addRule("null", `"null"`)
	g.addRule("value", `string | number | boolean | null | object | array`)
	g.addRule("object", `"{" ws ( string ":" ws value ( "," ws string ":" ws value )* )? "}" ws`)
	g.addRule("array", `"[" ws ( value ( "," ws value )* )? "]" ws`)
}

func (g *gbnfGenerator) visit(schema map[string]any, name string) string {
	if schema == nil {
		g.addRule(name, "value")
		return name
	}

	// Handle $ref (simple, non-recursive)
	if ref, ok := schema["$ref"].(string); ok {
		_ = ref
		g.addRule(name, "value")
		return name
	}

	// Handle enum
	if enumVals, ok := schema["enum"].([]any); ok {
		return g.visitEnum(enumVals, name)
	}

	// Handle const
	if constVal, ok := schema["const"]; ok {
		return g.visitConst(constVal, name)
	}

	// Handle oneOf
	if oneOf, ok := schema["oneOf"].([]any); ok {
		return g.visitOneOf(oneOf, name)
	}

	// Handle anyOf
	if anyOf, ok := schema["anyOf"].([]any); ok {
		return g.visitOneOf(anyOf, name)
	}

	// Handle type
	typ, _ := schema["type"].(string)
	switch typ {
	case "string":
		return g.visitString(schema, name)
	case "number":
		g.addRule(name, "number")
		return name
	case "integer":
		g.addRule(name, "integer")
		return name
	case "boolean":
		g.addRule(name, "boolean")
		return name
	case "null":
		g.addRule(name, "null")
		return name
	case "object":
		return g.visitObject(schema, name)
	case "array":
		return g.visitArray(schema, name)
	default:
		if _, hasProps := schema["properties"]; hasProps {
			return g.visitObject(schema, name)
		}
		if _, hasItems := schema["items"]; hasItems {
			return g.visitArray(schema, name)
		}
		g.addRule(name, "value")
		return name
	}
}

func (g *gbnfGenerator) visitString(schema map[string]any, name string) string {
	if enumVals, ok := schema["enum"].([]any); ok {
		return g.visitEnum(enumVals, name)
	}
	g.addRule(name, "string")
	return name
}

func (g *gbnfGenerator) visitEnum(vals []any, name string) string {
	var alts []string
	for _, v := range vals {
		switch tv := v.(type) {
		case string:
			alts = append(alts, fmt.Sprintf(`"\"" %s "\""`, escapeGBNF(tv)))
		case float64:
			s := fmt.Sprintf("%g", tv)
			alts = append(alts, fmt.Sprintf(`"%s"`, s))
		case bool:
			if tv {
				alts = append(alts, `"true"`)
			} else {
				alts = append(alts, `"false"`)
			}
		case nil:
			alts = append(alts, `"null"`)
		default:
			b, _ := json.Marshal(v)
			alts = append(alts, fmt.Sprintf(`"%s"`, escapeGBNFStr(string(b))))
		}
	}
	g.addRule(name, strings.Join(alts, " | "))
	return name
}

func (g *gbnfGenerator) visitConst(val any, name string) string {
	b, _ := json.Marshal(val)
	g.addRule(name, fmt.Sprintf(`"%s"`, escapeGBNFStr(string(b))))
	return name
}

func (g *gbnfGenerator) visitOneOf(schemas []any, name string) string {
	var alts []string
	for _, s := range schemas {
		if sMap, ok := s.(map[string]any); ok {
			altName := g.newRuleName(name + "-alt")
			g.visit(sMap, altName)
			alts = append(alts, altName)
		}
	}
	if len(alts) == 0 {
		g.addRule(name, "value")
		return name
	}
	g.addRule(name, strings.Join(alts, " | "))
	return name
}

func (g *gbnfGenerator) visitObject(schema map[string]any, name string) string {
	props, _ := schema["properties"].(map[string]any)
	requiredList, _ := schema["required"].([]any)

	required := make(map[string]bool)
	for _, r := range requiredList {
		if s, ok := r.(string); ok {
			required[s] = true
		}
	}

	if len(props) == 0 {
		g.addRule(name, `"{" ws ( string ":" ws value ( "," ws string ":" ws value )* )? "}" ws`)
		return name
	}

	propNames := make([]string, 0, len(props))
	for k := range props {
		propNames = append(propNames, k)
	}
	sort.Strings(propNames)

	propRules := make(map[string]string)
	for _, propName := range propNames {
		propSchema, ok := props[propName].(map[string]any)
		if !ok {
			propSchema = map[string]any{}
		}
		ruleName := g.newRuleName(name + "-" + sanitizeName(propName))
		g.visit(propSchema, ruleName)
		propRules[propName] = ruleName
	}

	var parts []string
	for i, propName := range propNames {
		propStr := fmt.Sprintf(`"\"" "%s" "\"" ":" ws %s`, escapeGBNF(propName), propRules[propName])
		if !required[propName] {
			if i > 0 {
				propStr = fmt.Sprintf(`( "," ws %s )?`, propStr)
			} else {
				propStr = fmt.Sprintf(`( %s )?`, propStr)
			}
		} else {
			if i > 0 {
				propStr = fmt.Sprintf(`"," ws %s`, propStr)
			}
		}
		parts = append(parts, propStr)
	}

	body := fmt.Sprintf(`"{" ws %s "}" ws`, strings.Join(parts, " "))
	g.addRule(name, body)
	return name
}

func (g *gbnfGenerator) visitArray(schema map[string]any, name string) string {
	items, hasItems := schema["items"].(map[string]any)

	if !hasItems {
		g.addRule(name, `"[" ws ( value ( "," ws value )* )? "]" ws`)
		return name
	}

	itemRule := g.newRuleName(name + "-item")
	g.visit(items, itemRule)
	g.addRule(name, fmt.Sprintf(`"[" ws ( %s ( "," ws %s )* )? "]" ws`, itemRule, itemRule))
	return name
}

func (g *gbnfGenerator) generate() string {
	if len(g.rules) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, name := range g.ruleOrder {
		body := g.rules[name]
		fmt.Fprintf(&sb, "%s ::= %s\n", name, body)
	}
	return sb.String()
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func escapeGBNF(s string) string {
	var sb strings.Builder
	for _, ch := range s {
		switch ch {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

func escapeGBNFStr(s string) string {
	var sb strings.Builder
	for _, ch := range s {
		switch ch {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		default:
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

func sanitizeName(s string) string {
	var sb strings.Builder
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			sb.WriteRune(ch)
		} else {
			sb.WriteRune('-')
		}
	}
	return sb.String()
}
