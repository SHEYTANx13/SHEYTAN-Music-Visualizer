package asciilog

// strict.go — the FINAL consolidated artifact contract (warning/error
// report only). The final artifact is stricter than captured step logs:
//
//   - printable ASCII 0x20..0x7E plus LF (0x0A) only;
//   - TAB is a violation (captured logs may keep tabs; the final
//     warning/error report must not);
//   - CR never survives (normalized to LF like Sanitize);
//   - no BOM, no ANSI escapes, no non-ASCII bytes, no "===" or "---"
//     runs, no other control bytes.
//
// SanitizeStrict transforms a buffer into this contract; ValidateStrict
// reports exact violations and is the fail-closed gate before the
// consolidated report is written or uploaded.

// SanitizeStrict applies the strict final-artifact contract to in.
// Rules (ordered): BOM strip; ANSI escape removal (CSI/OSC/DCS/SOS/PM/
// APC and stray ESC); CR and CRLF normalize to LF; TAB becomes a
// single space; bytes >= 0x80 become '?'; C0 controls other than
// LF (and the already-converted TAB) and DEL become '?'; runs of '='
// or '-' longer than two clamp to two.
func SanitizeStrict(in []byte) []byte {
	out := make([]byte, 0, len(in))
	i := 0
	if len(in) >= 3 && in[0] == 0xEF && in[1] == 0xBB && in[2] == 0xBF {
		i = 3
	}
	for i < len(in) {
		c := in[i]
		if c == 0x1B {
			i = skipEscape(in, i)
			continue
		}
		if c == '\r' {
			out = append(out, '\n')
			i++
			if i < len(in) && in[i] == '\n' {
				i++
			}
			continue
		}
		if c == '\t' {
			out = append(out, ' ')
			i++
			continue
		}
		if c >= 0x80 {
			out = append(out, '?')
			i++
			continue
		}
		if (c < 0x20 && c != '\n') || c == 0x7F {
			out = append(out, '?')
			i++
			continue
		}
		out = append(out, c)
		i++
	}
	return clampRuns(out)
}

// ValidateStrict scans b under the strict final-artifact contract and
// returns exact byte-level violation counts. TAB and CR violations are
// counted separately in Tabs and CarriageReturns AND included in
// Controls, so Violations() treats them as violations.
func ValidateStrict(b []byte) Report {
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
		case c == '\t':
			rep.Tabs++
			rep.Controls++
		case c == '\r':
			rep.CarriageReturns++
			rep.Controls++
		case c < 0x20 && c != '\n', c == 0x7F:
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
