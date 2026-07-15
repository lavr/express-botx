package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func generateTestPair(t *testing.T, serial int64, key *rsa.PrivateKey) ([]byte, []byte, *rsa.PrivateKey) {
	t.Helper()
	if key == nil {
		var err error
		key, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		key
}

func writeTestPair(t *testing.T, dir string, serial int64, key *rsa.PrivateKey) (string, string, []byte, []byte, *rsa.PrivateKey) {
	t.Helper()
	certPEM, keyPEM, key := generateTestPair(t, serial, key)
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, certPEM, keyPEM, key
}

func servedSerial(t *testing.T, cert *tls.Certificate) int64 {
	t.Helper()
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return parsed.SerialNumber.Int64()
}

func TestCertReloaderInitialAndUnchanged(t *testing.T) {
	certFile, keyFile, _, _, _ := writeTestPair(t, t.TempDir(), 1, nil)
	r := newCertReloader(certFile, keyFile, time.Minute)
	if _, err := r.GetCertificate(nil); err == nil {
		t.Fatal("GetCertificate before loadInitial succeeded")
	}
	if err := r.loadInitial(); err != nil {
		t.Fatal(err)
	}
	before, _ := r.GetCertificate(nil)
	changed, err := r.reload()
	if err != nil || changed {
		t.Fatalf("reload unchanged = (%v, %v)", changed, err)
	}
	after, _ := r.GetCertificate(nil)
	if after != before {
		t.Fatal("unchanged content replaced pointer")
	}
}

func TestCertReloaderReloadAndLastGood(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _, _, _ := writeTestPair(t, dir, 1, nil)
	r := newCertReloader(certFile, keyFile, time.Minute)
	if err := r.loadInitial(); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _ = writeTestPair(t, dir, 2, nil)
	changed, err := r.reload()
	if err != nil || !changed {
		t.Fatalf("reload = (%v, %v)", changed, err)
	}
	current, _ := r.GetCertificate(nil)
	if got := servedSerial(t, current); got != 2 {
		t.Fatalf("serial = %d, want 2", got)
	}
	before := current
	certHash, keyHash := r.lastCertHash, r.lastKeyHash
	if err := os.WriteFile(keyFile, []byte("broken key"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err = r.reload()
	if err == nil || changed {
		t.Fatalf("broken reload = (%v, %v)", changed, err)
	}
	after, _ := r.GetCertificate(nil)
	if after != before || r.lastCertHash != certHash || r.lastKeyHash != keyHash {
		t.Fatal("failed reload changed last-good state")
	}
}

func TestCertReloaderHashesFilesSeparately(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM, key := generateTestPair(t, 1, nil)
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	extra := pem.EncodeToMemory(&pem.Block{Type: "IGNORED", Bytes: []byte("block")})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, append(append([]byte{}, extra...), keyPEM...), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newCertReloader(certFile, keyFile, time.Minute)
	if err := r.loadInitial(); err != nil {
		t.Fatal(err)
	}

	renewedCert, _, _ := generateTestPair(t, 2, key)
	if err := os.WriteFile(certFile, renewedCert, 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.reload(); err != nil || !changed {
		t.Fatalf("cert-only change = (%v, %v)", changed, err)
	}

	// Restore the exact initial concatenation and make it the stored baseline.
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, append(append([]byte{}, extra...), keyPEM...), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.reload(); err != nil || !changed {
		t.Fatalf("restore concatenation baseline = (%v, %v)", changed, err)
	}

	// certPEM || extra || keyPEM is unchanged while each file changes.
	if err := os.WriteFile(certFile, append(append([]byte{}, certPEM...), extra...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.reload(); err != nil || !changed {
		t.Fatalf("concatenation ambiguity = (%v, %v)", changed, err)
	}
}

func kubeSecretMount(t *testing.T) (string, string, func()) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Kubernetes projection test requires symlinks")
	}
	mount := t.TempDir()
	for _, item := range []struct {
		dir    string
		serial int64
	}{{"..data_v1", 1}, {"..data_v2", 2}} {
		path := filepath.Join(mount, item.dir)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		cert, key, _ := generateTestPair(t, item.serial, nil)
		if err := os.WriteFile(filepath.Join(path, "tls.crt"), cert, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "tls.key"), key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("..data_v1", filepath.Join(mount, "..data")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join("..data", "tls.crt"), filepath.Join(mount, "tls.crt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "tls.key"), filepath.Join(mount, "tls.key")); err != nil {
		t.Fatal(err)
	}
	swap := func() {
		tmp := filepath.Join(mount, "..data_tmp")
		if err := os.Symlink("..data_v2", tmp); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(mount, "..data")); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(mount, "tls.crt"), filepath.Join(mount, "tls.key"), swap
}

func TestCertReloaderKubernetesSwapAndMidReadRetry(t *testing.T) {
	for _, midRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "between reloads", true: "between cert and key reads"}[midRead], func(t *testing.T) {
			certFile, keyFile, swap := kubeSecretMount(t)
			r := newCertReloader(certFile, keyFile, time.Minute)
			if err := r.loadInitial(); err != nil {
				t.Fatal(err)
			}
			if midRead {
				var once sync.Once
				r.readFile = func(name string) ([]byte, error) {
					data, err := os.ReadFile(name)
					if err == nil && name == certFile {
						once.Do(swap)
					}
					return data, err
				}
			} else {
				swap()
			}
			changed, err := r.reload()
			if err != nil || !changed {
				t.Fatalf("reload = (%v, %v)", changed, err)
			}
			cert, _ := r.GetCertificate(nil)
			if got := servedSerial(t, cert); got != 2 {
				t.Fatalf("mixed/stale serial = %d, want 2", got)
			}
		})
	}
}

func TestCertReloaderClampsInterval(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second} {
		r := newCertReloader("cert", "key", interval)
		if r.reloadInterval != defaultTLSReloadInterval {
			t.Fatalf("interval = %v", r.reloadInterval)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r.run(ctx)
	}
}
