package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func startTestRun(t *testing.T, srv *Server) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() { errs <- srv.Run(ctx) }()
	select {
	case <-srv.Ready():
	case err := <-errs:
		cancel()
		t.Fatalf("Run before Ready: %v", err)
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("Ready timeout")
	}
	return cancel, errs
}

func stopTestRun(t *testing.T, cancel context.CancelFunc, errs <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run shutdown timeout")
	}
}

func testRoots(t *testing.T, certs ...[]byte) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	for _, cert := range certs {
		if !pool.AppendCertsFromPEM(cert) {
			t.Fatal("append root")
		}
	}
	return pool
}

func tlsPeerSerial(addr string, roots *x509.CertPool, max uint16) (int64, error) {
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		RootCAs:    roots,
		ServerName: "127.0.0.1",
		MinVersion: tls.VersionTLS10,
		MaxVersion: max,
	})
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	return conn.ConnectionState().PeerCertificates[0].SerialNumber.Int64(), nil
}

func TestServerRunHTTPRegression(t *testing.T) {
	srv := newTestServer(nil, func(cfg *Config) { cfg.Listen = "127.0.0.1:0" })
	cancel, errs := startTestRun(t, srv)
	resp, err := http.Get("http://" + srv.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	stopTestRun(t, cancel, errs)
}

func TestServerRunTLSAndMinimumVersion(t *testing.T) {
	certFile, keyFile, certPEM, _, _ := writeTestPair(t, t.TempDir(), 1, nil)
	srv := newTestServer(nil, func(cfg *Config) {
		cfg.Listen = "127.0.0.1:0"
		cfg.TLS = &TLSConfig{CertFile: certFile, KeyFile: keyFile, ReloadInterval: time.Minute}
	})
	cancel, errs := startTestRun(t, srv)
	roots := testRoots(t, certPEM)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    roots,
		MinVersion: tls.VersionTLS12,
	}}}
	resp, err := client.Get("https://" + srv.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if _, err := tlsPeerSerial(srv.Addr().String(), roots, tls.VersionTLS11); err == nil {
		t.Fatal("TLS 1.1 succeeded")
	}
	stopTestRun(t, cancel, errs)
}

func TestServerRunInitialFailureBeforeBind(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := dir+"/tls.crt", dir+"/tls.key"
	if err := os.WriteFile(certFile, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(nil, func(cfg *Config) {
		cfg.Listen = "127.0.0.1:0"
		cfg.TLS = &TLSConfig{CertFile: certFile, KeyFile: keyFile, ReloadInterval: time.Minute}
	})
	if err := srv.Run(context.Background()); err == nil {
		t.Fatal("invalid keypair succeeded")
	}
	if srv.Addr() != nil {
		t.Fatalf("listener bound at %v", srv.Addr())
	}
}

func TestServerRunBindFailureDoesNotStartPoller(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	certFile, keyFile, _, _, _ := writeTestPair(t, t.TempDir(), 1, nil)
	srv := newTestServer(nil, func(cfg *Config) {
		cfg.Listen = occupied.Addr().String()
		cfg.TLS = &TLSConfig{CertFile: certFile, KeyFile: keyFile, ReloadInterval: time.Minute}
	})
	var started atomic.Bool
	srv.pollerStarted = func() { started.Store(true) }
	if err := srv.Run(context.Background()); err == nil || started.Load() {
		t.Fatalf("bind err=%v started=%v", err, started.Load())
	}
}

func TestServerRunCancelsAndJoinsPoller(t *testing.T) {
	certFile, keyFile, _, _, _ := writeTestPair(t, t.TempDir(), 1, nil)
	srv := newTestServer(nil, func(cfg *Config) {
		cfg.Listen = "127.0.0.1:0"
		cfg.TLS = &TLSConfig{CertFile: certFile, KeyFile: keyFile, ReloadInterval: time.Minute}
	})
	started := make(chan struct{})
	srv.pollerStarted = func() { close(started) }
	cancel, errs := startTestRun(t, srv)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("poller start timeout")
	}
	done := srv.pollerDone
	if done == nil {
		t.Fatal("pollerDone not stored")
	}
	stopTestRun(t, cancel, errs)
	select {
	case <-done:
	default:
		t.Fatal("Run returned before pollerDone closed")
	}
}

func TestServerRunHotReloadsCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, cert1, _, _ := writeTestPair(t, dir, 1, nil)
	cert2, key2, _ := generateTestPair(t, 2, nil)
	srv := newTestServer(nil, func(cfg *Config) {
		cfg.Listen = "127.0.0.1:0"
		cfg.TLS = &TLSConfig{CertFile: certFile, KeyFile: keyFile, ReloadInterval: 10 * time.Millisecond}
	})
	cancel, errs := startTestRun(t, srv)
	if err := os.WriteFile(certFile, cert2, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, key2, 0o600); err != nil {
		t.Fatal(err)
	}
	roots := testRoots(t, cert1, cert2)
	deadline := time.Now().Add(2 * time.Second)
	for {
		serial, err := tlsPeerSerial(srv.Addr().String(), roots, tls.VersionTLS13)
		if err == nil && serial == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reload timeout: serial=%d err=%v", serial, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopTestRun(t, cancel, errs)
}
