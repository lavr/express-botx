package botapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFilterOrphanMentions(t *testing.T) {
	const idA = "aaaaaaaa-1111-2222-3333-444444444444"
	const idB = "bbbbbbbb-1111-2222-3333-444444444444"

	tests := []struct {
		name     string
		original string
		body     string
		mentions string
		wantIDs  []string
		wantGone []string
		wantErr  bool
	}{
		{
			name:     "keeps entries whose placeholder survived",
			original: "hi @{mention:" + idA + "}",
			body:     "hi @{mention:" + idA + "}",
			mentions: `[{"mention_type":"user","mention_id":"` + idA + `","mention_data":{"user_huid":"u1"}}]`,
			wantIDs:  []string{idA},
		},
		{
			name:     "drops an entry whose placeholder is gone",
			original: "hi @{mention:" + idA + "} @{mention:" + idB + "}",
			body:     "hi @{mention:" + idA + "}",
			mentions: `[{"mention_id":"` + idA + `"},{"mention_id":"` + idB + `"}]`,
			wantIDs:  []string{idA},
			wantGone: []string{idB},
		},
		{
			name:     "an entry that never had a placeholder is kept",
			original: "plain text with no placeholders",
			body:     "plain text",
			mentions: `[{"mention_id":"` + idA + `"}]`,
			wantIDs:  []string{idA},
		},
		{
			name:     "keeps an id that still occurs later in the body",
			original: "@{mention:" + idA + "} and @{mention:" + idA + "}",
			body:     "@{mention:" + idA + "} and @{mention:" + idA + "}",
			mentions: `[{"mention_id":"` + idA + `"}]`,
			wantIDs:  []string{idA},
		},
		{
			name:     "recognises the contact and chat forms",
			original: "@@{mention:" + idA + "} ##{mention:" + idB + "}",
			body:     "@@{mention:" + idA + "} ##{mention:" + idB + "}",
			mentions: `[{"mention_id":"` + idA + `"},{"mention_id":"` + idB + `"}]`,
			wantIDs:  []string{idA, idB},
		},
		{
			name:     "an entry without mention_id is kept untouched",
			original: "no placeholders",
			body:     "no placeholders",
			mentions: `[{"mention_type":"all"}]`,
			wantIDs:  []string{"mention_type"},
		},
		{
			name:     "unparsable mentions are reported, not silently emptied",
			original: "hi",
			body:     "hi",
			mentions: `{"not":"an array"}`,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filterOrphanMentions(json.RawMessage(tt.mentions), tt.original, tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error for unparsable mentions")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, id := range tt.wantIDs {
				if !strings.Contains(string(got), id) {
					t.Errorf("entry for %s was dropped: %s", id, got)
				}
			}
			for _, id := range tt.wantGone {
				if strings.Contains(string(got), id) {
					t.Errorf("orphan entry survived: %s", got)
				}
			}
		})
	}
}

func TestFilterOrphanMentions_PreservesUnknownFieldsAndOrder(t *testing.T) {
	const idA = "aaaaaaaa-1111-2222-3333-444444444444"
	const idB = "bbbbbbbb-1111-2222-3333-444444444444"
	const idC = "cccccccc-1111-2222-3333-444444444444"
	original := "@{mention:" + idB + "} @{mention:" + idA + "} @{mention:" + idC + "}"
	body := "@{mention:" + idB + "} @{mention:" + idA + "}"
	raw := `[{"mention_id":"` + idB + `","future_field":42},{"mention_id":"` + idA + `","mention_data":{"user_huid":"u1","name":"Иванов"}},{"mention_id":"` + idC + `"}]`

	got, err := filterOrphanMentions(json.RawMessage(raw), original, body)
	if strings.Contains(string(got), idC) {
		t.Errorf("the dropped entry survived, so the rebuild branch was not taken: %s", got)
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(got), "future_field") {
		t.Errorf("unknown field was dropped: %s", got)
	}
	if !strings.Contains(string(got), "Иванов") {
		t.Errorf("mention_data was not preserved verbatim: %s", got)
	}
	if strings.Index(string(got), idB) > strings.Index(string(got), idA) {
		t.Errorf("entry order changed: %s", got)
	}
}
