package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// imagePathRe mencocokkan token mirip path berkas gambar (tanpa spasi).
// Mencakup path gaya Unix maupun Windows.
var imagePathRe = regexp.MustCompile(`(?i)[\w./\\:-]+\.(?:png|jpe?g|gif|webp|bmp)`)

// maxAutoImages membatasi jumlah gambar yang dikirim otomatis per balasan.
const maxAutoImages = 5

// detectImageFiles mencari path berkas gambar yang disebut di teks dan benar-
// benar ada di disk (relatif terhadap workDir atau absolut). Hasil di-dedupe
// berdasarkan path absolut dan dibatasi maxAutoImages.
func detectImageFiles(text, workDir string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range imagePathRe.FindAllString(text, -1) {
		cand := strings.Trim(m, `"'()[]<>`)
		if !filepath.IsAbs(cand) {
			cand = filepath.Join(workDir, cand)
		}
		abs, err := filepath.Abs(cand)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
		if len(out) >= maxAutoImages {
			break
		}
	}
	return out
}
