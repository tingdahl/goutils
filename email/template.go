package email

import (
	"bytes"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"strings"
	"sync"
	texttemplate "text/template"
	"time"
)

var (
	ErrTemplateNotFound  = errors.New("email: template not found")
	ErrEmptyTemplateID   = errors.New("email: template ID cannot be empty")
	ErrEmptyTemplateBody = errors.New("email: template must contain at least HTML or Text content")
	ErrEmptySubject      = errors.New("email: template subject cannot be empty")
)

// DefaultFuncMap provides standard helper functions for templates.
var DefaultFuncMap = htmltemplate.FuncMap{
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"trim":  strings.TrimSpace,
	"formatDate": func(unixMs int64, layout string) string {
		if layout == "" {
			layout = time.RFC3339
		}
		return time.UnixMilli(unixMs).UTC().Format(layout)
	},
}

var textDefaultFuncMap = texttemplate.FuncMap{
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"trim":  strings.TrimSpace,
	"formatDate": func(unixMs int64, layout string) string {
		if layout == "" {
			layout = time.RFC3339
		}
		return time.UnixMilli(unixMs).UTC().Format(layout)
	},
}

// TemplateDef defines an email template before compilation into in-memory templates.
type TemplateDef struct {
	ID      string
	From    Address
	Subject string
	HTML    string
	Text    string
	Funcs   htmltemplate.FuncMap
}

// Template represents a compiled, executable in-memory email template.
type Template struct {
	id          string
	from        Address
	subjectTmpl *texttemplate.Template
	htmlTmpl    *htmltemplate.Template
	textTmpl    *texttemplate.Template
}

// ID returns the template ID.
func (t *Template) ID() string {
	return t.id
}

// From returns the default From address configured for this template.
func (t *Template) From() Address {
	return t.from
}

// Render executes the compiled templates against the provided data model.
func (t *Template) Render(data any) (subject, html, text string, err error) {
	var subBuf bytes.Buffer
	if err := t.subjectTmpl.Execute(&subBuf, data); err != nil {
		return "", "", "", fmt.Errorf("email: failed to render subject for template %q: %w", t.id, err)
	}
	subject = strings.TrimSpace(subBuf.String())
	if subject == "" {
		return "", "", "", fmt.Errorf("email: rendered subject for template %q is empty", t.id)
	}

	if t.htmlTmpl != nil {
		var htmlBuf bytes.Buffer
		if err := t.htmlTmpl.Execute(&htmlBuf, data); err != nil {
			return "", "", "", fmt.Errorf("email: failed to render html for template %q: %w", t.id, err)
		}
		html = htmlBuf.String()
	}

	if t.textTmpl != nil {
		var textBuf bytes.Buffer
		if err := t.textTmpl.Execute(&textBuf, data); err != nil {
			return "", "", "", fmt.Errorf("email: failed to render text for template %q: %w", t.id, err)
		}
		text = textBuf.String()
	}

	return subject, html, text, nil
}

// TemplateRegistry provides thread-safe in-memory storage for compiled email templates.
type TemplateRegistry struct {
	mu        sync.RWMutex
	templates map[string]*Template
}

// NewTemplateRegistry creates a new independent in-memory TemplateRegistry.
func NewTemplateRegistry() *TemplateRegistry {
	return &TemplateRegistry{
		templates: make(map[string]*Template),
	}
}

var defaultTemplateRegistry = NewTemplateRegistry()

// Register parses and adds a template definition to the registry.
func (r *TemplateRegistry) Register(def TemplateDef) error {
	id := strings.TrimSpace(def.ID)
	if id == "" {
		return ErrEmptyTemplateID
	}
	subject := strings.TrimSpace(def.Subject)
	if subject == "" {
		return ErrEmptySubject
	}
	if strings.TrimSpace(def.HTML) == "" && strings.TrimSpace(def.Text) == "" {
		return ErrEmptyTemplateBody
	}

	// Merge text funcs
	tfMap := make(texttemplate.FuncMap)
	for k, v := range textDefaultFuncMap {
		tfMap[k] = v
	}
	for k, v := range def.Funcs {
		tfMap[k] = v
	}

	subTmpl, err := texttemplate.New(id + "_subject").Funcs(tfMap).Parse(subject)
	if err != nil {
		return fmt.Errorf("email: invalid subject template for %q: %w", id, err)
	}

	var htmlTmpl *htmltemplate.Template
	if strings.TrimSpace(def.HTML) != "" {
		hfMap := make(htmltemplate.FuncMap)
		for k, v := range DefaultFuncMap {
			hfMap[k] = v
		}
		for k, v := range def.Funcs {
			hfMap[k] = v
		}

		htmlTmpl, err = htmltemplate.New(id + "_html").Funcs(hfMap).Parse(def.HTML)
		if err != nil {
			return fmt.Errorf("email: invalid html template for %q: %w", id, err)
		}
	}

	var textTmpl *texttemplate.Template
	if strings.TrimSpace(def.Text) != "" {
		textTmpl, err = texttemplate.New(id + "_text").Funcs(tfMap).Parse(def.Text)
		if err != nil {
			return fmt.Errorf("email: invalid text template for %q: %w", id, err)
		}
	}

	tmpl := &Template{
		id:          id,
		from:        def.From,
		subjectTmpl: subTmpl,
		htmlTmpl:    htmlTmpl,
		textTmpl:    textTmpl,
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.templates[id] = tmpl
	return nil
}

// Get retrieves a compiled template by ID.
func (r *TemplateRegistry) Get(id string) (*Template, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tmpl, ok := r.templates[strings.TrimSpace(id)]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrTemplateNotFound, id)
	}
	return tmpl, nil
}

// Clear removes all registered templates from the registry.
func (r *TemplateRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.templates = make(map[string]*Template)
}

// RegisterTemplate registers a template in the package-level default registry.
func RegisterTemplate(def TemplateDef) error {
	return defaultTemplateRegistry.Register(def)
}

// MustRegisterTemplate registers a template in the package-level registry and panics on error.
func MustRegisterTemplate(def TemplateDef) {
	if err := RegisterTemplate(def); err != nil {
		panic(fmt.Sprintf("MustRegisterTemplate: %v", err))
	}
}

// GetTemplate retrieves a template from the package-level default registry.
func GetTemplate(id string) (*Template, error) {
	return defaultTemplateRegistry.Get(id)
}

// ClearTemplatesForTesting clears the package-level default registry.
func ClearTemplatesForTesting() {
	defaultTemplateRegistry.Clear()
}
