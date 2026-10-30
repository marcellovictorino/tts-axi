// Package voices knows Phonon's voice catalog for English, French and
// Portuguese, the default voice order per language, and how script speakers
// are matched to voices.
package voices

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// catalogJSON is a snapshot of Phonon's catalog (ptts.catalog_voices(), ptts
// 0.4.1) for the three supported languages. //go:embed compiles the file into
// the binary.
//
//go:embed catalog.json
var catalogJSON []byte

// Voice is one catalog voice.
type Voice struct {
	Name        string `json:"name"`
	Lang        string `json:"lang"`
	Description string `json:"description"`
	// Ref and RefText are set only for a cloned voice: the reference clip's
	// WAV and its words. The catalog has none.
	Ref     string `json:"-"`
	RefText string `json:"-"`
}

// Cloned reports whether the voice is a clone, spoken by Qwen rather than Phonon.
func (v Voice) Cloned() bool { return v.Ref != "" }

// Model names the speech model behind the voice.
func (v Voice) Model() string {
	if v.Cloned() {
		return "qwen"
	}
	return "phonon"
}

// Languages are the supported language codes, in display order.
var Languages = []string{"en", "pt", "fr"}

// Defaults is the voice order per language: the first is the default voice,
// the next ones go to the second, third... speaker of a script.
var Defaults = map[string][]string{
	"en": {"Hugh", "Bryony"},
	"pt": {"Davi", "Kauã", "Manuela-Lu", "Débora"},
	"fr": {"Augustin", "Coralie"},
}

var catalog []Voice

func init() {
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic("voices: bad embedded catalog: " + err.Error())
	}
}

// Catalog returns every voice, sorted by language then name.
func Catalog() []Voice { return append([]Voice(nil), catalog...) }

// Set is the voice configuration in effect: the catalog plus the default
// order, which settings may override per language.
type Set struct {
	order  map[string][]string
	clones []Voice
}

// WithClones adds cloned voices, which are English, so they can be found by
// name like catalog voices.
func (s Set) WithClones(vs []Voice) Set {
	s.clones = append([]Voice(nil), vs...)
	return s
}

// Clones lists the cloned voices.
func (s Set) Clones() []Voice { return append([]Voice(nil), s.clones...) }

// Find looks a voice up by name among the catalog and the cloned voices.
func (s Set) Find(name string) (Voice, bool) {
	if v, ok := Find(name); ok {
		return v, true
	}
	key := fold(name)
	for _, v := range s.clones {
		if fold(v.Name) == key {
			return v, true
		}
	}
	return Voice{}, false
}

// NewSet applies per-language overrides to Defaults. Every name must be a
// catalog voice of that language.
func NewSet(overrides map[string][]string) (Set, error) {
	s := Set{order: map[string][]string{}}
	for lang, names := range Defaults {
		s.order[lang] = names
	}
	for lang, names := range overrides {
		if !IsLanguage(lang) {
			return s, fmt.Errorf("voices.%s: unknown language (valid: %s)", lang, strings.Join(Languages, ", "))
		}
		if len(names) == 0 {
			return s, fmt.Errorf("voices.%s: give at least one voice", lang)
		}
		resolved := make([]string, len(names))
		for i, n := range names {
			v, ok := Find(n)
			if !ok || v.Lang != lang {
				return s, fmt.Errorf("voices.%s: %q is not a %s catalog voice (see `tts-axi voices --lang %s`)", lang, n, lang, lang)
			}
			resolved[i] = v.Name
		}
		s.order[lang] = resolved
	}
	return s, nil
}

// Order returns the default voice order for a language.
func (s Set) Order(lang string) []string { return s.order[lang] }

// Default returns the default voice for a language.
func (s Set) Default(lang string) string { return s.order[lang][0] }

// Rank returns the 1-based position of a voice in its language's default
// order, or 0 when it is not one of the defaults.
func (s Set) Rank(v Voice) int {
	for i, n := range s.order[v.Lang] {
		if n == v.Name {
			return i + 1
		}
	}
	return 0
}

// IsLanguage reports whether code is a supported language.
func IsLanguage(code string) bool {
	for _, l := range Languages {
		if l == code {
			return true
		}
	}
	return false
}

// Find looks a voice up by name, ignoring case, accents and spacing, so that
// "debora", "Débora" and "DEBORA" are the same voice.
func Find(name string) (Voice, bool) {
	key := fold(name)
	for _, v := range catalog {
		if fold(v.Name) == key {
			return v, true
		}
	}
	return Voice{}, false
}

// Names lists the catalog names of one language, sorted, for error messages.
func Names(lang string) []string {
	var out []string
	for _, v := range catalog {
		if v.Lang == lang {
			out = append(out, v.Name)
		}
	}
	sort.Strings(out)
	return out
}

// fold lowercases, strips accents and drops spaces, dashes and underscores.
func fold(s string) string {
	// NFD splits "é" into "e" plus a combining accent, which runes.Remove
	// then drops.
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	var b strings.Builder
	for _, r := range strings.ToLower(out) {
		if r == ' ' || r == '-' || r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Caster gives each script speaker a voice: a speaker named after a catalog
// voice gets that voice, an explicit mapping wins over everything, and any
// other speaker takes the next unused default voice of the script's language.
type Caster struct {
	set      Set
	lang     string
	explicit map[string]Voice // folded speaker name -> voice
	assigned map[string]Voice
	used     map[string]bool
}

// NewCaster starts casting for a script in lang. mapping is speaker -> voice
// name, from the script's front matter or --voice Speaker=Voice flags.
func NewCaster(set Set, lang string, mapping map[string]string) (*Caster, error) {
	c := &Caster{set: set, lang: lang, explicit: map[string]Voice{}, assigned: map[string]Voice{}, used: map[string]bool{}}
	for speaker, name := range mapping {
		v, ok := set.Find(name)
		if !ok {
			return nil, fmt.Errorf("speaker %q: %q is not a voice (see `tts-axi voices`)", speaker, name)
		}
		c.explicit[fold(speaker)] = v
		c.used[v.Name] = true
	}
	return c, nil
}

// Voice returns the voice for a speaker. An empty speaker is the narrator,
// who takes the language's default voice.
func (c *Caster) Voice(speaker string) (Voice, error) {
	key := fold(speaker)
	if v, ok := c.explicit[key]; ok {
		return v, nil
	}
	if v, ok := c.assigned[key]; ok {
		return v, nil
	}
	if speaker != "" {
		if v, ok := c.set.Find(speaker); ok {
			c.assigned[key] = v
			c.used[v.Name] = true
			return v, nil
		}
	}
	for _, name := range c.set.Order(c.lang) {
		if !c.used[name] {
			v, _ := Find(name)
			c.assigned[key] = v
			c.used[name] = true
			return v, nil
		}
	}
	if speaker == "" {
		v, _ := Find(c.set.Default(c.lang))
		c.assigned[key] = v
		return v, nil
	}
	return Voice{}, fmt.Errorf("speaker %q has no voice: the %s default voices (%s) are all taken; name a voice for it, for example `voices: {%s: %s}` in the front matter or --voice '%s=%s'",
		speaker, c.lang, strings.Join(c.set.Order(c.lang), ", "), speaker, Names(c.lang)[0], speaker, Names(c.lang)[0])
}
