package botapi

import "strings"

const mentionPlaceholderCore = "{mention:"

// lineBoundaryWindow is the share of the limit within which a trailing newline
// is preferred over a hard cut.
const lineBoundaryWindow = 10

// placeholderSpan is the half-open rune range [start, end) of one mention
// placeholder in the full message, sigils included.
type placeholderSpan struct {
	start int
	end   int
}

func TruncateMessage(msg, suffix string, limit int) (string, bool) {
	if limit <= 0 {
		return msg, false
	}
	runes := []rune(msg)
	if len(runes) <= limit {
		return msg, false
	}

	suffixRunes := []rune(suffix)
	if len(suffixRunes) >= limit {
		suffixRunes = nil
	}
	cut := limit - len(suffixRunes)

	if nl := lastLineBreak(runes, cut, limit/lineBoundaryWindow); nl >= 0 {
		cut = nl
	}
	cut = pullOutOfPlaceholder(placeholderSpans(runes), cut)

	return string(runes[:cut]) + string(suffixRunes), true
}

// lastLineBreak returns the index just past the last newline in runes[:cut]
// when that newline lies within window runes of cut, or -1.
func lastLineBreak(runes []rune, cut, window int) int {
	if window <= 0 {
		return -1
	}
	low := cut - window
	if low < 0 {
		low = 0
	}
	for i := cut - 1; i >= low; i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}
	return -1
}

// placeholderSpans locates every complete mention placeholder in the whole
// message. eXpress spells them @{mention:id}, @@{mention:id} and
// ##{mention:id}, and the leading sigils belong to the span: a cut that keeps
// only "@@" renders as literal text just as a cut through the body does.
//
// The spans are taken from the full text on purpose. Looking only at the part
// before the cut cannot see a placeholder the cut lands in the middle of, which
// is exactly the case that has to be caught.
func placeholderSpans(runes []rune) []placeholderSpan {
	var spans []placeholderSpan
	for i := 0; i < len(runes); {
		rel := strings.Index(string(runes[i:]), mentionPlaceholderCore)
		if rel < 0 {
			break
		}
		core := i + len([]rune(string(runes[i:])[:rel]))

		closing := -1
		for j := core; j < len(runes); j++ {
			if runes[j] == '}' {
				closing = j
				break
			}
		}
		if closing < 0 {
			break
		}

		start := core
		for start > 0 && (runes[start-1] == '@' || runes[start-1] == '#') {
			start--
		}
		spans = append(spans, placeholderSpan{start: start, end: closing + 1})
		i = closing + 1
	}
	return spans
}

// pullOutOfPlaceholder moves cut back to the start of the placeholder it falls
// inside, leaving a cut that lands on either side of one untouched.
func pullOutOfPlaceholder(spans []placeholderSpan, cut int) int {
	for _, sp := range spans {
		if cut > sp.start && cut < sp.end {
			return sp.start
		}
	}
	return cut
}
