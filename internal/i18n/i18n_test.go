package i18n

import (
	"os"
	"testing"
	"testing/fstest"
)

func bundle(t *testing.T) *Bundle {
	t.Helper()
	b, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return b
}

func TestShippedCatalogs(t *testing.T) {
	b := bundle(t)
	if !b.Has("zh") || !b.Has("en-US") == false {
		t.Fatalf("zh missing from %v", b.Langs())
	}
	if b.Name("zh") != "中文" {
		t.Errorf("zh name = %q", b.Name("zh"))
	}
	if b.Name("en") != "English" {
		t.Errorf("en name = %q", b.Name("en"))
	}
	if got := b.T("zh", "Hunt board"); got != "狩猎面板" {
		t.Errorf("T(zh, Hunt board) = %q", got)
	}
	// A message the catalog doesn't have reads as its English source.
	if got := b.T("zh", "Nothing translates this"); got != "Nothing translates this" {
		t.Errorf("fallback = %q", got)
	}
	// A language without a catalog does too.
	if got := b.T("fr", "Hunt board"); got != "Hunt board" {
		t.Errorf("unknown language = %q", got)
	}
}

func TestTFFormats(t *testing.T) {
	b := bundle(t)
	// A format string is translated as one message, verbs and all; a message
	// without a translation still fills in its arguments.
	if got := b.TF("zh", "Updated %s", "now"); got != "更新于 now" {
		t.Errorf("TF = %q", got)
	}
	if got := b.TF("zh", "Nothing %s here", "to see"); got != "Nothing to see here" {
		t.Errorf("TF fallback = %q", got)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"zh-CN": "zh", "zh_Hans": "zh", "EN-us": "en", " zh ": "zh", "de": "de", "": "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatch(t *testing.T) {
	langs := []string{"en", "zh"}
	tests := []struct{ header, want string }{
		{"", ""},
		{"zh-CN,zh;q=0.9,en;q=0.8", "zh"},
		{"en-GB,en;q=0.9", "en"},
		{"de-DE,de;q=0.9,zh;q=0.5", "zh"},
		{"fr-FR", ""},
		{"zh;q=0", ""},
		{"en;q=0.2, zh;q=0.8", "zh"},
		{"*", ""},
	}
	for _, tt := range tests {
		if got := Match(tt.header, langs); got != tt.want {
			t.Errorf("Match(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestNewFromFS(t *testing.T) {
	fsys := fstest.MapFS{
		"cat/zh.json":  &fstest.MapFile{Data: []byte(`{"name":"中文","messages":{"Hi":"你好"}}`)},
		"cat/bad.json": &fstest.MapFile{Data: []byte("{")},
	}
	if _, err := New(fsys, "cat"); err == nil {
		t.Fatal("want an error for a broken catalog")
	}
	fsys = fstest.MapFS{"cat/zh.json": &fstest.MapFile{Data: []byte(`{"name":"中文","messages":{"Hi":"你好"}}`)}}
	b, err := New(fsys, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if b.T("zh", "Hi") != "你好" {
		t.Errorf("got %q", b.T("zh", "Hi"))
	}
	if _, err := os.Stat("catalog/zh.json"); err != nil {
		t.Errorf("catalog not embedded from disk: %v", err)
	}
}
