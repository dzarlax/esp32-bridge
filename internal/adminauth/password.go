package adminauth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const passwordIterations = 600_000

// HashPassword creates a versioned PBKDF2-SHA256 password verifier.
func HashPassword(password string) (string, error) {
	if password == "" || len(password) > 1024 {
		return "", errors.New("password must be between 1 and 1024 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", errors.New("cannot generate password salt")
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return "", errors.New("cannot derive password verifier")
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword accepts only the production parameters used by HashPassword.
func verifyPassword(password, encoded string) bool {
	if password == "" || len(password) > 1024 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations != passwordIterations {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	expected, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(salt) < 16 || len(expected) != 32 {
		return false
	}
	actual, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(expected))
	return err == nil && subtle.ConstantTimeCompare(actual, expected) == 1
}

func readPasswordHash(file string) (string, error) {
	lstat, err := os.Lstat(file)
	if err != nil || !lstat.Mode().IsRegular() || (lstat.Mode().Perm() != 0400 && lstat.Mode().Perm() != 0600) {
		return "", errors.New("admin password hash file must be a private regular file")
	}
	f, err := os.Open(file)
	if err != nil {
		return "", errors.New("cannot read admin password hash file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(lstat, info) || (info.Mode().Perm() != 0400 && info.Mode().Perm() != 0600) {
		return "", errors.New("admin password hash file must be a private regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(b) > 1024 {
		return "", errors.New("invalid admin password hash file")
	}
	hash := strings.TrimSpace(string(b))
	if !validPasswordHash(hash) {
		return "", errors.New("invalid admin password hash file")
	}
	return hash, nil
}

func validPasswordHash(hash string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n != passwordIterations {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	key, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	return err1 == nil && err2 == nil && len(salt) >= 16 && len(key) == 32
}
