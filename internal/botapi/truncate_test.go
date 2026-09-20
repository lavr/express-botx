package botapi

import (
	"regexp"
	"strings"
	"testing"
)

func TestTruncateMessage(t *testing.T) {
	const suffix = "…"

	tests := []struct {
		name    string
		msg     string
		limit   int
		suffix  string
		want    string
		wantCut bool
	}{
		{
			name:    "under the limit is untouched",
			msg:     "short",
			limit:   10,
			suffix:  suffix,
			want:    "short",
			wantCut: false,
		},
		{
			name:    "exactly at the limit is untouched",
			msg:     "abcde",
			limit:   5,
			suffix:  suffix,
			want:    "abcde",
			wantCut: false,
		},
		{
			name:    "zero limit disables truncation",
			msg:     strings.Repeat("x", 100),
			limit:   0,
			suffix:  suffix,
			want:    strings.Repeat("x", 100),
			wantCut: false,
		},
		{
			name:    "suffix counts inside the limit",
			msg:     "abcdefghij",
			limit:   5,
			suffix:  "...",
			want:    "ab...",
			wantCut: true,
		},
		{
			name:    "empty suffix still cuts to the limit",
			msg:     "abcdefghij",
			limit:   4,
			suffix:  "",
			want:    "abcd",
			wantCut: true,
		},
		{
			name:    "counts runes not bytes",
			msg:     strings.Repeat("я", 10),
			limit:   5,
			suffix:  "",
			want:    strings.Repeat("я", 5),
			wantCut: true,
		},
		{
			name:    "prefers a line boundary inside the window",
			msg:     strings.Repeat("a", 95) + "\n" + strings.Repeat("x", 40),
			limit:   100,
			suffix:  "",
			want:    strings.Repeat("a", 95) + "\n",
			wantCut: true,
		},
		{
			name:    "a distant line boundary is not used",
			msg:     strings.Repeat("a", 50) + "\n" + strings.Repeat("x", 100),
			limit:   100,
			suffix:  "",
			want:    strings.Repeat("a", 50) + "\n" + strings.Repeat("x", 49),
			wantCut: true,
		},
		{
			name:    "falls back to a hard cut when no line boundary is near",
			msg:     "head\n" + strings.Repeat("x", 100),
			limit:   20,
			suffix:  "",
			want:    "head\n" + strings.Repeat("x", 15),
			wantCut: true,
		},
		{
			name:    "never splits a mention placeholder",
			msg:     "hi @{mention:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee} tail",
			limit:   20,
			suffix:  "",
			want:    "hi ",
			wantCut: true,
		},
		{
			name:    "a suffix longer than the limit is dropped",
			msg:     "abcdefghij",
			limit:   3,
			suffix:  "[truncated]",
			want:    "abc",
			wantCut: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cut := TruncateMessage(tt.msg, tt.suffix, tt.limit)
			if cut != tt.wantCut {
				t.Fatalf("truncated = %v, want %v (got %q)", cut, tt.wantCut, got)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
			if tt.wantCut && len([]rune(got)) > tt.limit {
				t.Errorf("result is %d runes, over the limit of %d", len([]rune(got)), tt.limit)
			}
		})
	}
}

// placeholderSpansForTest locates every mention placeholder independently of
// the implementation, so the assertion below does not inherit its bugs.
func placeholderSpansForTest(t *testing.T, msg string) [][2]int {
	t.Helper()
	re := regexp.MustCompile(`(@@|##|@)\{mention:[^}]*\}`)
	var spans [][2]int
	for _, loc := range re.FindAllStringIndex(msg, -1) {
		spans = append(spans, [2]int{
			len([]rune(msg[:loc[0]])),
			len([]rune(msg[:loc[1]])),
		})
	}
	return spans
}

// eXpress renders three placeholder forms: @{mention:id} for a user or all,
// @@{mention:id} for a contact and ##{mention:id} for a chat or channel. For
// every possible cut position the result must end either before a placeholder
// starts or after it ends, never inside it.
func TestTruncateMessage_NeverCutsInsideAPlaceholder(t *testing.T) {
	const idA = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	const idB = "11111111-2222-3333-4444-555555555555"

	messages := []string{
		"hi @{mention:" + idA + "} tail",
		"hi @@{mention:" + idA + "} tail",
		"hi ##{mention:" + idA + "} tail",
		"x@{mention:" + idA + "}@@{mention:" + idB + "}y",
		"##{mention:" + idA + "} leads",
	}

	for _, msg := range messages {
		t.Run(msg[:12], func(t *testing.T) {
			spans := placeholderSpansForTest(t, msg)
			total := len([]rune(msg))
			for limit := 1; limit <= total; limit++ {
				got, _ := TruncateMessage(msg, "", limit)
				n := len([]rune(got))
				for _, sp := range spans {
					if n > sp[0] && n < sp[1] {
						t.Fatalf("limit %d cut inside placeholder %v: %q", limit, sp, got)
					}
				}
			}
		})
	}
}

// Text that merely contains @ or # is not a placeholder and must survive.
func TestTruncateMessage_LeavesPlainSigilsAlone(t *testing.T) {
	tests := []struct {
		msg   string
		limit int
		want  string
	}{
		{"abc@xyz", 4, "abc@"},
		{"tag #release now", 6, "tag #r"},
		{"a@@b", 3, "a@@"},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got, _ := TruncateMessage(tt.msg, "", tt.limit)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
