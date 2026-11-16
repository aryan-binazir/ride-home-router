package server

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

func staticFileHandler(files fs.FS) (http.Handler, error) {
	hashes := make(map[string]string)
	err := fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		hashes[name] = fmt.Sprintf("%x", digest[:12])
		return nil
	})
	if err != nil {
		return nil, err
	}
	next := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		versions := r.URL.Query()["v"]
		hash := hashes[strings.TrimPrefix(r.URL.Path, "/")]
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && hash != "" && len(versions) == 1 && versions[0] == hash {
			w = &assetCacheWriter{ResponseWriter: w}
		}
		next.ServeHTTP(w, r)
	}), nil
}

type assetCacheWriter struct{ http.ResponseWriter }

func (w *assetCacheWriter) WriteHeader(status int) {
	if status == http.StatusOK || status == http.StatusPartialContent {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.ResponseWriter.WriteHeader(status)
}
