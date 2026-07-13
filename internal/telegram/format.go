package telegram

import (
	"fmt"
	"regexp"
	"strings"
)

// mdToHTML mengonversi teks markdown (gaya CommonMark keluaran Claude) ke subset
// HTML yang didukung Telegram (parse_mode=HTML): <b> <i> <s> <code> <pre> <a>.
//
// Hasilnya SELALU seimbang (tag yang tak tertutup di-auto-tutup) sehingga aman
// dipakai pada potongan streaming yang di-edit bertahap: blok kode berpagar yang
// belum ditutup dibungkus <pre> lalu ditutup, dan penanda inline yang tak
// berpasangan (mis. '**' menggantung) dibiarkan sebagai teks literal.
//
// Elemen yang tidak punya padanan di Telegram dirender agar tetap terbaca:
// judul '#' menjadi tebal, butir '- ' menjadi '• ', dan tabel '|...|' dibungkus
// <pre> agar kolomnya lurus (monospace).
func mdToHTML(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")

	var b strings.Builder
	// Pisahkan blok kode berpagar dari teks biasa. Segmen dengan indeks ganjil
	// adalah isi blok kode (di antara sepasang ```); indeks genap adalah teks.
	segs := strings.Split(s, "```")
	for i, seg := range segs {
		if i%2 == 1 {
			b.WriteString("<pre>")
			b.WriteString(escapeHTML(stripCodeFenceLang(seg)))
			b.WriteString("</pre>")
			continue
		}
		b.WriteString(renderText(seg))
	}
	return b.String()
}

// stripCodeFenceLang membuang baris bahasa opsional di awal blok kode
// (mis. "python\n...") dan newline pembungkus di ujung.
func stripCodeFenceLang(seg string) string {
	seg = strings.TrimPrefix(seg, "\n")
	if i := strings.IndexByte(seg, '\n'); i >= 0 {
		first := seg[:i]
		if first != "" && !strings.ContainsAny(first, " \t") {
			seg = seg[i+1:] // baris pertama adalah token bahasa; buang
		}
	}
	return strings.Trim(seg, "\n")
}

// renderText memproses teks non-kode baris per baris: judul, butir, tabel, lalu
// format inline.
func renderText(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	var table []string

	flushTable := func() {
		if len(table) == 0 {
			return
		}
		out = append(out, "<pre>"+escapeHTML(strings.Join(table, "\n"))+"</pre>")
		table = table[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isTableRow(trimmed) {
			if !isTableSeparator(trimmed) {
				table = append(table, trimmed)
			}
			continue
		}
		flushTable()
		out = append(out, renderLine(line))
	}
	flushTable()
	return strings.Join(out, "\n")
}

var (
	headingRe = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*)$`)
	bulletRe  = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	codeRe    = regexp.MustCompile("`([^`]+)`")
	linkRe    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	boldRe    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	strikeRe  = regexp.MustCompile(`~~(.+?)~~`)
	italicRe  = regexp.MustCompile(`\*([^*\n]+?)\*`)
)

// renderLine memformat satu baris teks (bukan tabel, bukan blok kode).
func renderLine(line string) string {
	if m := headingRe.FindStringSubmatch(line); m != nil {
		return "<b>" + inlineHTML(m[1]) + "</b>"
	}
	if m := bulletRe.FindStringSubmatch(line); m != nil {
		return m[1] + "• " + inlineHTML(m[2])
	}
	return inlineHTML(line)
}

// inlineHTML menerapkan format inline pada satu baris. Isi kode inline diambil
// lebih dulu agar penanda di dalamnya tidak ikut diformat.
func inlineHTML(s string) string {
	var codes []string
	s = codeRe.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, m[1:len(m)-1])
		return fmt.Sprintf("\x00c%d\x00", len(codes)-1)
	})

	s = escapeHTML(s)

	s = linkRe.ReplaceAllStringFunc(s, func(m string) string {
		g := linkRe.FindStringSubmatch(m)
		href := strings.ReplaceAll(g[2], `"`, "&quot;")
		return `<a href="` + href + `">` + g[1] + `</a>`
	})
	s = boldRe.ReplaceAllString(s, "<b>$1</b>")
	s = strikeRe.ReplaceAllString(s, "<s>$1</s>")
	s = italicRe.ReplaceAllString(s, "<i>$1</i>")

	for i, c := range codes {
		s = strings.Replace(s, fmt.Sprintf("\x00c%d\x00", i), "<code>"+escapeHTML(c)+"</code>", 1)
	}
	return s
}

// escapeHTML meng-escape karakter khusus HTML. '&' lebih dulu agar tidak
// meng-escape ganda hasil substitusi berikutnya.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func isTableRow(trimmed string) bool {
	return strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2
}

// isTableSeparator mengenali baris pemisah tabel seperti "|---|:--:|".
func isTableSeparator(trimmed string) bool {
	if !isTableRow(trimmed) {
		return false
	}
	return strings.Trim(trimmed, "|:- \t") == ""
}
