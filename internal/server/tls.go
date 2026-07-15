package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

const (
	defaultTLSReloadInterval = 60 * time.Second
	stablePairReadAttempts   = 3
)

type certReloader struct {
	certFile       string
	keyFile        string
	reloadInterval time.Duration
	cert           atomic.Pointer[tls.Certificate]
	lastCertHash   [sha256.Size]byte
	lastKeyHash    [sha256.Size]byte
	readFile       func(string) ([]byte, error)
}

func newCertReloader(certFile, keyFile string, interval time.Duration) *certReloader {
	if interval <= 0 {
		interval = defaultTLSReloadInterval
	}
	return &certReloader{
		certFile:       certFile,
		keyFile:        keyFile,
		reloadInterval: interval,
		readFile:       os.ReadFile,
	}
}

func (r *certReloader) readStablePair() ([]byte, []byte, error) {
	for attempt := 1; attempt <= stablePairReadAttempts; attempt++ {
		certBefore, err := r.readFile(r.certFile)
		if err != nil {
			return nil, nil, fmt.Errorf("reading TLS certificate %s: %w", r.certFile, err)
		}
		keyPEM, err := r.readFile(r.keyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("reading TLS key %s: %w", r.keyFile, err)
		}
		certAfter, err := r.readFile(r.certFile)
		if err != nil {
			return nil, nil, fmt.Errorf("verifying TLS certificate %s: %w", r.certFile, err)
		}
		// Re-reading the certificate detects Kubernetes ..data swaps that
		// straddle the key read. A key-only mixed/torn read that does not match
		// the certificate is rejected later by tls.X509KeyPair, so it cannot
		// replace the last-good pair.
		if sha256.Sum256(certBefore) == sha256.Sum256(certAfter) {
			return certAfter, keyPEM, nil
		}
	}
	return nil, nil, fmt.Errorf("TLS certificate changed during %d stable-pair read attempts", stablePairReadAttempts)
}

func (r *certReloader) reload() (bool, error) {
	certPEM, keyPEM, err := r.readStablePair()
	if err != nil {
		return false, err
	}
	certHash, keyHash := sha256.Sum256(certPEM), sha256.Sum256(keyPEM)
	if r.cert.Load() != nil && certHash == r.lastCertHash && keyHash == r.lastKeyHash {
		return false, nil
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return false, fmt.Errorf("parsing TLS certificate/key pair: %w", err)
	}
	r.cert.Store(&cert)
	r.lastCertHash, r.lastKeyHash = certHash, keyHash
	return true, nil
}

func (r *certReloader) loadInitial() error {
	changed, err := r.reload()
	if err != nil {
		return err
	}
	if !changed || r.cert.Load() == nil {
		return fmt.Errorf("initial TLS certificate was not loaded")
	}
	return nil
}

func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := r.cert.Load()
	if cert == nil {
		return nil, fmt.Errorf("TLS certificate is not loaded")
	}
	return cert, nil
}

func (r *certReloader) run(ctx context.Context) {
	ticker := time.NewTicker(r.reloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed, err := r.reload()
			if err != nil {
				vlog.Info("server: TLS reload failed; keeping last good certificate: %v", err)
				continue
			}
			if changed {
				vlog.Info("server: TLS certificate reloaded")
			}
		}
	}
}
