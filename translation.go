package imports

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/translation"
)

// The sentences of this package's screens, messages and activity descriptions.
// They are embedded and not published: an application that wants other words
// writes them in its own catalogue, under the same keys, and Config.Translator
// is asked first. One override path, the one the application already has.

//go:embed resources/lang
var langSources embed.FS

// TranslationGroup is the file every key of this package lives in, and the
// prefix of every key: "imports.screen.index_title".
const TranslationGroup = "imports"

var shipped, shippedLocales = mustShipped()

func mustShipped() (*translation.Translator, []string) {
	root, err := fs.Sub(langSources, "resources/lang")
	if err != nil {
		panic("imports: the embedded catalogue has no lang directory: " + err.Error())
	}
	loader, err := translation.NewFileLoader(root, ".")
	if err != nil {
		panic("imports: the embedded catalogue does not parse: " + err.Error())
	}
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		panic("imports: the embedded catalogue cannot be listed: " + err.Error())
	}
	locales := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			locales = append(locales, entry.Name())
		}
	}
	sort.Strings(locales)
	if len(locales) == 0 {
		panic("imports: the embedded catalogue holds no locale, so every screen would draw its keys")
	}
	return translation.New(loader, FallbackLocale, FallbackLocale), locales
}

// Locales are the languages the package ships.
func Locales() []string { return append([]string(nil), shippedLocales...) }

// Lines are the sentences this package ships for one locale, keyed the way a
// translator asks for them ("imports.validation.required"), and nil for a locale
// it does not ship. An application reads them to see what there is to
// override, or to check a catalogue of its own against them.
func Lines(locale string) translation.Lines {
	lines := shipped.GetLoader().Load(locale, TranslationGroup, "*")
	if len(lines) == 0 {
		return nil
	}
	out := make(translation.Lines, len(lines))
	for item, line := range lines {
		out[TranslationGroup+"."+item] = line
	}
	return out
}

// Labels are the sentences of this package in one locale.
type Labels struct {
	Locale     string
	translator *translation.Translator
}

// labelsOf are the sentences the configuration asks for.
func labelsOf(c Config) Labels {
	locale := strings.TrimSpace(c.Locale)
	if locale == "" {
		locale = DefaultLocale
	}
	return Labels{Locale: locale, translator: c.Translator}
}

// Labels answers the sentences of the module's locale.
func (m *Module) Labels() Labels { return labelsOf(m.cfg) }

// T reads a key of this package: "screen.index_title".
func (l Labels) T(key string) string { return l.line(key, nil) }

// line reads a key with its placeholders filled, asking the application's
// translator first and the shipped catalogue after it.
func (l Labels) line(key string, replace translation.Replace) string {
	full := TranslationGroup + "." + key
	if l.translator != nil && l.translator.Has(l.Locale, full) {
		return l.translator.Get(l.Locale, full, replace)
	}
	return shipped.Get(l.Locale, full, replace)
}

// has says whether any catalogue has a line for the key.
func (l Labels) has(key string) bool {
	full := TranslationGroup + "." + key
	if l.translator != nil && l.translator.Has(l.Locale, full) {
		return true
	}
	return shipped.Has(l.Locale, full) || shipped.Has(FallbackLocale, full)
}

// Status is the label of a run's status.
func (l Labels) Status(s Status) string { return l.T("status." + string(s)) }

// Kind is the label of what a run does: importing or exporting.
func (l Labels) Kind(kind string) string { return l.T("kind." + kind) }

// FailureText is the sentence a run's failure code is told in. A code with no
// line, such as one a later version wrote, reads as the generic failure.
func (l Labels) FailureText(code string) string {
	if code == "" {
		return ""
	}
	if l.has("failure." + code) {
		return l.T("failure." + code)
	}
	return l.T("failure.unknown")
}

// Field is what a person reads in place of an input's name, in a summary of the
// rejected fields: "subject" reads "Assunto". A field with no line reads the
// way the input was named.
func (l Labels) Field(name string) string {
	if l.has("field." + name) {
		return l.T("field." + name)
	}
	words := strings.ReplaceAll(name, "_", " ")
	if words == "" {
		return words
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// Summary is one line per rejected message, the field named before it, for the
// banner above a form. Nil when nothing was rejected, so a layout that ranges
// over it draws no banner.
func (l Labels) Summary(errs validation.Errors) []string {
	if len(errs) == 0 {
		return nil
	}
	out := make([]string, 0, len(errs))
	for _, field := range errs.Keys() {
		for _, msg := range errs.Get(field) {
			out = append(out, l.line("validation.summary", translation.Replace{"field": l.Field(field), "message": msg}))
		}
	}
	return out
}

// message is a validation message of this package, in the locale of the
// labels.
func (l Labels) message(key string, replace ...translation.Replace) string {
	var r translation.Replace
	if len(replace) > 0 {
		r = replace[0]
	}
	return l.line("validation."+key, r)
}

// invalid is a rejected input with one message on one field.
func (l Labels) invalid(field, key string, replace ...translation.Replace) validation.Errors {
	return validation.Errors{field: {l.message(key, replace...)}}
}

// F is a line of the catalogue with its values given as pairs, for a view that
// has the numbers and the names at hand: F("screen.upload_hint", "mb", "10").
func (l Labels) F(key string, pairs ...any) string {
	replace := translation.Replace{}
	for i := 0; i+1 < len(pairs); i += 2 {
		replace[fmt.Sprint(pairs[i])] = pairs[i+1]
	}
	return l.line(key, replace)
}
