// Package voicepair implements the signed HomeDash voice pairing protocol,
// persisted bindings and HTTP adapter. Deployment wiring lives in voicepairsetup.
package voicepair

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("invalid pairing input")
var encoding64 = base64.RawURLEncoding.Strict()

type ID [16]byte
type Secret [32]byte
type Generation [16]byte

type QR struct {
	Bridge     ID
	Device     ID
	PublicKey  [65]byte
	Nonce      Secret
	OriginHash [15]byte
}

func DecodeID(s string) (ID, error) {
	var id ID
	if len(s) != 32 || strings.ToLower(s) != s {
		return id, ErrInvalid
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return id, ErrInvalid
	}
	copy(id[:], b)
	return id, nil
}

func decode64(s string, n int) ([]byte, error) {
	if len(s) != encoding64.EncodedLen(n) {
		return nil, ErrInvalid
	}
	b, err := encoding64.DecodeString(s)
	if err != nil || len(b) != n || encoding64.EncodeToString(b) != s {
		return nil, ErrInvalid
	}
	return b, nil
}

func ParseQR(s string) (QR, error) {
	var q QR
	if len(s) != 199 || !strings.HasPrefix(s, "HDV2:") {
		return q, ErrInvalid
	}
	b, err := decode64(s[5:], 145)
	if err != nil || b[0] != 2 {
		return q, ErrInvalid
	}

	copy(q.Bridge[:], b[1:17])
	copy(q.Device[:], b[17:33])
	copy(q.PublicKey[:], b[33:98])
	copy(q.Nonce[:], b[98:130])
	copy(q.OriginHash[:], b[130:145])
	if q.Bridge == (ID{}) || q.Device == (ID{}) || q.Nonce == (Secret{}) {
		return QR{}, ErrInvalid
	}
	if _, err = ParsePublicKey(q.PublicKey[:]); err != nil {
		return QR{}, ErrInvalid
	}
	return q, nil
}

func ParsePublicKey(b []byte) (*ecdsa.PublicKey, error) {
	if len(b) != 65 || b[0] != 4 {
		return nil, ErrInvalid
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), b)
	if x == nil || y == nil {
		return nil, ErrInvalid
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}

// ValidateOrigins validates the exact canonical Bridge HTTPS origin and WSS
// origin, requiring identical authorities. Neither accepts paths or defaults.
func ValidateOrigins(httpsOrigin, wssOrigin string) error {
	h, err := canonicalAuthority(httpsOrigin, "https")
	if err != nil {
		return ErrInvalid
	}
	w, err := canonicalAuthority(wssOrigin, "wss")
	if err != nil || h != w {
		return ErrInvalid
	}
	return nil
}
func canonicalAuthority(s, scheme string) (string, error) {
	if len(s) > 159 || !strings.HasPrefix(s, scheme+"://") {
		return "", ErrInvalid
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != scheme || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Host == "" {
		return "", ErrInvalid
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" || port == "" {
		return "", ErrInvalid
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return "", ErrInvalid
	}
	if strings.Contains(host, ":") {
		ip := net.ParseIP(host)
		if ip == nil || ip.To4() != nil || ip.String() != host {
			return "", ErrInvalid
		}
	} else {
		if len(host) > 253 || strings.ToLower(host) != host || strings.HasSuffix(host, ".") {
			return "", ErrInvalid
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", ErrInvalid
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return "", ErrInvalid
				}
			}
		}
	}
	if s != scheme+"://"+net.JoinHostPort(host, port) {
		return "", ErrInvalid
	}
	return u.Host, nil
}

type Operation byte

const (
	Deliver  Operation = 1
	Ack      Operation = 2
	ClearAck Operation = 3
)

type Transcript struct {
	Operation   Operation
	Bridge      ID
	Device      ID
	Session     Secret
	Challenge   Secret
	Generation  Generation
	HTTPSOrigin string
	Epoch       uint64
}

func (t Transcript) Bytes() ([]byte, error) {
	if _, err := canonicalAuthority(t.HTTPSOrigin, "https"); err != nil {
		return nil, ErrInvalid
	}
	var zeroSecret Secret
	var zeroGen Generation
	if t.Bridge == (ID{}) || t.Device == (ID{}) || t.Challenge == (Secret{}) {
		return nil, ErrInvalid
	}
	if t.Operation != ClearAck && t.Session == (Secret{}) {
		return nil, ErrInvalid
	}
	switch t.Operation {
	case Deliver:
		if t.Generation != zeroGen || t.Epoch != 0 {
			return nil, ErrInvalid
		}
	case Ack:
		if t.Epoch != 0 || t.Generation == zeroGen {
			return nil, ErrInvalid
		}
	case ClearAck:
		if t.Session != zeroSecret || t.Generation == zeroGen || t.Epoch == 0 {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	b := append([]byte("HomeDash voice device proof v1"), 0, 1, byte(t.Operation))
	b = append(b, t.Bridge[:]...)
	b = append(b, t.Device[:]...)
	b = append(b, t.Session[:]...)
	b = append(b, t.Challenge[:]...)
	b = append(b, t.Generation[:]...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(t.HTTPSOrigin)))
	b = append(b, t.HTTPSOrigin...)
	b = binary.BigEndian.AppendUint64(b, t.Epoch)
	return b, nil
}
func VerifyProof(pub *ecdsa.PublicKey, t Transcript, signature string) bool {
	b, err := t.Bytes()
	if err != nil {
		return false
	}
	return verifySignature(pub, b, signature)
}
func verifySignature(pub *ecdsa.PublicKey, b []byte, signature string) bool {
	if pub == nil || pub.Curve != elliptic.P256() || pub.X == nil || pub.Y == nil || !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return false
	}
	sig, err := decode64(signature, 64)
	if err != nil {
		return false
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	n := elliptic.P256().Params().N
	half := new(big.Int).Rsh(new(big.Int).Set(n), 1)
	if r.Sign() <= 0 || r.Cmp(n) >= 0 || s.Sign() <= 0 || s.Cmp(half) > 0 {
		return false
	}
	digest := sha256.Sum256(b)
	return ecdsa.Verify(pub, digest[:], r, s)
}

func (id ID) MarshalText() ([]byte, error) { return []byte(hex.EncodeToString(id[:])), nil }
func (id *ID) UnmarshalText(b []byte) error {
	v, err := DecodeID(string(b))
	if err != nil {
		return err
	}
	*id = v
	return nil
}
func (s Secret) MarshalText() ([]byte, error) { return []byte(encoding64.EncodeToString(s[:])), nil }
func (s *Secret) UnmarshalText(b []byte) error {
	v, err := decode64(string(b), 32)
	if err != nil {
		return err
	}
	copy(s[:], v)
	return nil
}

// ClearAckPreauthBytes is the domain-separated pre-authentication transcript.
func ClearAckPreauthBytes(t Transcript, nonce Secret) ([]byte, error) {
	if t.Operation != ClearAck || t.Bridge == (ID{}) || t.Device == (ID{}) || t.Generation == (Generation{}) || t.Epoch == 0 || t.Session != (Secret{}) || nonce == (Secret{}) {
		return nil, ErrInvalid
	}
	if _, err := canonicalAuthority(t.HTTPSOrigin, "https"); err != nil {
		return nil, ErrInvalid
	}
	b := append([]byte("HomeDash voice clear ack preauth v1"), 0, 1)
	b = append(b, t.Bridge[:]...)
	b = append(b, t.Device[:]...)
	b = append(b, t.Generation[:]...)
	b = binary.BigEndian.AppendUint64(b, t.Epoch)
	b = binary.BigEndian.AppendUint16(b, uint16(len(t.HTTPSOrigin)))
	b = append(b, t.HTTPSOrigin...)
	b = append(b, nonce[:]...)
	return b, nil
}
