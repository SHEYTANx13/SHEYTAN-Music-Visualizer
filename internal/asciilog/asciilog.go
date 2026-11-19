// Package asciilog is the canonical implementation of the SHEYTAN
// build-and-runtime log ASCII contract (run 44330231458 remediation).
//
// Contract: captured logs and the consolidated run report contain
// English ASCII characters only. Byte-level rules, applied in order:
//
//  1. UTF-8 byte-order marks are removed.
//  2. ANSI escape sequences (CSI, OSC, and two-character escapes) are
//     removed; a stray ESC byte becomes '?' so no escape byte survives.
//  3. CR and CRLF are normalized to LF.
//  4. Every byte at or above 0x80 becomes '?' (multi-byte UTF-8 text
//     therefore degrades to one '?' per byte: lossy by contract, not
//     by accident).
//  5. C0 control bytes other than LF and TAB, and DEL, become '?'.
//  6. Runs of '=' or '-' longer than two are clamped to exactly two,
//     so the three-byte sequences "===" and "---" can never appear
//     (including inside tool output such as go test -v markers).
//
// Validate scans bytes and reports exact violation counts; the
// collector and the standalone civalidate gate use it to fail closed
// when a produced log still violates the contract.
package asciilog

// Sanitize applies the full contract to in and returns the compliant
// byte slice. Deterministic and allocation-bounded by input length.
func Sanitize(in []byte) []byte {
	out := make([]byte, 0, len(in))
	i := 0
	// Rule 1: strip a leading UTF-8 BOM.
	if len(in) >= 3 && in[0] == 0xEF && in[1] == 0xBB && in[2] == 0xBF {
		i = 3
	}
	for i < len(in) {
		c := in[i]
		if c == 0x1B { // Rule 2: ANSI escape sequences.
			i = skipEscape(in, i)
			continue
		}
		if c == '\r' { // Rule 3: CR and CRLF normalize to LF.
			out = append(out, '\n')
			i++
			if i < len(in) && in[i] == '\n' {
				i++
			}
			continue
		}
		if c >= 0x80 { // Rule 4: non-ASCII degrades to '?'.
			out = append(out, '?')
			i++
			continue
		}
		if c < 0x20 && c != '\n' && c != '\t' || c == 0x7F { // Rule 5.
			out = append(out, '?')
			i++
			continue
		}
		out = append(out, c)
		i++
	}
	return clampRuns(out)
}

// skipEscape consumes one ANSI escape sequence starting at in[i] ==
// ESC and returns the index of the first byte after it. Malformed
// sequences are consumed greedily so no ESC-prefixed bytes survive.
func skipEscape(in []byte, i int) int {
	if i+1 >= len(in) {
		return i + 1 // stray ESC at end of stream
	}
	switch in[i+1] {
	case '[': // CSI: parameter bytes, intermediate bytes, final byte.
		j := i + 2
		for j < len(in) && in[j] >= 0x20 && in[j] <= 0x3F {
			j++
		}
		if j < len(in) && in[j] >= 0x40 && in[j] <= 0x7E {
			return j + 1
		}
		return j // malformed: drop what was scanned
	case ']': // OSC terminated by BEL or ESC \.
		j := i + 2
		for j < len(in) {
			if in[j] == 0x07 {
				return j + 1
			}
			if in[j] == 0x1B && j+1 < len(in) && in[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j // unterminated OSC: consume to end
	case 'P', 'X', '^', '_': // DCS/SOS/PM/APC terminated by ST (ESC \).
		j := i + 2
		for j < len(in) {
			if in[j] == 0x1B && j+1 < len(in) && in[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	default: // Two-character escape (ESC F) or stray ESC.
		if in[i+1] >= 0x40 && in[i+1] <= 0x5F {
			return i + 2
		}
		return i + 1
	}
}

// clampRuns applies rule 6: runs of '=' or '-' longer than two clamp
// to exactly two.
func clampRuns(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c == '=' || c == '-' {
			j := i
			for j < len(b) && b[j] == c {
				j++
			}
			n := j - i
			if n >= 3 {
				n = 2
			}
			for k := 0; k < n; k++ {
				out = append(out, c)
			}
			i = j
			continue
		}
		out = append(out, c)
		i++
	}
	return out
}

// Report is the byte-level violation count of one scanned buffer. The
// counts use the same metrics as the run 44330231458 evidence scan:
// raw byte occurrences, not decoded runes.
type Report struct {
	Bytes      int64 // total bytes scanned
	NonASCII   int64 // bytes at or above 0x80
	ANSIEscape int64 // ESC (0x1B) bytes (escape sequences or strays)
	BOM        int64 // UTF-8 BOM occurrences
	SeqEquals  int64 // occurrences of the 3-byte sequence "==="
	SeqDash    int64 // occurrences of the 3-byte sequence "---"
	Controls   int64 // C0 controls other than LF/TAB, plus DEL
}

// Violations is the number of contract classes violated (zero means
// the buffer fully complies).
func (r Report) Violations() int {
	n := 0
	for _, v := range []int64{r.NonASCII, r.ANSIEscape, r.BOM, r.SeqEquals, r.SeqDash, r.Controls} {
		if v > 0 {
			n++
		}
	}
	return n
}

// Validate scans b and returns the exact violation counts. Validate
// never mutates b and never loses bytes.
func Validate(b []byte) Report {
	var rep Report
	rep.Bytes = int64(len(b))
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		rep.BOM++
	}
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == 0x1B:
			rep.ANSIEscape++
		case c >= 0x80:
			rep.NonASCII++
		case c < 0x20 && c != '\n' && c != '\t', c == 0x7F:
			rep.Controls++
		}
	}
	for _, s := range []struct {
		seq   []byte
		count *int64
	}{
		{[]byte("==="), &rep.SeqEquals},
		{[]byte("---"), &rep.SeqDash},
	} {
		for i := 0; i+len(s.seq) <= len(b); i++ {
			if string(b[i:i+len(s.seq)]) == string(s.seq) {
				*s.count++
			}
		}
	}
	return rep
}
