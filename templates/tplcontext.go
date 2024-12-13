package templates

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/Masterminds/sprig/v3"
	"github.com/dustin/go-humanize"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
)

var bufPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

var sprigFuncMap = sprig.TxtFuncMap()

type TemplateContext struct {
	Root        http.FileSystem
	Req         *http.Request
	Args        []any
	RespHeader  WrappedHeader
	CustomFuncs []template.FuncMap
	config      *Templates
	tpl         *template.Template
}

// NewTemplate returns a new template intended to be evaluated with this
// context, as it is initialized with configuration from this context.
func (c *TemplateContext) NewTemplate(tplName string) *template.Template {
	c.tpl = template.New(tplName).Option("missingkey=zero")

	// customize delimiters, if applicable
	if c.config != nil && len(c.config.Delimiters) == 2 {
		c.tpl.Delims(c.config.Delimiters[0], c.config.Delimiters[1])
	}

	// add sprig library
	c.tpl.Funcs(sprigFuncMap)

	// add all custom functions
	for _, funcMap := range c.CustomFuncs {
		c.tpl.Funcs(funcMap)
	}

	// add our own library
	c.tpl.Funcs(template.FuncMap{
		"include":  c.funcInclude,
		"readFile": c.funcReadFile,
		"import":   c.funcImport,
		//"httpInclude":      c.funcHTTPInclude,
		"stripHTML": c.funcStripHTML,
		//"markdown":         c.funcMarkdown,
		//"splitFrontMatter": c.funcSplitFrontMatter,
		"listFiles": c.funcListFiles,
		"fileStat":  c.funcFileStat,
		"env":       c.funcEnv,
		//"placeholder":      c.funcPlaceholder,
		//"ph":               c.funcPlaceholder, // shortcut
		"fileExists": c.funcFileExists,
		"httpError":  c.funcHTTPError,
		"humanize":   c.funcHumanize,
		"maybe":      c.funcMaybe,
		"pathEscape": url.PathEscape,
	})
	return c.tpl
}

// OriginalReq returns the original, unmodified, un-rewritten request as
// it originally came in over the wire.
func (c TemplateContext) OriginalReq() http.Request {
	return *c.Req
}

// funcInclude returns the contents of filename relative to the site root and renders it in place.
// Note that included files are NOT escaped, so you should only include
// trusted files. If it is not trusted, be sure to use escaping functions
// in your template.
func (c TemplateContext) funcInclude(filename string, args ...any) (string, error) {
	bodyBuf := bufPool.Get().(*bytes.Buffer)
	bodyBuf.Reset()
	defer bufPool.Put(bodyBuf)

	err := c.readFileToBuffer(filename, bodyBuf)
	if err != nil {
		return "", err
	}

	c.Args = args

	err = c.executeTemplateInBuffer(filename, bodyBuf)
	if err != nil {
		return "", err
	}

	return bodyBuf.String(), nil
}

// funcReadFile returns the contents of a filename relative to the site root.
// Note that included files are NOT escaped, so you should only include
// trusted files. If it is not trusted, be sure to use escaping functions
// in your template.
func (c TemplateContext) funcReadFile(filename string) (string, error) {
	bodyBuf := bufPool.Get().(*bytes.Buffer)
	bodyBuf.Reset()
	defer bufPool.Put(bodyBuf)

	err := c.readFileToBuffer(filename, bodyBuf)
	if err != nil {
		return "", err
	}

	return bodyBuf.String(), nil
}

// readFileToBuffer reads a file into a buffer
func (c TemplateContext) readFileToBuffer(filename string, bodyBuf *bytes.Buffer) error {
	if c.Root == nil {
		return fmt.Errorf("root file system not specified")
	}

	file, err := c.Root.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(bodyBuf, file)
	if err != nil {
		return err
	}

	return nil
}

// funcImport parses the filename into the current template stack. The imported
// file will be rendered within the current template by calling {{ block }} or
// {{ template }} from the standard template library. If the imported file has
// no {{ define }} blocks, the name of the import will be the path
func (c *TemplateContext) funcImport(filename string) (string, error) {
	bodyBuf := bufPool.Get().(*bytes.Buffer)
	bodyBuf.Reset()
	defer bufPool.Put(bodyBuf)

	err := c.readFileToBuffer(filename, bodyBuf)
	if err != nil {
		return "", err
	}

	_, err = c.tpl.Parse(bodyBuf.String())
	if err != nil {
		return "", err
	}
	return "", nil
}

func (c *TemplateContext) executeTemplateInBuffer(tplName string, buf *bytes.Buffer) error {
	c.NewTemplate(tplName)

	_, err := c.tpl.Parse(buf.String())
	if err != nil {
		return err
	}

	buf.Reset() // reuse buffer for output

	return c.tpl.Execute(buf, c)
}

func (TemplateContext) funcEnv(varName string) string {
	return os.Getenv(varName)
}

// Cookie gets the value of a cookie with name.
func (c TemplateContext) Cookie(name string) string {
	cookies := c.Req.Cookies()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// RemoteIP gets the IP address of the connection's remote IP.
func (c TemplateContext) RemoteIP() string {
	ip, _, err := net.SplitHostPort(c.Req.RemoteAddr)
	if err != nil {
		return c.Req.RemoteAddr
	}
	return ip
}

// Host returns the hostname portion of the Host header
// from the HTTP request.
func (c TemplateContext) Host() (string, error) {
	host, _, err := net.SplitHostPort(c.Req.Host)
	if err != nil {
		if !strings.Contains(c.Req.Host, ":") {
			// common with sites served on the default port 80
			return c.Req.Host, nil
		}
		return "", err
	}
	return host, nil
}

// funcStripHTML returns s without HTML tags. It is fairly naive
// but works with most valid HTML inputs.
func (TemplateContext) funcStripHTML(s string) string {
	var buf bytes.Buffer
	var inTag, inQuotes bool
	var tagStart int
	for i, ch := range s {
		if inTag {
			if ch == '>' && !inQuotes {
				inTag = false
			} else if ch == '<' && !inQuotes {
				// false start
				buf.WriteString(s[tagStart:i])
				tagStart = i
			} else if ch == '"' {
				inQuotes = !inQuotes
			}
			continue
		}
		if ch == '<' {
			inTag = true
			tagStart = i
			continue
		}
		buf.WriteRune(ch)
	}
	if inTag {
		// false start
		buf.WriteString(s[tagStart:])
	}
	return buf.String()
}

// funcListFiles reads and returns a slice of names from the given
// directory relative to the root of c.
func (c TemplateContext) funcListFiles(name string) ([]string, error) {
	if c.Root == nil {
		return nil, fmt.Errorf("root file system not specified")
	}

	dir, err := c.Root.Open(path.Clean(name))
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	stat, err := dir.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("%v is not a directory", name)
	}

	dirInfo, err := dir.Readdir(0)
	if err != nil {
		return nil, err
	}

	names := make([]string, len(dirInfo))
	for i, fileInfo := range dirInfo {
		names[i] = fileInfo.Name()
	}

	return names, nil
}

// funcFileExists returns true if filename can be opened successfully.
func (c TemplateContext) funcFileExists(filename string) (bool, error) {
	if c.Root == nil {
		return false, fmt.Errorf("root file system not specified")
	}
	file, err := c.Root.Open(filename)
	if err == nil {
		file.Close()
		return true, nil
	}
	return false, nil
}

// funcFileStat returns Stat of a filename
func (c TemplateContext) funcFileStat(filename string) (fs.FileInfo, error) {
	if c.Root == nil {
		return nil, fmt.Errorf("root file system not specified")
	}

	file, err := c.Root.Open(path.Clean(filename))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return file.Stat()
}

// funcHTTPError returns a structured HTTP handler error. EXPERIMENTAL; SUBJECT TO CHANGE.
// Example usage: `{{if not (fileExists $includeFile)}}{{httpError 404}}{{end}}`
func (c TemplateContext) funcHTTPError(statusCode int) (bool, error) {
	// Delete some headers that may have been set by the underlying
	// handler (such as file_server) which may break the error response.
	c.RespHeader.Header.Del("Content-Length")
	c.RespHeader.Header.Del("Content-Type")
	c.RespHeader.Header.Del("Etag")
	c.RespHeader.Header.Del("Last-Modified")
	c.RespHeader.Header.Del("Accept-Ranges")

	return false, errors.New(string(statusCode))
}

// funcHumanize transforms size and time inputs to a human readable format.
//
// Size inputs are expected to be integers, and are formatted as a
// byte size, such as "83 MB".
//
// Time inputs are parsed using the given layout (default layout is RFC1123Z)
// and are formatted as a relative time, such as "2 weeks ago".
// See https://pkg.go.dev/time#pkg-constants for time layout docs.
func (c TemplateContext) funcHumanize(formatType, data string) (string, error) {
	// The format type can optionally be followed
	// by a colon to provide arguments for the format
	parts := strings.Split(formatType, ":")

	switch parts[0] {
	case "size":
		dataint, dataerr := strconv.ParseUint(data, 10, 64)
		if dataerr != nil {
			return "", fmt.Errorf("humanize: size cannot be parsed: %s", dataerr.Error())
		}
		return humanize.Bytes(dataint), nil

	case "time":
		timelayout := time.RFC1123Z
		if len(parts) > 1 {
			timelayout = parts[1]
		}

		dataint, dataerr := time.Parse(timelayout, data)
		if dataerr != nil {
			return "", fmt.Errorf("humanize: time cannot be parsed: %s", dataerr.Error())
		}
		return humanize.Time(dataint), nil
	}

	return "", fmt.Errorf("no know function was given")
}

// funcMaybe invokes the plugged-in function named functionName if it is plugged in
// (is a module in the 'http.handlers.templates.functions' namespace). If it is not
// available, a log message is emitted.
//
// The first argument is the function name, and the rest of the arguments are
// passed on to the actual function.
//
// This function is useful for executing templates that use components that may be
// considered as optional in some cases (like during local development) where you do
// not want to require everyone to have a custom Caddy build to be able to execute
// your template.
//
// NOTE: This function is EXPERIMENTAL and subject to change or removal.
func (c TemplateContext) funcMaybe(functionName string, args ...any) (any, error) {
	for _, funcMap := range c.CustomFuncs {
		if fn, ok := funcMap[functionName]; ok {
			val := reflect.ValueOf(fn)
			if val.Kind() != reflect.Func {
				continue
			}
			argVals := make([]reflect.Value, len(args))
			for i, arg := range args {
				argVals[i] = reflect.ValueOf(arg)
			}
			returnVals := val.Call(argVals)
			switch len(returnVals) {
			case 0:
				return "", nil
			case 1:
				return returnVals[0].Interface(), nil
			case 2:
				var err error
				if !returnVals[1].IsNil() {
					err = returnVals[1].Interface().(error)
				}
				return returnVals[0].Interface(), err
			default:
				return nil, fmt.Errorf("maybe %s: invalid number of return values: %d", functionName, len(returnVals))
			}
		}
	}
	return "", nil
}

// WrappedHeader wraps niladic functions so that they
// can be used in templates. (Template functions must
// return a value.)
type WrappedHeader struct{ http.Header }

// Add adds a header field value, appending val to
// existing values for that field. It returns an
// empty string.
func (h WrappedHeader) Add(field, val string) string {
	h.Header.Add(field, val)
	return ""
}

// Set sets a header field value, overwriting any
// other values for that field. It returns an
// empty string.
func (h WrappedHeader) Set(field, val string) string {
	h.Header.Set(field, val)
	return ""
}

// Del deletes a header field. It returns an empty string.
func (h WrappedHeader) Del(field string) string {
	h.Header.Del(field)
	return ""
}
