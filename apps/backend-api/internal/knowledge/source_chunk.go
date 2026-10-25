package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const sourceChunkerVersion = 1

type sourceFragment struct {
	Heading, Content, Kind, Hash string
	Chars, Tokens                int
}

func sourceHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

func normalizeSource(text, format string) string {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	if format == "MARKDOWN" && len(lines) > 1 && lines[0] == "---" {
		for i := 1; i < len(lines); i++ {
			if lines[i] == "---" || lines[i] == "..." {
				lines = lines[i+1:]
				break
			}
		}
	}
	return norm.NFC.String(strings.Trim(strings.Join(lines, "\n"), "\n"))
}

func sourceHeading(text string) (int, string) {
	n := 0
	for n < len(text) && text[n] == '#' {
		n++
	}
	if n < 1 || n > 6 || len(text) <= n || text[n] != ' ' {
		return 0, ""
	}
	title := strings.TrimSpace(text[n:])
	withoutClosing := strings.TrimRight(title, "#")
	if len(withoutClosing) < len(title) && (withoutClosing == "" || strings.HasSuffix(withoutClosing, " ") || strings.HasSuffix(withoutClosing, "\t")) {
		title = strings.TrimSpace(withoutClosing)
	}
	return n, title
}

func sourceFence(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
		end := 0
		for end < len(line) && line[end] == line[0] {
			end++
		}
		return line[:end]
	}
	return ""
}

func setextLevel(line string) int {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0
	}
	if strings.Trim(line, "=") == "" {
		return 1
	}
	if strings.Trim(line, "-") == "" {
		return 2
	}
	return 0
}

// chunkSource is deterministic, bounds UTF-8 bytes as well as runes, repeats
// split table headers, and preserves code lines. Unsupported oversized code
// lines fail explicitly instead of silently destroying source text.
func chunkSource(text, format string) ([]sourceFragment, string) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') {
		return nil, "TEXT_NOT_EXTRACTABLE"
	}
	if utf8.RuneCountInString(text) > 2000000 {
		return nil, "TOO_LARGE"
	}
	if strings.TrimSpace(text) == "" {
		return nil, "TEXT_NOT_EXTRACTABLE"
	}
	nonspace, cjk := 0, 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			nonspace++
			if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
				cjk++
			}
		}
	}
	limit := 2000
	if cjk*10 >= nonspace*3 {
		limit = 800
	}
	out := []sourceFragment{}
	seen := map[string]bool{}
	add := func(heading, body, kind string) {
		if kind == "CODE" {
			body = strings.Trim(body, "\n")
		} else {
			body = strings.TrimSpace(body)
		}
		if strings.TrimSpace(body) == "" {
			return
		}
		hr := []rune(heading)
		if len(hr) > 300 {
			heading = "…" + string(hr[len(hr)-299:])
		}
		hash := sourceHash(heading + "\n" + body)
		if seen[hash] {
			return
		}
		seen[hash] = true
		out = append(out, sourceFragment{Heading: heading, Content: body, Kind: kind, Hash: hash, Chars: utf8.RuneCountInString(body), Tokens: (len(body) + 2) / 3})
	}
	fits := func(body string, code bool) bool {
		maxChars, maxBytes := limit, 1800
		if code {
			maxChars = limit * 3 / 2
			maxBytes = 2700
		}
		return utf8.RuneCountInString(body) <= maxChars && len(body) <= maxBytes
	}
	splitText := func(heading, body string) {
		runes := []rune(body)
		for len(runes) > 0 {
			n, bytes := 0, 0
			for n < len(runes) && n < limit && bytes+utf8.RuneLen(runes[n]) <= 1800 {
				bytes += utf8.RuneLen(runes[n])
				n++
			}
			if n < len(runes) {
				for j := n - 1; j >= 0 && j >= n-50; j-- {
					if unicode.IsSpace(runes[j]) || strings.ContainsRune("。！？；.!?;", runes[j]) {
						n = j + 1
						break
					}
				}
			}
			add(heading, string(runes[:n]), "TEXT")
			if n == len(runes) {
				break
			}
			// Only hard-cut paragraphs overlap; paragraph boundaries are packed below.
			overlap := min(120, n/3)
			runes = runes[n-overlap:]
		}
	}
	headings := []string{}
	heading := ""
	pending := ""
	flush := func() {
		if pending != "" {
			splitText(heading, pending)
			pending = ""
		}
	}
	lines := strings.Split(text, "\n")
	// Like a line scanner, a terminal newline terminates its preceding line;
	// it does not manufacture another empty content line.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i := 0; i < len(lines); {
		line := lines[i]
		if format == "MARKDOWN" {
			level, title := sourceHeading(line)
			consumed := 1
			if level == 0 && strings.TrimSpace(line) != "" && i+1 < len(lines) {
				if l := setextLevel(lines[i+1]); l > 0 {
					level, title, consumed = l, strings.TrimSpace(line), 2
				}
			}
			if level > 0 {
				flush()
				if len(headings) >= level {
					headings = headings[:level-1]
				}
				for len(headings) < level-1 {
					headings = append(headings, "")
				}
				headings = append(headings, title)
				path := []string{}
				for _, h := range headings {
					if h != "" {
						path = append(path, h)
					}
				}
				heading = strings.Join(path, " › ")
				i += consumed
				continue
			}
		}
		fence := sourceFence(line)
		if format == "CODE" || (format == "MARKDOWN" && fence != "") {
			flush()
			open, closingFence := "", ""
			code := []string{}
			if format == "CODE" {
				code = lines[i:]
				i = len(lines)
			} else {
				open = line
				closingFence = fence
				i++
				for i < len(lines) {
					f := sourceFence(lines[i])
					if f != "" && f[0] == fence[0] && len(f) >= len(fence) && strings.TrimSpace(lines[i]) == f {
						i++
						break
					}
					code = append(code, lines[i])
					i++
				}
			}
			body := ""
			wrap := func(s string) string {
				if open == "" {
					return s
				}
				return open + "\n" + s + "\n" + closingFence
			}
			for _, cl := range code {
				if !fits(wrap(cl), true) {
					return nil, "CODE_LINE_TOO_LONG"
				}
				candidate := cl
				if body != "" {
					candidate = body + "\n" + cl
				}
				if !fits(wrap(candidate), true) {
					add(heading, wrap(body), "CODE")
					body = cl
				} else {
					body = candidate
				}
			}
			if body != "" {
				add(heading, wrap(body), "CODE")
			}
			continue
		}
		if format == "MARKDOWN" && strings.HasPrefix(strings.TrimSpace(line), "|") {
			flush()
			table := []string{}
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				table = append(table, lines[i])
				i++
			}
			if len(table) < 3 {
				splitText(heading, strings.Join(table, "\n"))
				continue
			}
			header := strings.Join(table[:2], "\n")
			if !fits(header, false) {
				return nil, "TABLE_ROW_TOO_LONG"
			}
			body := header
			for _, row := range table[2:] {
				if !fits(header+"\n"+row, false) {
					return nil, "TABLE_ROW_TOO_LONG"
				}
				if !fits(body+"\n"+row, false) {
					add(heading, body, "TEXT")
					body = header
				}
				body += "\n" + row
			}
			add(heading, body, "TEXT")
			continue
		}
		if strings.TrimSpace(line) == "" {
			if pending != "" && !strings.HasSuffix(pending, "\n\n") {
				pending += "\n\n"
			}
			i++
			continue
		}
		candidate := pending
		if candidate != "" && !strings.HasSuffix(candidate, "\n") {
			candidate += "\n"
		}
		candidate += line
		if !fits(candidate, false) && pending != "" {
			flush()
			pending = line
		} else {
			pending = candidate
		}
		i++
	}
	flush()
	if len(out) == 0 {
		return nil, "TEXT_NOT_EXTRACTABLE"
	}
	if len(out) > 2000 {
		return nil, "TOO_MANY_CHUNKS"
	}
	return out, ""
}
