package httputil

import "strings"

// ResolveBaseURL normalizes a host into a full base URL.
// If host starts with http:// or https://, it is used as-is (with trailing slash removed).
// Otherwise, https:// is prepended.
func ResolveBaseURL(host string) string {
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return strings.TrimRight(host, "/")
	}
	return "https://" + host
}
