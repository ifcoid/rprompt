package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectImageFiles(t *testing.T) {
	dir := t.TempDir()
	// Berkas yang benar-benar ada.
	mustWrite(t, filepath.Join(dir, "grafik.png"))
	mustWrite(t, filepath.Join(dir, "foto.JPG"))

	text := "Saya buat grafik.png dan foto.JPG. " +
		"Yang ini tidak ada: hilang.png. Bukan gambar: catatan.txt."

	got := detectImageFiles(text, dir)
	if len(got) != 2 {
		t.Fatalf("harus mendeteksi 2 gambar yang ada, dapat %d: %#v", len(got), got)
	}

	wantBases := map[string]bool{"grafik.png": true, "foto.JPG": true}
	for _, p := range got {
		if !wantBases[filepath.Base(p)] {
			t.Errorf("path tak terduga: %s", p)
		}
		if !filepath.IsAbs(p) {
			t.Errorf("path harus absolut: %s", p)
		}
	}
}

func TestDetectImageFilesDedup(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.png"))
	text := "a.png a.png a.png"
	if got := detectImageFiles(text, dir); len(got) != 1 {
		t.Fatalf("duplikat harus dihapus, dapat %d", len(got))
	}
}

func TestDetectImageFilesNone(t *testing.T) {
	dir := t.TempDir()
	if got := detectImageFiles("tidak ada gambar di sini", dir); len(got) != 0 {
		t.Fatalf("tidak boleh ada hasil, dapat %#v", got)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
