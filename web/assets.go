package web

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
)

func AssetURL(name string) string {
	return assetURLs[name]
}

var assetURLs = func() map[string]string {
	urls := make(map[string]string)
	err := fs.WalkDir(Static, "static", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := Static.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		urls[path[len("static/"):]] = fmt.Sprintf("/%s?v=%x", path, digest[:12])
		return nil
	})
	if err != nil {
		panic(err)
	}
	return urls
}()
