package fileserver

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"io/fs"
	"iupload/templates"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
)

const (
	defaultDirEntryLimit = 10000
	separator            = string(filepath.Separator)
)

//go:embed browse.html
var BrowseTemplate string

type Browse struct {
	TemplateFile   string   `json:"template_file,omitempty"`
	RevealSymlinks bool     `json:"reveal_symlinks,omitempty"`
	SortOptions    []string `json:"sort,omitempty"`
	FileLimit      int      `json:"file_limit,omitempty"`
}
type FileServer struct {
	Root       string   `json:"root,omitempty"`
	IndexNames []string `json:"index_names,omitempty"`
	Browse     *Browse  `json:"browse,omitempty"`
}

func SanitizedPathJoin(root, reqPath string) string {
	if root == "" {
		root = "."
	}

	relPath := path.Clean("/" + reqPath)[1:] // clean path and trim the leading /
	if relPath != "" && !filepath.IsLocal(relPath) {
		// path is unsafe (see https://github.com/golang/go/issues/56336#issuecomment-1416214885)
		return root
	}

	path := filepath.Join(root, filepath.FromSlash(relPath))

	// filepath.Join also cleans the path, and cleaning strips
	// the trailing slash, so we need to re-add it afterwards.
	// if the length is 1, then it's a path to the root,
	// and that should return ".", so we don't append the separator.
	if strings.HasSuffix(reqPath, "/") && len(reqPath) > 1 {
		path += separator
	}

	return path
}
func redirect(w http.ResponseWriter, r *http.Request, toPath string) error {
	for strings.HasPrefix(toPath, "//") {
		// prevent path-based open redirects
		toPath = strings.TrimPrefix(toPath, "/")
	}
	// preserve the query string if present
	if r.URL.RawQuery != "" {
		toPath += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, toPath, http.StatusPermanentRedirect)
	return nil
}

// isSymlinkTargetDir returns true if f's symbolic link target
// is a directory.
func (fsrv *FileServer) isSymlinkTargetDir(fileSystem fs.FS, f fs.FileInfo, root, urlPath string) bool {
	if !isSymlink(f) {
		return false
	}
	target := SanitizedPathJoin(root, path.Join(urlPath, f.Name()))
	targetInfo, err := fs.Stat(fileSystem, target)
	if err != nil {
		return false
	}
	return targetInfo.IsDir()
}

func (fsrv *FileServer) serveBrowse(fileSystem fs.FS, root, dirPath string, w http.ResponseWriter, r *http.Request) error {
	origReq := r
	if r.URL.Path == "" || path.Base(origReq.URL.Path) == path.Base(r.URL.Path) {
		if !strings.HasSuffix(origReq.URL.Path, "/") {
			return redirect(w, r, origReq.URL.Path+"/")
		}
	}

	dir, err := fileSystem.Open(dirPath)
	if err != nil {
		return err
	}
	defer dir.Close()

	// TODO: not entirely sure if path.Clean() is necessary here but seems like a safe plan (i.e. /%2e%2e%2f) - someone could verify this
	listing, err := fsrv.loadDirectoryContents(fileSystem, dir.(fs.ReadDirFile), root, path.Clean(r.URL.EscapedPath()))
	if err != nil {
		return err
	}

	fsrv.browseApplyQueryParams(w, r, listing)

	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)

	tplCtx := &templateContext{
		TemplateContext: templates.TemplateContext{
			Req:        r,
			RespHeader: templates.WrappedHeader{Header: w.Header()},
		},
		browseTemplateContext: listing,
	}

	tpl, err := fsrv.makeBrowseTemplate(tplCtx)
	if err != nil {
		return fmt.Errorf("parsing browse template: %v", err)
	}
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return tpl.Execute(w, tplCtx)
}

func (fsrv *FileServer) loadDirectoryContents(fileSystem fs.FS, dir fs.ReadDirFile, root, urlPath string) (*browseTemplateContext, error) {
	dirLimit := defaultDirEntryLimit
	files, err := dir.ReadDir(dirLimit)
	if err != nil && err != io.EOF {
		return nil, err
	}

	// user can presumably browse "up" to parent folder if path is longer than "/"
	canGoUp := len(urlPath) > 1

	return fsrv.directoryListing(fileSystem, files, canGoUp, root, urlPath), nil
}

// browseApplyQueryParams applies query parameters to the listing.
// It mutates the listing and may set cookies.
func (fsrv *FileServer) browseApplyQueryParams(w http.ResponseWriter, r *http.Request, listing *browseTemplateContext) {
	var orderParam, sortParam string

	// The configs in Caddyfile have lower priority than Query params,
	// so put it at first.
	for idx, item := range fsrv.Browse.SortOptions {
		// Only `sort` & `order`, 2 params are allowed
		if idx >= 2 {
			break
		}
		switch item {
		case sortByName, sortByNameDirFirst, sortBySize, sortByTime:
			sortParam = item
		case sortOrderAsc, sortOrderDesc:
			orderParam = item
		}
	}

	layoutParam := r.URL.Query().Get("layout")
	limitParam := r.URL.Query().Get("limit")
	offsetParam := r.URL.Query().Get("offset")
	sortParamTmp := r.URL.Query().Get("sort")
	if sortParamTmp != "" {
		sortParam = sortParamTmp
	}
	orderParamTmp := r.URL.Query().Get("order")
	if orderParamTmp != "" {
		orderParam = orderParamTmp
	}

	switch layoutParam {
	case "list", "grid", "":
		listing.Layout = layoutParam
	default:
		listing.Layout = "list"
	}

	// figure out what to sort by
	switch sortParam {
	case "":
		sortParam = sortByNameDirFirst
		if sortCookie, sortErr := r.Cookie("sort"); sortErr == nil {
			sortParam = sortCookie.Value
		}
	case sortByName, sortByNameDirFirst, sortBySize, sortByTime:
		http.SetCookie(w, &http.Cookie{Name: "sort", Value: sortParam, Secure: r.TLS != nil})
	}

	// then figure out the order
	switch orderParam {
	case "":
		orderParam = sortOrderAsc
		if orderCookie, orderErr := r.Cookie("order"); orderErr == nil {
			orderParam = orderCookie.Value
		}
	case sortOrderAsc, sortOrderDesc:
		http.SetCookie(w, &http.Cookie{Name: "order", Value: orderParam, Secure: r.TLS != nil})
	}

	// finally, apply the sorting and limiting
	listing.applySortAndLimit(sortParam, orderParam, limitParam, offsetParam)
}

// makeBrowseTemplate creates the template to be used for directory listings.
func (fsrv *FileServer) makeBrowseTemplate(tplCtx *templateContext) (*template.Template, error) {
	var tpl *template.Template
	var err error

	if fsrv.Browse.TemplateFile != "" {
		tpl = tplCtx.NewTemplate(path.Base(fsrv.Browse.TemplateFile))
		tpl, err = tpl.ParseFiles(fsrv.Browse.TemplateFile)
		if err != nil {
			return nil, fmt.Errorf("parsing browse template file: %v", err)
		}
	} else {
		tpl = tplCtx.NewTemplate("default_listing")
		tpl, err = tpl.Parse(BrowseTemplate)
		if err != nil {
			return nil, fmt.Errorf("parsing default browse template: %v", err)
		}
	}

	return tpl, nil
}

// isSymlink return true if f is a symbolic link.
func isSymlink(f fs.FileInfo) bool {
	return f.Mode()&os.ModeSymlink != 0
}

// templateContext powers the context used when evaluating the browse template.
// It combines browse-specific features with the standard templates handler
// features.
type templateContext struct {
	templates.TemplateContext
	*browseTemplateContext
}

// bufPool is used to increase the efficiency of file listings.
var bufPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}
