package botx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testBotID  = "8b0f6c1e-3d2a-4f5b-9c7d-1e2f3a4b5c6d"
	testSecret = "test-secret"
	testChatID = "0c8f1d2e-7a6b-4c5d-8e9f-a1b2c3d4e5f6"
	testUserID = "5e4d3c2b-1a09-4f8e-9d7c-6b5a4f3e2d1c"
)

// fakeBotX serves the token endpoint and records which token every other request carried.
type fakeBotX struct {
	t          *testing.T
	mu         sync.Mutex
	issued     int
	tokenCodes []int
	authSeen   []string
	handler    func(w http.ResponseWriter, r *http.Request, token string)
}

func (f *fakeBotX) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/v2/botx/bots/") {
		f.mu.Lock()
		f.issued++
		n := f.issued
		code := http.StatusOK
		if len(f.tokenCodes) >= n {
			code = f.tokenCodes[n-1]
		}
		f.mu.Unlock()
		if r.URL.Path != "/api/v2/botx/bots/"+testBotID+"/token" {
			f.t.Errorf("token path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("signature"); got != BuildSignature(testBotID, testSecret) {
			f.t.Errorf("signature = %q", got)
		}
		w.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = fmt.Fprintf(w, `{"status":"ok","result":"tok-%d"}`, n)
		}
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	f.authSeen = append(f.authSeen, tok)
	f.mu.Unlock()
	f.handler(w, r, tok)
}

func newFake(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, token string)) (*fakeBotX, *Client) {
	t.Helper()
	f := &fakeBotX{t: t, handler: handler}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New("express.example.com", testBotID, testSecret, WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f, c
}

func okUser(w http.ResponseWriter) {
	io.WriteString(w, `{"status":"ok","result":{"user_huid":"`+testUserID+`","name":"Test User"}}`) //nolint:errcheck
}

func TestNew_RequiresCredentials(t *testing.T) {
	if _, err := New("express.example.com", "", testSecret); err == nil {
		t.Error("New with empty bot ID: want error")
	}
	if _, err := New("express.example.com", testBotID, ""); err == nil {
		t.Error("New with empty secret: want error")
	}
}

func TestClient_ObtainsTokenOnceAndReusesIt(t *testing.T) {
	f, c := newFake(t, func(w http.ResponseWriter, r *http.Request, token string) { okUser(w) })

	for range 3 {
		if _, err := c.GetUserByHUID(context.Background(), testUserID); err != nil {
			t.Fatalf("GetUserByHUID: %v", err)
		}
	}
	if f.issued != 1 {
		t.Errorf("tokens issued = %d, want 1", f.issued)
	}
	for _, tok := range f.authSeen {
		if tok != "tok-1" {
			t.Errorf("request carried token %q, want tok-1", tok)
		}
	}
}

func TestClient_RefreshesTokenOnceOn401(t *testing.T) {
	f, c := newFake(t, func(w http.ResponseWriter, r *http.Request, token string) {
		if token == "tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), testUserID) {
			t.Errorf("retried request lost its body: %s", body)
		}
		io.WriteString(w, `{"status":"ok","result":true}`) //nolint:errcheck
	})

	if err := c.AddUsers(context.Background(), testChatID, []string{testUserID}); err != nil {
		t.Fatalf("AddUsers: %v", err)
	}
	if f.issued != 2 {
		t.Errorf("tokens issued = %d, want 2", f.issued)
	}
	if want := []string{"tok-1", "tok-2"}; strings.Join(f.authSeen, ",") != strings.Join(want, ",") {
		t.Errorf("tokens seen = %v, want %v", f.authSeen, want)
	}
}

func TestClient_PersistentUnauthorizedIsNotRetriedAgain(t *testing.T) {
	f, c := newFake(t, func(w http.ResponseWriter, r *http.Request, token string) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, err := c.ListChats(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ListChats error = %v, want ErrUnauthorized", err)
	}
	if f.issued != 2 || len(f.authSeen) != 2 {
		t.Errorf("tokens issued = %d, requests = %d, want 2 and 2", f.issued, len(f.authSeen))
	}
}

func TestClient_RejectedSecretIsUnauthorized(t *testing.T) {
	f, c := newFake(t, func(w http.ResponseWriter, r *http.Request, token string) {
		t.Error("API called without a token")
	})
	f.tokenCodes = []int{http.StatusUnauthorized}

	_, err := c.ListChats(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Op != "token request" {
		t.Errorf("error = %#v, want *APIError of the token request", err)
	}
}

func TestClient_ConcurrentUnauthorizedRefreshesOnce(t *testing.T) {
	const callers = 8
	var arrived sync.WaitGroup
	arrived.Add(callers)
	barrier := make(chan struct{})
	go func() { arrived.Wait(); close(barrier) }()

	f, c := newFake(t, func(w http.ResponseWriter, r *http.Request, token string) {
		if token == "tok-1" {
			arrived.Done()
			<-barrier
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		okUser(w)
	})
	if _, _, err := c.currentToken(context.Background()); err != nil {
		t.Fatalf("currentToken: %v", err)
	}

	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			if _, err := c.GetUserByEmail(context.Background(), "user@example.com"); err != nil {
				t.Errorf("GetUserByEmail: %v", err)
			}
		})
	}
	wg.Wait()
	if f.issued != 2 {
		t.Errorf("tokens issued = %d, want 2: %d calls rejected with the old token share one refresh", f.issued, callers)
	}
}

func TestClient_RefreshToSameTokenDoesNotLoop(t *testing.T) {
	var calls atomic.Int32
	f := &fakeBotX{t: t}
	f.handler = func(w http.ResponseWriter, r *http.Request, token string) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			f.mu.Lock()
			f.issued++
			f.mu.Unlock()
			_, _ = io.WriteString(w, `{"status":"ok","result":"same"}`)
			return
		}
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, testBotID, testSecret)

	if _, err := c.ListChats(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
	if f.issued != 2 || calls.Load() != 2 {
		t.Errorf("tokens issued = %d, requests = %d, want 2 and 2", f.issued, calls.Load())
	}
}

func TestClient_FailedRefreshIsReportedAndRetriedLater(t *testing.T) {
	f, c := newFake(t, func(w http.ResponseWriter, r *http.Request, token string) {
		if token == "tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		okUser(w)
	})
	f.tokenCodes = []int{http.StatusOK, http.StatusBadGateway}

	if _, err := c.GetUserByHUID(context.Background(), testUserID); err == nil || !strings.Contains(err.Error(), "refreshing token") {
		t.Fatalf("error = %v, want a failed refresh", err)
	}
	if _, err := c.GetUserByHUID(context.Background(), testUserID); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if f.issued != 3 {
		t.Errorf("tokens issued = %d, want 3", f.issued)
	}
}

func TestClient_CancelledCallDoesNotWaitForAnotherCallsToken(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			close(started)
			<-release
			_, _ = io.WriteString(w, `{"status":"ok","result":"tok"}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"ok","result":[]}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, testBotID, testSecret)

	first := make(chan error, 1)
	go func() { _, err := c.ListChats(context.Background()); first <- err }()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := c.ListChats(ctx); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Error("a cancelled call waited for another call's token request")
	}

	close(release)
	if err := <-first; err != nil {
		t.Errorf("first call: %v", err)
	}
}

func TestClient_CallerGivingUpDoesNotFailOthers(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			close(started)
			<-release
			_, _ = io.WriteString(w, `{"status":"ok","result":"tok"}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"ok","result":[]}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, testBotID, testSecret)

	ctx, cancel := context.WithCancel(context.Background())
	initiator := make(chan error, 1)
	go func() { _, err := c.ListChats(ctx); initiator <- err }()
	<-started
	waiter := make(chan error, 1)
	go func() { _, err := c.ListChats(context.Background()); waiter <- err }()

	cancel()
	if err := <-initiator; !errors.Is(err, context.Canceled) {
		t.Errorf("initiator error = %v, want context.Canceled", err)
	}
	close(release)
	if err := <-waiter; err != nil {
		t.Errorf("waiter error = %v, want nil", err)
	}
}

func TestNewWithToken_DoesNotRefresh(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != "Bearer static" {
			t.Errorf("Authorization = %q", got)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewWithToken(srv.URL, "static")
	if err := c.AddAdmins(context.Background(), testChatID, []string{testUserID}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
	if calls != 1 {
		t.Errorf("requests = %d, want 1", calls)
	}
}

func TestRequestToken_ErrorsDoNotLeakSignature(t *testing.T) {
	sig := BuildSignature(testBotID, testSecret)

	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closed.Close()
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hanging.Close()
	badRedirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://%zz/?signature="+r.URL.Query().Get("signature"))
		w.WriteHeader(http.StatusFound)
	}))
	defer badRedirect.Close()
	reflecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"status":%q,"reason":%q}`, r.URL.Query().Get("signature"), r.URL.RawQuery)
	}))
	defer reflecting.Close()
	reflectingError := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, `{"status":"error","reason":%q}`, r.URL.Query().Get("signature"))
	}))
	defer reflectingError.Close()

	tests := []struct {
		name    string
		host    string
		timeout time.Duration
	}{
		{name: "connection refused", host: closed.URL},
		{name: "timeout", host: hanging.URL, timeout: 50 * time.Millisecond},
		{name: "unparsable URL", host: "http://bad host"},
		{name: "redirect to an unparsable location", host: badRedirect.URL},
		{name: "signature reflected in a 200 body", host: reflecting.URL},
		{name: "signature reflected in a 403 body", host: reflectingError.URL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.timeout)
				defer cancel()
			}
			_, err := RequestToken(ctx, tt.host, testBotID, sig)
			if err == nil {
				t.Fatal("want error")
			}
			for e := err; e != nil; e = errors.Unwrap(e) {
				if strings.Contains(e.Error(), sig) || strings.Contains(e.Error(), testSecret) {
					t.Errorf("error chain leaks credentials: %v", e)
				}
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				if strings.Contains(apiErr.Status+apiErr.Reason+apiErr.Body, sig) {
					t.Errorf("APIError fields leak the signature: %#v", apiErr)
				}
			}
			if tt.timeout > 0 && !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("error = %v, want context.DeadlineExceeded in the chain", err)
			}
		})
	}
}

func TestClient_TokenErrorThroughCallDoesNotLeakSignature(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closed.Close()
	c, err := New(closed.URL, testBotID, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetChatInfo(context.Background(), testChatID)
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), BuildSignature(testBotID, testSecret)) {
		t.Errorf("error leaks signature: %v", err)
	}
}
