package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// TLSManager quản lý chứng chỉ SSL cục bộ cho domain local.blinkgo.tech và 127.0.0.1
type TLSManager struct {
	certFile string
	keyFile  string
}

func NewTLSManager(store *AgentSettingsStore) *TLSManager {
	dir := filepath.Dir(store.settingsPath)
	return &TLSManager{
		certFile: filepath.Join(dir, "cert.pem"),
		keyFile:  filepath.Join(dir, "key.pem"),
	}
}

// GetTLSConfig nạp chứng chỉ có sẵn hoặc tự động tạo chứng chỉ SSL 10 năm nếu chưa có
func (tm *TLSManager) GetTLSConfig() (*tls.Config, error) {
	cert, err := tm.loadOrGenerateCert()
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func (tm *TLSManager) loadOrGenerateCert() (tls.Certificate, error) {
	// 1. Nếu file chứng chỉ đã tồn tại, thử load
	if cert, err := tls.LoadX509KeyPair(tm.certFile, tm.keyFile); err == nil {
		return cert, nil
	}

	// 2. Nếu chưa có, tự động tạo chứng chỉ X.509 ECDSA bảo mật cao
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Blink POS Vietnam"},
			CommonName:   "local.blinkgo.tech",
			Country:      []string{"VN"},
		},
		NotBefore: time.Now().Add(-24 * time.Hour),
		NotAfter:  time.Now().Add(10 * 365 * 24 * time.Hour), // 10 năm không cần lo hết hạn

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,

		DNSNames: []string{
			"local.blinkgo.tech",
			"*.blinkgo.tech",
			"localhost",
			"local.santori.vn",
			"*.santori.vn",
		},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Lưu Cert PEM
	certPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	_ = os.WriteFile(tm.certFile, certPem, 0644)

	// Lưu Key PEM
	keyBytes, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	_ = os.WriteFile(tm.keyFile, keyPem, 0600)

	return tls.X509KeyPair(certPem, keyPem)
}
