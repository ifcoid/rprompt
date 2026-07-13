package telegram

import (
	"strings"
	"testing"
)

func TestMdToHTMLInline(t *testing.T) {
	cases := map[string]string{
		"**tebal**":        "<b>tebal</b>",
		"*miring*":         "<i>miring</i>",
		"~~coret~~":        "<s>coret</s>",
		"pakai `kode` ya":  "pakai <code>kode</code> ya",
		"# Judul":          "<b>Judul</b>",
		"## Sub judul":     "<b>Sub judul</b>",
		"- butir":          "• butir",
		"* butir bintang":  "• butir bintang",
		"[tautan](http://x)": `<a href="http://x">tautan</a>`,
	}
	for in, want := range cases {
		if got := mdToHTML(in); got != want {
			t.Errorf("mdToHTML(%q) = %q, mau %q", in, got, want)
		}
	}
}

func TestMdToHTMLEscapesSpecials(t *testing.T) {
	got := mdToHTML("a < b & c > d")
	if got != "a &lt; b &amp; c &gt; d" {
		t.Errorf("escaping salah: %q", got)
	}
}

func TestMdToHTMLNoFormatInsideCode(t *testing.T) {
	got := mdToHTML("`a**b**c`")
	if got != "<code>a**b**c</code>" {
		t.Errorf("penanda di dalam kode tidak boleh diformat: %q", got)
	}
}

func TestMdToHTMLCodeBlock(t *testing.T) {
	got := mdToHTML("```go\nfmt.Println(\"x\")\n```")
	want := "<pre>fmt.Println(\"x\")</pre>" // baris bahasa "go" dibuang; kutip tak di-escape
	if got != want {
		t.Errorf("blok kode = %q, mau %q", got, want)
	}
}

// Blok kode yang belum ditutup (kondisi streaming) harus tetap menghasilkan
// tag <pre> yang seimbang.
func TestMdToHTMLUnclosedCodeBlockBalanced(t *testing.T) {
	got := mdToHTML("teks\n```\nkode belum selesai")
	if strings.Count(got, "<pre>") != strings.Count(got, "</pre>") {
		t.Errorf("tag <pre> tidak seimbang: %q", got)
	}
	if !strings.Contains(got, "kode belum selesai") {
		t.Errorf("isi kode hilang: %q", got)
	}
}

// Penanda bold yang menggantung (streaming) dibiarkan literal, output seimbang.
func TestMdToHTMLDanglingBoldLiteral(t *testing.T) {
	got := mdToHTML("mulai **tebal belum tutup")
	if strings.Contains(got, "<b>") {
		t.Errorf("bold menggantung tidak boleh membuka tag: %q", got)
	}
}

func TestMdToHTMLTableToPre(t *testing.T) {
	md := "| A | B |\n|---|---|\n| 1 | 2 |"
	got := mdToHTML(md)
	if !strings.HasPrefix(got, "<pre>") || !strings.HasSuffix(got, "</pre>") {
		t.Errorf("tabel harus jadi blok <pre>: %q", got)
	}
	if strings.Contains(got, "---") {
		t.Errorf("baris pemisah tabel harus dibuang: %q", got)
	}
}
