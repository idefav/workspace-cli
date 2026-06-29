package workspace

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"unicode"
)

func Slugify(input string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(input)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug != "" {
		return slug
	}
	sum := sha1.Sum([]byte(strings.TrimSpace(input)))
	return "req-" + hex.EncodeToString(sum[:])[:8]
}

func FeatureBranch(slug string) string {
	return "feature/" + slug
}
