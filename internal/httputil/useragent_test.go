package httputil

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewObservedClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("observed client dropped the User-Agent")
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	var calls atomic.Int32
	var gotCode atomic.Int32
	c := NewObservedClient(time.Second, func(req *http.Request, resp *http.Response, err error, d time.Duration) {
		calls.Add(1)
		if err == nil {
			gotCode.Store(int32(resp.StatusCode))
		}
	})
	resp, err := c.Get(srv.URL + "/api/v3/botx/chats/list")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if calls.Load() != 1 || gotCode.Load() != http.StatusTeapot {
		t.Errorf("observer calls = %d, code = %d", calls.Load(), gotCode.Load())
	}

	// A failed attempt is observed too, with an error and no response.
	var failed atomic.Bool
	c = NewObservedClient(time.Second, func(req *http.Request, resp *http.Response, err error, d time.Duration) {
		failed.Store(err != nil && resp == nil)
	})
	if _, err := c.Get("http://127.0.0.1:1/"); err == nil {
		t.Fatal("request to a closed port succeeded")
	}
	if !failed.Load() {
		t.Error("failed attempt not observed")
	}

	if NewObservedClient(time.Second, nil).Transport == nil {
		t.Error("nil observer left no transport")
	}
}
