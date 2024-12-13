package fileserver

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
)

func (fsrv *FileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	if runtime.GOOS == "windows" {
		// reject paths with Alternate Data Streams (ADS)
		if strings.Contains(r.URL.Path, ":") {
			return Error(http.StatusBadRequest, fmt.Errorf("illegal ADS path"))
		}
		// reject paths with "8.3" short names
		trimmedPath := strings.TrimRight(r.URL.Path, ". ") // Windows ignores trailing dots and spaces, sigh
		if len(path.Base(trimmedPath)) <= 12 && strings.Contains(trimmedPath, "~") {
			return Error(http.StatusBadRequest, fmt.Errorf("illegal short name"))
		}
	}
	root := ""
	filename := strings.TrimSuffix(SanitizedPathJoin(root, r.URL.Path), "/")
	fsFs := os.DirFS(fsrv.Root)
	fileSystem := os.DirFS(fsrv.Root)
	info, err := fs.Stat(fsFs, filename)
	if err != nil {
		return Error(http.StatusInternalServerError, err)
	}
	if info.IsDir() && fsrv.Browse != nil {
		return fsrv.serveBrowse(fileSystem, root, filename, w, r)
	} else {
		return errors.New("browse error")
	}
}
