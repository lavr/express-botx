package server

import (
	"net/http"
	"net/url"
)

const QueryKeyParam = "api_key"

func queryKey(r *http.Request) string {
	if v, ok := r.Context().Value(queryKeyCtxKey).(string); ok {
		return v
	}
	return ""
}

func stripQueryKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery == "" {
			next.ServeHTTP(w, r)
			return
		}

		values, err := url.ParseQuery(r.URL.RawQuery)
		present := values[QueryKeyParam]
		if err == nil && len(present) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		key := ""
		if err == nil && len(present) == 1 {
			key = present[0]
		}
		values.Del(QueryKeyParam)

		cleaned := r.Clone(r.Context())
		cleaned.URL.RawQuery = values.Encode()
		cleaned.RequestURI = cleaned.URL.RequestURI()
		if key != "" {
			cleaned = cleaned.WithContext(withQueryKey(cleaned.Context(), key))
		}

		next.ServeHTTP(w, cleaned)
	})
}
