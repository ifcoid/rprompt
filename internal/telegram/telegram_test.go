package telegram

import (
	"strings"
	"testing"
)

func TestSplitTextShort(t *testing.T) {
	got := splitText("halo dunia", 4096)
	if len(got) != 1 || got[0] != "halo dunia" {
		t.Fatalf("teks pendek harus utuh, dapat %#v", got)
	}
}

func TestSplitTextEmpty(t *testing.T) {
	got := splitText("   ", 4096)
	if len(got) != 1 || got[0] != "(kosong)" {
		t.Fatalf("teks kosong harus jadi placeholder, dapat %#v", got)
	}
}

func TestSplitTextPrefersNewline(t *testing.T) {
	// limit 10: harus memotong pada '\n', bukan di tengah kata.
	text := "satu dua\ntiga empat lima"
	got := splitText(text, 10)
	if got[0] != "satu dua" {
		t.Fatalf("potongan pertama harus berhenti di newline, dapat %q", got[0])
	}
	for _, c := range got {
		if len([]rune(c)) > 10 {
			t.Fatalf("potongan melebihi limit: %q", c)
		}
	}
}

func TestSplitTextNewlineBoundariesRoundtrip(t *testing.T) {
	// Saat semua pemotongan jatuh di newline, join dengan '\n' memulihkan asli.
	text := "aaa\nbbb\nccc"
	got := splitText(text, 5)
	if strings.Join(got, "\n") != text {
		t.Fatalf("rekonstruksi gagal: %#v", got)
	}
}

func TestSplitTextHardCut(t *testing.T) {
	// Tanpa newline & melebihi limit: dipotong keras, tiap potongan <= limit.
	text := strings.Repeat("a", 25)
	got := splitText(text, 10)
	if len(got) != 3 {
		t.Fatalf("harus 3 potongan, dapat %d", len(got))
	}
	for _, c := range got {
		if len([]rune(c)) > 10 {
			t.Fatalf("potongan melebihi limit: %q", c)
		}
	}
	if strings.Join(got, "") != text {
		t.Fatalf("rekonstruksi gagal")
	}
}

func TestSplitTextUTF8Safe(t *testing.T) {
	// Karakter multibyte tidak boleh terpotong di tengah byte.
	text := strings.Repeat("é", 15) // tiap 'é' = 2 byte
	got := splitText(text, 10)
	for _, c := range got {
		if !isValidUTF8(c) {
			t.Fatalf("potongan berisi UTF-8 rusak: %q", c)
		}
	}
	if strings.Join(got, "") != text {
		t.Fatalf("rekonstruksi gagal")
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestIsImageExt(t *testing.T) {
	cases := map[string]bool{
		"a.png": true, "b.JPG": true, "c.jpeg": true,
		"d.gif": true, "e.webp": true, "f.txt": false, "g": false,
	}
	for path, want := range cases {
		if got := isImageExt(path); got != want {
			t.Errorf("isImageExt(%q)=%v, mau %v", path, got, want)
		}
	}
}

func TestNonEmpty(t *testing.T) {
	if nonEmpty("  ") != "…" {
		t.Errorf("string kosong harus jadi placeholder")
	}
	if nonEmpty("x") != "x" {
		t.Errorf("string berisi harus tetap")
	}
}
