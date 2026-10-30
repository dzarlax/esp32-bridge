// settings-export-v2 writes a private, non-overwriting copy for manual rollback.
package main

import (
	"errors"
	"esp32-bridge/internal/settings"
	"fmt"
	"io"
	"os"
)

func run(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: settings-export-v2 INPUT OUTPUT (output must not exist)")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return errors.New("cannot open input")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4<<20 {
		return errors.New("input must be a private, bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil {
		return errors.New("cannot read input")
	}
	output, err := settings.ExportV2(data)
	if err != nil {
		return err
	}
	dest, err := os.OpenFile(args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create output; existing files are never replaced")
	}
	ok := false
	defer func() {
		dest.Close()
		if !ok {
			os.Remove(args[1])
		}
	}()
	if _, err = dest.Write(output); err != nil {
		return errors.New("cannot write output")
	}
	if err = dest.Sync(); err != nil {
		return errors.New("cannot flush output")
	}
	if err = dest.Close(); err != nil {
		return errors.New("cannot close output")
	}
	ok = true
	return nil
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
