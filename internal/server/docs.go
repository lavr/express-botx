package server

import (
	"embed"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

//go:embed api
var apiFS embed.FS

// docsHandler serves Swagger UI and the OpenAPI spec.
// GET /docs            → docs.html (Swagger UI)
// GET /docs/openapi.yaml → OpenAPI spec with host resolved from externalURL,
//
//	X-Forwarded-* headers, or Host header.
func docsHandler(externalURL string) http.Handler {
	sub, _ := fs.Sub(apiFS, "api")
	spec, _ := fs.ReadFile(sub, "openapi.yaml")
	specStr := string(spec)

	// Pre-parse external URL if configured.
	var extScheme, extHost string
	if externalURL != "" {
		if u, err := url.Parse(externalURL); err == nil {
			extScheme = u.Scheme
			extHost = u.Host
		}
	}

	mux := http.NewServeMux()
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		scheme, host := resolveOrigin(r, extScheme, extHost)
		body := specStr
		body = strings.Replace(body, "default: localhost:8080", "default: "+host, 1)
		body = strings.Replace(body, "default: http\n", "default: "+scheme+"\n", 1)
		w.Header().Set("Content-Type", "application/yaml")
		w.Write([]byte(body))
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			r.URL.Path = "/docs.html"
		}
		fileServer.ServeHTTP(w, r)
	})
	return mux
}

// resolveOrigin determines the scheme and host for the OpenAPI spec.
// Priority: externalURL > X-Forwarded-* headers > Host header.
func resolveOrigin(r *http.Request, extScheme, extHost string) (scheme, host string) {
	if extHost != "" {
		scheme = extScheme
		host = extHost
		if scheme == "" {
			scheme = "http"
		}
		return
	}

	// X-Forwarded-* headers (set by nginx ingress)
	host = r.Header.Get("X-Forwarded-Host")
	scheme = r.Header.Get("X-Forwarded-Proto")

	if host == "" {
		host = r.Host
	}
	if host == "" {
		host = "localhost:8080"
	}
	if scheme == "" {
		scheme = "http"
	}

	// Strip default ports
	if scheme == "http" {
		host = strings.TrimSuffix(host, ":80")
	} else if scheme == "https" {
		host = strings.TrimSuffix(host, ":443")
	}

	return
}
