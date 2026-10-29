//go:build !webassets

package web

import (
	"io/fs"
	"os"
)

func assetsFS() (fs.FS, error) {
	for _, dir := range []string{"web/dist", "dist"} {
		if _, e := os.Stat(dir + "/index.html"); e == nil {
			return os.DirFS(dir), nil
		}
	}
	return nil, os.ErrNotExist
}
