//go:build integration

package testsupport

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Certificates are the ephemeral files a TLS-enabled instance is built from.
//
// Everything here is generated per test and thrown away with the temporary
// directory. Nothing is committed: a certificate in the repository is a
// certificate that outlives its purpose and eventually gets trusted somewhere.
type Certificates struct {
	// CACert is the root the client verifies the server against.
	CACert string
	// ClientCert and ClientKey authenticate the client to the server.
	ClientCert string
	ClientKey  string

	// serverCert and serverKey are copied into the container.
	serverCert string
	serverKey  string
}

// The server certificate is issued for localhost, which is the name the mapped
// port is reached by. WrongHost is a name it is deliberately not issued for, so
// that verify-full has something to refuse.
const (
	CertifiedHost = "localhost"
	WrongHost     = "127.0.0.1"
)

// NewCertificates builds a throwaway CA and issues a server and a client
// certificate from it.
func NewCertificates(t *testing.T) Certificates {
	t.Helper()

	dir := t.TempDir()
	caCert, caKey := issueCA(t)

	certs := Certificates{
		CACert:     write(t, dir, "ca.crt", encodeCert(caCert.Raw)),
		serverCert: "",
		serverKey:  "",
	}

	serverDER, serverKey := issue(t, caCert, caKey, "server", []string{CertifiedHost})
	certs.serverCert = write(t, dir, "server.crt", encodeCert(serverDER))
	certs.serverKey = write(t, dir, "server.key", encodeKey(serverKey))

	clientDER, clientKey := issue(t, caCert, caKey, User, nil)
	certs.ClientCert = write(t, dir, "client.crt", encodeCert(clientDER))
	certs.ClientKey = write(t, dir, "client.key", encodeKey(clientKey))

	return certs
}

func issueCA(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the CA key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "hermes test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("issuing the CA certificate: %v", err)
	}

	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the CA certificate: %v", err)
	}

	return parsed, key
}

// issue signs a leaf certificate. The common name doubles as the PostgreSQL
// role for the client certificate, which is what makes certificate
// authentication line up with the user in the connection.
func issue(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey, commonName string, hosts []string) ([]byte, *rsa.PrivateKey) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the key for %s: %v", commonName, err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
			continue
		}
		template.DNSNames = append(template.DNSNames, host)
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("issuing the certificate for %s: %v", commonName, err)
	}

	return der, key
}

func encodeCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func encodeKey(key *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// write puts a file where the client can read it. The mode is 0600 because
// libpq refuses a client key with any group or world access, exactly as the
// server does with its own.
func write(t *testing.T, dir, name string, content []byte) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}

	return path
}
