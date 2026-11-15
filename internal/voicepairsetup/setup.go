// Package voicepairsetup connects the optional pairing service to management.
package voicepairsetup

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"esp32-bridge/internal/settings"
	"esp32-bridge/internal/voicepair"
	"esp32-bridge/internal/voicepush"
	"golang.org/x/sys/unix"
)

var errConfig = errors.New("invalid voice pairing configuration")

type Config struct {
	StateDir, Origin, AdminBase, MasterFile, MasterID, IntegrityFile, IntegrityID, SigningFile, TLSCertFile, TLSKeyFile, Publisher string
	LegacyMigration                                                                                                                bool
	LegacySubscriber, LegacyTarget                                                                                                 string
}
type Setup struct {
	Manager *voicepair.Manager
	Hub     *voicepush.Hub
	Adapter *voicepair.HTTPAdapter
	TLS     tls.Certificate
}

// readPrivate rejects symlinks, nonregular files and files accessible by other users.
func readPrivate(path string, max int64) ([]byte, error) {
	if path == "" {
		return nil, errConfig
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errConfig
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > max {
		return nil, errConfig
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) != info.Size() {
		return nil, errConfig
	}
	return b, nil
}
func New(c Config, store *settings.Store, admin func(http.Handler) http.Handler) (*Setup, error) {
	if store == nil || admin == nil || c.StateDir == "" || c.MasterID == "" || c.IntegrityID == "" || c.MasterID == c.IntegrityID || len(c.Publisher) < 24 {
		return nil, errConfig
	}
	if c.LegacyMigration {
		if len(c.LegacySubscriber) < 24 || c.LegacyTarget == "" || c.LegacySubscriber == c.Publisher || strings.ContainsAny(c.LegacySubscriber, " \t\r\n") {
			return nil, errConfig
		}
	} else if c.LegacySubscriber != "" || c.LegacyTarget != "" {
		return nil, errConfig
	}
	base, err := url.Parse(c.AdminBase)
	if err != nil || base.Scheme != "https" || base.User != nil || base.Hostname() == "" || (base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.Fragment != "" {
		return nil, errConfig
	}
	port := base.Port()
	if port == "" {
		port = "443"
	}
	if c.Origin != "https://"+net.JoinHostPort(base.Hostname(), port) {
		return nil, errConfig
	}
	paths := map[string]bool{}
	var identities []os.FileInfo
	for _, p := range []string{c.MasterFile, c.IntegrityFile, c.SigningFile, c.TLSKeyFile} {
		if p == "" || paths[p] {
			return nil, errConfig
		}
		paths[p] = true
		info, e := os.Lstat(p)
		if e != nil || !info.Mode().IsRegular() {
			return nil, errConfig
		}
		for _, old := range identities {
			if os.SameFile(old, info) {
				return nil, errConfig
			}
		}
		identities = append(identities, info)
	}
	master, e := readPrivate(c.MasterFile, 32)
	if e != nil || len(master) != 32 {
		return nil, errConfig
	}
	integrity, e := readPrivate(c.IntegrityFile, 32)
	if e != nil || len(integrity) != 32 {
		return nil, errConfig
	}
	raw, e := readPrivate(c.SigningFile, 4096)
	if e != nil {
		return nil, errConfig
	}
	block, rest := pem.Decode(raw)
	if block == nil || len(rest) != 0 {
		return nil, errConfig
	}
	var signing *ecdsa.PrivateKey
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		signing = k
	} else if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		signing, _ = k.(*ecdsa.PrivateKey)
	}
	cert, e := os.ReadFile(c.TLSCertFile)
	if e != nil || len(cert) > 65536 {
		return nil, errConfig
	}
	tlskey, e := readPrivate(c.TLSKeyFile, 16384)
	if e != nil {
		return nil, errConfig
	}
	pair, e := tls.X509KeyPair(cert, tlskey)
	if e != nil {
		return nil, errConfig
	}
	if signing == nil {
		return nil, errConfig
	}
	signingPub, err := x509.MarshalPKIXPublicKey(&signing.PublicKey)
	if err != nil {
		return nil, errConfig
	}
	tlsSigner, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errConfig
	}
	tlsPub, err := x509.MarshalPKIXPublicKey(tlsSigner.Public())
	if err != nil || bytes.Equal(signingPub, tlsPub) {
		return nil, errConfig
	}
	bridge, e := voicepair.DecodeID(store.BridgeID())
	if e != nil {
		return nil, errConfig
	}
	var mk, ik [32]byte
	copy(mk[:], master)
	copy(ik[:], integrity)
	if mk == ([32]byte{}) {
		return nil, errConfig
	}
	m, e := voicepair.Open(c.StateDir, bridge, mk, c.MasterID, ik, c.IntegrityID, signing)
	if e != nil {
		return nil, errConfig
	}
	target := func() string {
		id, ok := m.SelectedTarget()
		if !ok {
			return ""
		}
		return hex.EncodeToString(id[:])
	}
	managed, e := m.ManagedMode()
	if e != nil {
		m.Close()
		return nil, errConfig
	}
	h := voicepush.NewMigrating(c.Publisher, c.LegacySubscriber, c.LegacyTarget, func(token string) (voicepush.PairIdentity, bool) {
		id, gen, ok := m.LookupActiveToken(token)
		return voicepush.PairIdentity{DeviceID: hex.EncodeToString(id[:]), Generation: hex.EncodeToString(gen[:])}, ok
	}, target(), managed)
	a, e := voicepair.NewHTTPAdapter(voicepair.HTTPConfig{Enabled: true, TrustedTransport: true, HTTPSOrigin: c.Origin, Manager: m, Admin: admin, DeviceExists: func(id voicepair.ID) bool { return store.HasDevice(hex.EncodeToString(id[:])) }, Connected: func(id voicepair.ID) bool { return h.Connected(hex.EncodeToString(id[:])) }, RoutingUpdate: func(mutate func() error) error {
		var mutationErr error
		routingErr := h.RoutingUpdateMode(func() (bool, string, error) {
			mutationErr = mutate()
			mode, err := m.ManagedMode()
			if err != nil {
				return true, "", err
			}
			return mode, target(), nil
		})
		if routingErr != nil {
			return routingErr
		}
		return mutationErr
	}})
	if e != nil {
		h.Close()
		m.Close()
		return nil, errConfig
	}
	return &Setup{m, h, a, pair}, nil
}
func TLSOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			http.Error(w, "TLS required", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}
func (s *Setup) Clear(id string) any {
	device, e := voicepair.DecodeID(id)
	if e != nil {
		return nil
	}
	c, ok := s.Manager.PendingClear(device)
	if !ok {
		return nil
	}
	enc := base64.RawURLEncoding.EncodeToString
	return map[string]any{"version": 1, "bridge_id": hex.EncodeToString(c.Bridge[:]), "device_id": hex.EncodeToString(c.Device[:]), "device_key_hash": enc(c.KeyHash[:]), "generation": hex.EncodeToString(c.Generation[:]), "revocation_epoch": strconv.FormatUint(c.Epoch, 10), "nonce": enc(c.Nonce[:]), "signature": c.Signature}
}
