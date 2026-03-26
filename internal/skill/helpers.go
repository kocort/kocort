package skill

// asString extracts a string from an interface value.
func asString(value any) string {
	raw, _ := value.(string) // zero value fallback is intentional
	return raw
}
