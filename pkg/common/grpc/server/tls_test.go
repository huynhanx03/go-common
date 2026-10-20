package server_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	grpcserver "github.com/huynhanx03/go-common/pkg/common/grpc/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestLoadServerMTLSRejectsIncompleteAndInvalidFiles(t *testing.T) {
	for _, paths := range [][3]string{{}, {"server.pem", "", "ca.pem"}, {"missing.pem", "missing.key", "missing-ca.pem"}} {
		if _, err := grpcserver.LoadServerMTLS(paths[0], paths[1], paths[2]); err == nil {
			t.Fatalf("LoadServerMTLS(%q, %q, %q) accepted invalid files", paths[0], paths[1], paths[2])
		}
	}
}

func TestMTLSServerAcceptsOnlyClientCertificatesFromDedicatedCA(t *testing.T) {
	directory := t.TempDir()
	ca, caKey := testCA(t)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
	serverCert, serverKey := testLeaf(t, ca, caKey, "localhost", x509.ExtKeyUsageServerAuth)
	clientCert, clientKey := testLeaf(t, ca, caKey, "envoy", x509.ExtKeyUsageClientAuth)
	certificateFile := writeTestFile(t, directory, "server.pem", serverCert)
	keyFile := writeTestFile(t, directory, "server.key", serverKey)
	caFile := writeTestFile(t, directory, "ca.pem", caPEM)
	serverTLS, err := grpcserver.LoadServerMTLS(certificateFile, keyFile, caFile)
	if err != nil {
		t.Fatal(err)
	}
	server, err := grpcserver.New(grpcserver.Config{Name: "mtls-test", Address: "127.0.0.1:0", TLS: serverTLS}, func(*grpc.Server) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for server.Address() == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.Address() == "" {
		t.Fatal("mTLS server did not listen")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	validIdentity, err := tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		identity *tls.Certificate
		allowed  bool
	}{
		{name: "client certificate", identity: &validIdentity, allowed: true},
		{name: "missing certificate", allowed: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			clientTLS := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "localhost"}
			if test.identity != nil {
				clientTLS.Certificates = []tls.Certificate{*test.identity}
			}
			connection, err := grpc.NewClient(server.Address(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			checkCtx, checkCancel := context.WithTimeout(context.Background(), time.Second)
			defer checkCancel()
			_, err = healthpb.NewHealthClient(connection).Check(checkCtx, &healthpb.HealthCheckRequest{})
			if test.allowed && err != nil {
				t.Fatalf("valid mTLS client rejected: %v", err)
			}
			if !test.allowed && err == nil {
				t.Fatal("client without certificate was accepted")
			}
		})
	}
}

func testCA(t *testing.T) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(100), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key
}

func testLeaf(t *testing.T, ca *x509.Certificate, caKey ed25519.PrivateKey, name string, usage x509.ExtKeyUsage) ([]byte, []byte) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(int64(len(name) + 200)), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, key.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})
}

func writeTestFile(t *testing.T, directory, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadServerMTLSRequiresVerifiedClientCertificate(t *testing.T) {
	directory := t.TempDir()
	certificate := filepath.Join(directory, "server.pem")
	key := filepath.Join(directory, "server.key")
	ca := filepath.Join(directory, "clients.pem")
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:     true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string][]byte{
		certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key:         pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}),
		ca:          pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	} {
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	config, err := grpcserver.LoadServerMTLS(certificate, key, ca)
	if err != nil {
		t.Fatal(err)
	}
	if config.MinVersion != tls.VersionTLS13 || config.ClientAuth != tls.RequireAndVerifyClientCert || config.ClientCAs == nil {
		t.Fatalf("mTLS policy = %+v", config)
	}
}
