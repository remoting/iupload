package templates

import (
	"fmt"
	"text/template"
)

func init() {
}

type Templates struct {
	// The root path from which to load files. Required if template functions
	// accessing the file system are used (such as include). Default is
	// `{http.vars.root}` if set, or current working directory otherwise.
	FileRoot string `json:"file_root,omitempty"`

	// The MIME types for which to render templates. It is important to use
	// this if the route matchers do not exclude images or other binary files.
	// Default is text/plain, text/markdown, and text/html.
	MIMETypes []string `json:"mime_types,omitempty"`

	// The template action delimiters. If set, must be precisely two elements:
	// the opening and closing delimiters. Default: `["{{", "}}"]`
	Delimiters []string `json:"delimiters,omitempty"`

	customFuncs []template.FuncMap
}

// CustomFunctions is the interface for registering custom template functions.
type CustomFunctions interface {
	// CustomTemplateFunctions should return the mapping from custom function names to implementations.
	CustomTemplateFunctions() template.FuncMap
}

// Validate ensures t has a valid configuration.
func (t *Templates) Validate() error {
	if len(t.Delimiters) != 0 && len(t.Delimiters) != 2 {
		return fmt.Errorf("delimiters must consist of exactly two elements: opening and closing")
	}
	return nil
}
