package mentions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// MaxParsedMentions is the maximum number of inline tokens the parser will
// process in a single message. Tokens beyond this limit are left as literal
// text and a limit error is recorded.
const MaxParsedMentions = 1000

// maxParsedMentions is the effective limit used at runtime.
// Tests may override this; production code uses MaxParsedMentions.
var maxParsedMentions = MaxParsedMentions

// tokenPrefix is the opening sequence the scanner looks for.
const tokenPrefix = "@mention["

// UserResolver looks up a user by email and returns their info.
// In production this is backed by botapi.Client.GetUserByEmail.
type UserResolver interface {
	GetUserByEmail(ctx context.Context, email string) (huid string, name string, err error)
}

// ParseError describes a single parser or lookup failure.
type ParseError struct {
	Kind     string `json:"kind"`               // "parse", "lookup", "limit"
	Token    string `json:"token"`              // original token text
	Resolver string `json:"resolver,omitempty"` // "email", "huid", "all" if known
	Value    string `json:"value,omitempty"`    // resolver value if known
	Cause    string `json:"cause"`              // human-readable reason
}

// parsedToken represents a successfully parsed inline mention token.
type parsedToken struct {
	resolver    string // "email", "huid", "all"
	value       string // email address or UUID; empty for "all"
	displayName string // URL-decoded display name; may be empty
}

// mentionEntry represents a single BotX wire-format mention.
type mentionEntry struct {
	MentionID   string       `json:"mention_id"`
	MentionType string       `json:"mention_type"`
	MentionData *mentionData `json:"mention_data"`
}

// mentionData holds user-specific mention data.
type mentionData struct {
	UserHUID string `json:"user_huid"`
	Name     string `json:"name,omitempty"`
}

// newMentionID generates a unique mention identifier.
// Replaced in tests for deterministic output.
var newMentionID = generateUUIDv4

func generateUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// Result holds the output of Parse.
type Result struct {
	Message  string          // normalized message text
	Mentions json.RawMessage // merged mentions array (BotX wire-format)
	Errors   []ParseError    // accumulated parse/lookup/limit errors
}

// Parse scans the message for @mention[...] tokens and normalizes them into
// BotX wire-format. Raw mentions (already in BotX format) are preserved and
// parsed mentions are appended after them.
//
// If parseEnabled is false, the message is returned unchanged and rawMentions
// are passed through as-is.
func Parse(ctx context.Context, message string, rawMentions json.RawMessage, parseEnabled bool, resolver UserResolver) *Result {
	if !parseEnabled {
		return &Result{
			Message:  message,
			Mentions: rawMentions,
		}
	}

	tokens := scan(message)
	if len(tokens) == 0 {
		return &Result{
			Message:  message,
			Mentions: rawMentions,
		}
	}

	var errs []ParseError
	var parsed []mentionEntry
	var buf strings.Builder
	cursor := 0
	processed := 0
	limitHit := false

	for _, tok := range tokens {
		raw := tok.raw(message)
		buf.WriteString(message[cursor:tok.start])

		// If the limit has been reached, leave remaining tokens as literal text.
		if limitHit {
			buf.WriteString(raw)
			cursor = tok.end
			continue
		}

		if tok.unclosed {
			errs = append(errs, ParseError{
				Kind:  "parse",
				Token: raw,
				Cause: "unclosed token",
			})
			buf.WriteString(raw)
			cursor = tok.end
			continue
		}

		body := tok.body(message)
		pt, err := parseTokenBody(body)
		if err != nil {
			errs = append(errs, ParseError{
				Kind:  "parse",
				Token: raw,
				Cause: err.Error(),
			})
			buf.WriteString(raw)
			cursor = tok.end
			continue
		}

		// Count every well-formed token toward the limit — both successful
		// and failed normalizations involve work (including potential network
		// lookups for email), so they all consume the budget.
		processed++
		if processed > maxParsedMentions {
			limitHit = true
			errs = append(errs, ParseError{
				Kind:  "limit",
				Token: "",
				Cause: fmt.Sprintf("reached maximum of %d parsed mentions", maxParsedMentions),
			})
			buf.WriteString(raw)
			cursor = tok.end
			continue
		}

		entry, perr := normalize(ctx, pt, raw, resolver)
		if perr != nil {
			errs = append(errs, *perr)
			buf.WriteString(raw)
		} else {
			parsed = append(parsed, *entry)
			fmt.Fprintf(&buf, "@{mention:%s}", entry.MentionID)
		}
		cursor = tok.end
	}

	buf.WriteString(message[cursor:])

	return &Result{
		Message:  buf.String(),
		Mentions: mergeMentions(rawMentions, parsed),
		Errors:   errs,
	}
}

// normalize resolves a parsed token into a BotX mention entry.
// Returns (entry, nil) on success or (nil, error) on lookup failure.
func normalize(ctx context.Context, pt *parsedToken, raw string, resolver UserResolver) (*mentionEntry, *ParseError) {
	switch pt.resolver {
	case "email":
		return normalizeEmail(ctx, pt, raw, resolver)
	case "huid":
		return normalizeHuid(pt)
	case "all":
		return normalizeAll()
	default:
		return nil, &ParseError{Kind: "parse", Token: raw, Cause: fmt.Sprintf("unknown resolver %q", pt.resolver)}
	}
}

// normalizeEmail resolves an email token by looking up the user.
func normalizeEmail(ctx context.Context, pt *parsedToken, raw string, resolver UserResolver) (*mentionEntry, *ParseError) {
	if resolver == nil {
		return nil, &ParseError{
			Kind: "lookup", Token: raw, Resolver: "email",
			Value: pt.value, Cause: "no resolver available",
		}
	}

	huid, name, err := resolver.GetUserByEmail(ctx, pt.value)
	if err != nil {
		return nil, &ParseError{
			Kind: "lookup", Token: raw, Resolver: "email",
			Value: pt.value, Cause: err.Error(),
		}
	}

	displayName := pt.displayName
	if displayName == "" {
		displayName = name
	}

	return &mentionEntry{
		MentionID:   newMentionID(),
		MentionType: "user",
		MentionData: &mentionData{
			UserHUID: huid,
			Name:     displayName,
		},
	}, nil
}

// normalizeHuid creates a mention entry from an huid token. No lookup is
// needed — the huid is taken directly from the token. The name field is only
// set when a display name was explicitly provided.
func normalizeHuid(pt *parsedToken) (*mentionEntry, *ParseError) {
	md := &mentionData{UserHUID: pt.value}
	if pt.displayName != "" {
		md.Name = pt.displayName
	}
	return &mentionEntry{
		MentionID:   newMentionID(),
		MentionType: "user",
		MentionData: md,
	}, nil
}

// normalizeAll creates a mention entry for the "all" (broadcast) token.
// No lookup is needed. mention_data is nil for "all" mentions.
func normalizeAll() (*mentionEntry, *ParseError) {
	return &mentionEntry{
		MentionID:   newMentionID(),
		MentionType: "all",
		MentionData: nil,
	}, nil
}

// mergeMentions combines raw mentions (already in BotX wire-format) with
// parsed mention entries. Raw mentions come first, parsed are appended.
func mergeMentions(raw json.RawMessage, parsed []mentionEntry) json.RawMessage {
	if len(parsed) == 0 {
		return raw
	}

	var arr []json.RawMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &arr)
	}

	for i := range parsed {
		b, _ := json.Marshal(parsed[i])
		arr = append(arr, b)
	}

	result, _ := json.Marshal(arr)
	return result
}

// tokenSpan records the byte offsets of a single @mention[...] token found
// in the message.
type tokenSpan struct {
	start    int  // index of '@' in tokenPrefix
	end      int  // index one past ']' (or end of token text for unclosed)
	unclosed bool // true if the closing ']' was not found
}

// raw returns the original token text from the message.
func (t tokenSpan) raw(msg string) string {
	return msg[t.start:t.end]
}

// body returns the text between '[' and ']' in the original message.
// Must not be called on unclosed tokens.
func (t tokenSpan) body(msg string) string {
	return msg[t.start+len(tokenPrefix) : t.end-1]
}

// scan finds all @mention[...] tokens in msg.
// Tokens must have a matching closing ']'. Nested brackets are not supported.
// Unclosed tokens are ignored (they will be caught as parse errors later when
// the full grammar parser is wired in).
// Scanning stops after 2*maxParsedMentions tokens to bound memory usage.
func scan(msg string) []tokenSpan {
	var spans []tokenSpan
	scanLimit := 2 * maxParsedMentions
	i := 0
	for i < len(msg) {
		// Look for the prefix.
		idx := indexAt(msg, tokenPrefix, i)
		if idx < 0 {
			break
		}
		// Find the closing bracket. We don't allow newlines inside tokens.
		bodyStart := idx + len(tokenPrefix)
		closed := false
		j := bodyStart
		for j < len(msg) {
			if msg[j] == ']' {
				spans = append(spans, tokenSpan{start: idx, end: j + 1})
				i = j + 1
				closed = true
				break
			}
			if msg[j] == '\n' {
				break
			}
			j++
		}
		if !closed {
			// Record unclosed token for error reporting.
			spans = append(spans, tokenSpan{start: idx, end: j, unclosed: true})
			i = j
		}
		if len(spans) >= scanLimit {
			break
		}
	}
	return spans
}

// parseTokenBody parses the content between @mention[ and ].
// Supported forms:
//   - "all"
//   - "email:<address>"
//   - "email:<address>;<url-encoded display name>"
//   - "huid:<uuid>"
//   - "huid:<uuid>;<url-encoded display name>"
func parseTokenBody(body string) (*parsedToken, error) {
	if body == "all" {
		return &parsedToken{resolver: "all"}, nil
	}

	colonIdx := strings.Index(body, ":")
	if colonIdx < 0 {
		if strings.HasPrefix(body, "all;") {
			return nil, fmt.Errorf("'all' does not accept value or display name")
		}
		return nil, fmt.Errorf("unknown resolver %q", body)
	}

	resolver := body[:colonIdx]
	rest := body[colonIdx+1:]

	if resolver != "email" && resolver != "huid" {
		return nil, fmt.Errorf("unknown resolver %q", resolver)
	}

	var value, displayName string
	if semiIdx := strings.Index(rest, ";"); semiIdx >= 0 {
		value = rest[:semiIdx]
		raw := rest[semiIdx+1:]
		if strings.Contains(raw, ";") {
			return nil, fmt.Errorf("display name contains raw semicolon; use %%3B to encode")
		}
		decoded, err := url.QueryUnescape(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid URL encoding in display name: %v", err)
		}
		displayName = decoded
	} else {
		value = rest
	}

	if value == "" {
		return nil, fmt.Errorf("empty value for resolver %q", resolver)
	}

	return &parsedToken{
		resolver:    resolver,
		value:       value,
		displayName: displayName,
	}, nil
}

// indexAt returns the index of substr in s starting from offset, or -1.
func indexAt(s, substr string, offset int) int {
	if offset >= len(s) {
		return -1
	}
	if i := strings.Index(s[offset:], substr); i >= 0 {
		return offset + i
	}
	return -1
}
