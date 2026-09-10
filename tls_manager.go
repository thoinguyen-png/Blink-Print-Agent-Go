package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CertAPIResponse cấu trúc phản hồi từ API cấp SSL của Blink Cloud
type CertAPIResponse struct {
	Success   bool   `json:"success"`
	Domain    string `json:"domain"`
	FullChain string `json:"fullchain"`
	PrivKey   string `json:"privkey"`
	NotAfter  string `json:"not_after"`
	Message   string `json:"message"`
}

// TLSManager quản lý chứng chỉ SSL cục bộ cho domain local.blinkgo.tech và 127.0.0.1
type TLSManager struct {
	certFile   string
	keyFile    string
	mu         sync.RWMutex
	activeCert *tls.Certificate
}

func NewTLSManager(store *AgentSettingsStore) *TLSManager {
	dir := filepath.Dir(store.settingsPath)
	tm := &TLSManager{
		certFile: filepath.Join(dir, "cert.pem"),
		keyFile:  filepath.Join(dir, "key.pem"),
	}

	// Khởi động tiến trình đồng bộ chứng chỉ ngầm định kỳ
	tm.startBackgroundSync()
	return tm
}

// GetTLSConfig nạp chứng chỉ SSL và hỗ trợ tự động reload khi chứng chỉ mới được cập nhật
func (tm *TLSManager) GetTLSConfig() (*tls.Config, error) {
	cert, err := tm.loadOrSyncCert()
	if err != nil {
		return nil, err
	}

	tm.mu.Lock()
	tm.activeCert = &cert
	tm.mu.Unlock()

	return &tls.Config{
		GetCertificate: tm.getCertificate,
		Certificates:   []tls.Certificate{cert},
		MinVersion:     tls.VersionTLS12,
	}, nil
}

// getCertificate cung cấp chứng chỉ động cho từng kết nối TLS
func (tm *TLSManager) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if tm.activeCert != nil {
		return tm.activeCert, nil
	}
	return nil, fmt.Errorf("chưa có chứng chỉ TLS khả dụng")
}

// loadOrSyncCert ưu tiên chứng chỉ Let's Encrypt hợp lệ, tự động tải từ Cloud hoặc tạo fallback
func (tm *TLSManager) loadOrSyncCert() (tls.Certificate, error) {
	// 1. Kiểm tra chứng chỉ hiện tại trên máy
	hasValidCert, needsRenewal := tm.checkCurrentCertStatus()

	// 2. Nếu chưa có chứng chỉ hoặc chứng chỉ sắp hết hạn/tự ký, thử kéo từ Cloud về trước
	if !hasValidCert || needsRenewal {
		log.Println("[TLS] Đang kiểm tra và tải chứng chỉ SSL Let's Encrypt từ Blink Cloud...")
		if newCert, err := tm.fetchAndApplyCloudCert(); err == nil {
			return newCert, nil
		} else {
			log.Printf("[TLS] Không thể tải chứng chỉ từ Cloud: %v", err)
		}
	}

	// 3. Nếu đã có chứng chỉ cục bộ trên máy, nạp lên
	if cert, err := tls.LoadX509KeyPair(tm.certFile, tm.keyFile); err == nil {
		return cert, nil
	}

	// 4. Nếu máy hoàn toàn chưa có chứng chỉ và không kết nối được Cloud, tạo tự ký tạm thời
	log.Println("[TLS] Đang tạo chứng chỉ SSL tự ký tạm thời...")
	return tm.generateFallbackCert()
}

// checkCurrentCertStatus kiểm tra xem chứng chỉ hiện tại có tồn tại, còn hạn hay là tự ký
func (tm *TLSManager) checkCurrentCertStatus() (exists bool, needsRenewal bool) {
	certBytes, err := os.ReadFile(tm.certFile)
	if err != nil {
		return false, true
	}
	_, err = os.ReadFile(tm.keyFile)
	if err != nil {
		return false, true
	}

	block, _ := pem.Decode(certBytes)
	if block == nil {
		return false, true
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, true
	}

	// Nếu là chứng chỉ tự ký (Issuer == Subject), coi như cần thay thế bằng Let's Encrypt
	isSelfSigned := cert.Issuer.CommonName == cert.Subject.CommonName

	// Nếu hết hạn hoặc còn dưới 15 ngày
	isExpiringSoon := time.Until(cert.NotAfter) < (15 * 24 * time.Hour)

	if isSelfSigned {
		log.Println("[TLS] Phát hiện chứng chỉ tự ký cục bộ. Cần nâng cấp lên chứng chỉ Let's Encrypt.")
		return true, true
	}

	if isExpiringSoon {
		log.Printf("[TLS] Chứng chỉ sắp hết hạn (%v). Cần gia hạn.", cert.NotAfter.Format("2006-01-02 15:04:05"))
		return true, true
	}

	return true, false
}

// fetchAndApplyCloudCert kéo chứng chỉ mới nhất từ Cloud Run / API của Blink
func (tm *TLSManager) fetchAndApplyCloudCert() (tls.Certificate, error) {
	endpoints := []string{
		"https://api.blinkgo.tech/api/agent/cert.php?domain=local.blinkgo.tech",
		"https://dev.blinkgo.tech/api/agent/cert.php?domain=local.blinkgo.tech",
		"http://localhost/Blink/api/agent/cert.php?domain=local.blinkgo.tech",
	}

	client := http.Client{Timeout: 8 * time.Second}
	var lastErr error

	for _, endpoint := range endpoints {
		req, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("X-Agent-Client", "BlinkPrintAgent")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP status %d from %s", resp.StatusCode, endpoint)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		var apiResp CertAPIResponse
		if err := json.Unmarshal(body, &apiResp); err != nil {
			lastErr = err
			continue
		}

		if !apiResp.Success || apiResp.FullChain == "" || apiResp.PrivKey == "" {
			lastErr = fmt.Errorf("phản hồi không hợp lệ từ %s: %s", endpoint, apiResp.Message)
			continue
		}

		// Kiểm tra cặp khóa và chứng chỉ có khớp nhau không
		newCert, err := tls.X509KeyPair([]byte(apiResp.FullChain), []byte(apiResp.PrivKey))
		if err != nil {
			lastErr = fmt.Errorf("chứng chỉ nhận được không hợp lệ: %w", err)
			continue
		}

		// Ghi đè vào ổ cứng
		_ = os.WriteFile(tm.certFile, []byte(apiResp.FullChain), 0644)
		_ = os.WriteFile(tm.keyFile, []byte(apiResp.PrivKey), 0600)

		// Cập nhật chứng chỉ đang chạy ngay lập tức
		tm.mu.Lock()
		tm.activeCert = &newCert
		tm.mu.Unlock()

		log.Printf("🔒 [TLS] Đồng bộ chứng chỉ SSL Let's Encrypt chính hãng thành công từ: %s", endpoint)
		return newCert, nil
	}

	return tls.Certificate{}, fmt.Errorf("không thể đồng bộ SSL từ mọi endpoint: %v", lastErr)
}

// startBackgroundSync chạy định kỳ kiểm tra và làm mới chứng chỉ ngầm không cần restart
func (tm *TLSManager) startBackgroundSync() {
	go func() {
		// Đợi 15 giây sau khi mở server in
		time.Sleep(15 * time.Second)

		// Kiểm tra định kỳ mỗi 12 tiếng
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()

		for {
			_, needsRenewal := tm.checkCurrentCertStatus()
			if needsRenewal {
				log.Println("[TLS-Sync] Đang tải chứng chỉ mới nhất từ Cloud...")
				if _, err := tm.fetchAndApplyCloudCert(); err != nil {
					log.Printf("[TLS-Sync] Chưa thể cập nhật SSL: %v", err)
				}
			}

			<-ticker.C
		}
	}()
}

// generateFallbackCert tạo chứng chỉ ECDSA cục bộ nếu máy không có internet
func (tm *TLSManager) generateFallbackCert() (tls.Certificate, error) {
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
		NotAfter:  time.Now().Add(10 * 365 * 24 * time.Hour),

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

	certPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	_ = os.WriteFile(tm.certFile, certPem, 0644)

	keyBytes, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	_ = os.WriteFile(tm.keyFile, keyPem, 0600)

	return tls.X509KeyPair(certPem, keyPem)
}
