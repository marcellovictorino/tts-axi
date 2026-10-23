package voices

import (
	"strings"
	"testing"
)

// Intent (spec: the default voices): Hugh then Bryony
// in English, Davi then Kauã, Manuela-Lu, Debora in Portuguese, Augustin then
// Coralie in French, and every one of them is a catalog voice.
func TestDefaultsAreCatalogVoices(t *testing.T) {
	for lang, names := range Defaults {
		for _, n := range names {
			v, ok := Find(n)
			if !ok || v.Lang != lang {
				t.Errorf("%s default %q is not a %s catalog voice", lang, n, lang)
			}
		}
	}
	// People type "Debora"; the catalog spells it "Débora".
	if v, ok := Find("Debora"); !ok || v.Name != "Débora" {
		t.Errorf("Debora resolved to %+v", v)
	}
	if v, ok := Find("manuela lu"); !ok || v.Name != "Manuela-Lu" {
		t.Errorf("manuela lu resolved to %+v", v)
	}
}

// Intent (render: speakers map to the saved voices in order): unnamed
// speakers take the defaults in order, a voice-named speaker keeps its voice,
// and a speaker beyond the defaults is a clear error.
func TestCaster(t *testing.T) {
	set, _ := NewSet(nil)
	c, err := NewCaster(set, "en", map[string]string{"Guest": "Zoey"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ speaker, voice string }{
		{"Host", "Hugh"}, {"Guest", "Zoey"}, {"Davi", "Davi"}, {"Co-host", "Bryony"}, {"Host", "Hugh"},
	}
	for _, tc := range cases {
		v, err := c.Voice(tc.speaker)
		if err != nil || v.Name != tc.voice {
			t.Errorf("%s: got %s (%v), want %s", tc.speaker, v.Name, err, tc.voice)
		}
	}
	if _, err := c.Voice("Producer"); err == nil || !strings.Contains(err.Error(), "--voice") {
		t.Errorf("a third unnamed English speaker should ask for a voice, got %v", err)
	}
}

func TestSettingsOverrideTheOrder(t *testing.T) {
	set, err := NewSet(map[string][]string{"en": {"bryony", "Hugh"}})
	if err != nil || set.Default("en") != "Bryony" {
		t.Fatalf("got %v, %v", set.Order("en"), err)
	}
	if _, err := NewSet(map[string][]string{"en": {"Davi"}}); err == nil {
		t.Error("a Portuguese voice in the English order should be refused")
	}
}
