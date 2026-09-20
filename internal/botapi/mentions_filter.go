package botapi

import (
	"encoding/json"
	"fmt"
	"strings"
)

// filterOrphanMentions drops the mention entries whose placeholder the
// truncation removed: an id that occurred in original but no longer occurs in
// body. An entry that had no placeholder to begin with is the caller's own
// business and is left alone, so truncation never changes what an explicit
// mentions array meant.
//
// Entries are carried as raw JSON so unknown fields, entry order and
// mention_data survive untouched; only mention_id is decoded.
//
// An error means the array could not be understood, never that it is empty.
// The caller keeps the original array in that case and still sends the cut
// body: an over-long body is refused by BotX outright, while an orphaned
// mention only might be.
func filterOrphanMentions(mentions json.RawMessage, original, body string) (json.RawMessage, error) {
	if len(mentions) == 0 {
		return mentions, nil
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(mentions, &entries); err != nil {
		return nil, fmt.Errorf("mentions is not a JSON array: %w", err)
	}

	kept := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		var probe struct {
			MentionID string `json:"mention_id"`
		}
		if err := json.Unmarshal(entry, &probe); err != nil {
			return nil, fmt.Errorf("mention entry is not a JSON object: %w", err)
		}
		if probe.MentionID != "" {
			placeholder := mentionPlaceholderCore + probe.MentionID + "}"
			if strings.Contains(original, placeholder) && !strings.Contains(body, placeholder) {
				continue
			}
		}
		kept = append(kept, entry)
	}

	if len(kept) == len(entries) {
		return mentions, nil
	}
	return json.Marshal(kept)
}
