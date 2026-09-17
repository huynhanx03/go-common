package server

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// LoadServerMTLS loads a static server identity and a dedicated client CA.
// Empty or partial paths are errors; callers must opt into local plaintext by
// passing nil TLS to Config explicitly. Rotate certificates by restarting the
// listener after atomically updating the mounted files.
func LoadServerMTLS(certificateFile, privateKeyFile, clientCAFile string) (*tls.Config, error) {
	if certificateFile == "" || privateKeyFile == "" || clientCAFile == "" {
		return nil, fmt.Errorf("grpc server: complete mTLS files are required")
	}
	certificate, err := tls.LoadX509KeyPair(certificateFile, privateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("grpc server: load server identity: %w", err)
	}
	caPEM, err := os.ReadFile(clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("grpc server: read client CA: %w", err)
	}
	clients := x509.NewCertPool()
	if !clients.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("grpc server: parse client CA")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clients,
	}, nil
}
