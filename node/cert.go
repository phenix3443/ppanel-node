package node

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/common/logx"
)

const certificateDirectory = "/etc/PPanel-node"

func certificatePaths(info *panel.NodeInfo) (string, string) {
	base := info.Type + strconv.Itoa(info.Id)
	return filepath.Join(certificateDirectory, base+".cer"), filepath.Join(certificateDirectory, base+".key")
}

func (c *Controller) renewCertTask(_ context.Context) error {
	l, err := NewLego(c.info)
	if err != nil {
		logx.Node(c.tag).WithError(err).Error("创建lego客户端失败")
		return nil
	}
	renewed, err := l.RenewCert()
	if err != nil {
		// Return nil so the periodic task remains alive and retries on its next run.
		logx.Node(c.tag).WithError(err).Error("续期证书失败")
		return nil
	}
	if renewed {
		logx.Node(c.tag).Info("证书续期成功，正在投递重载信号")
		c.enqueueReload()
	}
	return nil
}

func (c *Controller) enqueueReload() {
	if c == nil || c.server == nil || c.server.ReloadCh == nil {
		return
	}
	select {
	case c.server.ReloadCh <- struct{}{}:
	default:
	}
}

func (c *Controller) reportSelfCertificateSHA256() error {
	if c == nil || c.info == nil || c.info.Protocol == nil || c.apiClient == nil {
		return fmt.Errorf("node controller is not initialized")
	}
	certFile, keyFile := certificatePaths(c.info)
	leaf, err := loadUsableCertificate(certFile, keyFile, c.info.Protocol.SNI)
	if err != nil {
		return fmt.Errorf("load self-signed certificate: %w", err)
	}
	// hex.EncodeToString is canonical lowercase; the panel must compare this
	// fingerprint case-insensitively to accept equivalent hexadecimal forms.
	c.apiClient.SetCertificateSHA256(certificateSHA256(leaf))
	return nil
}

func certificateSHA256(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func (c *Controller) requestCert() error {
	certFile, keyFile := certificatePaths(c.info)
	mode := strings.TrimSpace(c.info.Protocol.CertMode)

	switch mode {
	case "none", "":
		return nil
	case "file":
		if _, err := loadUsableCertificate(certFile, keyFile, c.info.Protocol.SNI); err != nil {
			return fmt.Errorf("validate configured certificate: %w", err)
		}
		if err := securePrivateKeyFile(keyFile); err != nil {
			return err
		}
		return nil
	case "dns", "http":
		if leaf, err := loadUsableCertificate(certFile, keyFile, c.info.Protocol.SNI); err == nil && certificateUsesECDSAP256(leaf) {
			if err := securePrivateKeyFile(keyFile); err != nil {
				return err
			}
			return nil
		}
		l, err := NewLego(c.info)
		if err != nil {
			return fmt.Errorf("create lego object error: %w", err)
		}
		if err := l.CreateCert(); err != nil {
			return fmt.Errorf("create lego cert error: %w", err)
		}
		return nil
	case "self":
		if leaf, err := loadUsableCertificate(certFile, keyFile, c.info.Protocol.SNI); err == nil && certificateUsesECDSAP256(leaf) {
			if err := securePrivateKeyFile(keyFile); err != nil {
				return err
			}
			return nil
		}
		if err := generateSelfSslCertificate(c.info.Protocol.SNI, certFile, keyFile); err != nil {
			return fmt.Errorf("generate self cert error: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported certmode: %s", c.info.Protocol.CertMode)
	}
}

func generateSelfSslCertificate(domain, certPath, keyPath string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return fmt.Errorf("certificate domain is required")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ECDSA private key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return fmt.Errorf("generate certificate serial number: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		Version:      3,
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: domain,
		},
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(30, 0, 0),
	}
	if ip := net.ParseIP(domain); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{domain}
	}

	cert, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return fmt.Errorf("create self-signed certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal ECDSA private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return writeCertificatePair(certPath, keyPath, certPEM, keyPEM)
}

func loadUsableCertificate(certPath, keyPath, serverName string) (*x509.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load certificate pair: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse leaf certificate: %w", err)
	}
	if leaf.IsCA {
		return nil, fmt.Errorf("leaf certificate must not be a CA certificate")
	}
	if len(leaf.ExtKeyUsage) > 0 {
		serverAuth := false
		for _, usage := range leaf.ExtKeyUsage {
			if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
				serverAuth = true
				break
			}
		}
		if !serverAuth {
			return nil, fmt.Errorf("certificate is not valid for server authentication")
		}
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) {
		return nil, fmt.Errorf("certificate is not valid before %s", leaf.NotBefore.UTC().Format(time.RFC3339))
	}
	if !now.Before(leaf.NotAfter) {
		return nil, fmt.Errorf("certificate expired at %s", leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	if serverName = strings.TrimSpace(serverName); serverName != "" {
		if err := leaf.VerifyHostname(serverName); err != nil {
			return nil, fmt.Errorf("certificate does not match SNI %q: %w", serverName, err)
		}
	}
	return leaf, nil
}

func certificateUsesECDSAP256(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	publicKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
	return ok && publicKey.Curve.Params().Name == elliptic.P256().Params().Name
}

func securePrivateKeyFile(keyPath string) error {
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return fmt.Errorf("secure private key permissions: %w", err)
	}
	return nil
}

func writeCertificatePair(certPath, keyPath string, certPEM, keyPEM []byte) error {
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("validate certificate pair before write: %w", err)
	}

	// A certificate and its key cannot be replaced atomically as a pair. The
	// caller only reloads Xray after this function returns; renewal also reuses
	// the existing private key, so the active pair remains compatible.
	if err := writeFileAtomically(keyPath, keyPEM, 0o600, 0o755); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	if err := writeFileAtomically(certPath, certPEM, 0o644, 0o755); err != nil {
		return fmt.Errorf("write certificate: %w", err)
	}
	return nil
}

func writeFileAtomically(filePath string, data []byte, fileMode, dirMode os.FileMode) (err error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(filePath)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if tmp != nil {
			_ = tmp.Close()
		}
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if err = tmp.Chmod(fileMode); err != nil {
		return fmt.Errorf("set temporary file permissions: %w", err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	tmp = nil
	if err = os.Rename(tmpPath, filePath); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}
