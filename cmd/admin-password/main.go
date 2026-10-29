// admin-password creates the private PBKDF2 verifier consumed by Bridge.
// Supply the password on stdin or with -password-file; never use arguments.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"esp32-bridge/internal/adminauth"
)

func main() {
	passwordFile := flag.String("password-file", "", "read password from a private regular file (otherwise stdin)")
	outFile := flag.String("out", "", "create the hash file with mode 0600 (otherwise write hash to stdout)")
	flag.Parse()
	if flag.NArg() != 0 || (*passwordFile != "" && *passwordFile == "-") {
		fail("usage: admin-password [-password-file private-file] [-out new-private-file]")
	}
	password, err := readPassword(*passwordFile)
	if err != nil {
		fail("cannot read password input")
	}
	hash, err := adminauth.HashPassword(password)
	if err != nil {
		fail("cannot create password hash")
	}
	if *outFile == "" {
		if _, err := fmt.Fprintln(os.Stdout, hash); err != nil {
			fail("cannot write password hash")
		}
		return
	}
	f, err := os.OpenFile(*outFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fail("cannot create password hash file")
	}
	if _, err = io.WriteString(f, hash+"\n"); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(*outFile)
		fail("cannot write password hash file")
	}
}

func readPassword(file string) (string, error) {
	var r io.Reader = os.Stdin
	if file != "" {
		lstat, err := os.Lstat(file)
		if err != nil || !lstat.Mode().IsRegular() || (lstat.Mode().Perm() != 0400 && lstat.Mode().Perm() != 0600) {
			return "", fmt.Errorf("password input must be a private regular file")
		}
		f, err := os.Open(file)
		if err != nil {
			return "", err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || !os.SameFile(lstat, info) || (info.Mode().Perm() != 0400 && info.Mode().Perm() != 0600) {
			return "", fmt.Errorf("password input must be private regular file")
		}
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, 1026))
	if err != nil || len(b) > 1025 {
		return "", fmt.Errorf("password input too large")
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	if password == "" || len(password) > 1024 || strings.ContainsRune(password, '\x00') {
		return "", fmt.Errorf("invalid password")
	}
	return password, nil
}

func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
