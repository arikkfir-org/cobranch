package graphql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"cobranch.dev/internal/services/agents"
)

// defaultTranscriptPage is how many entries a read holds unless it asks for another number.
const defaultTranscriptPage = 100

// transcriptEntry is e as GraphQL shows it, its body decoded with its numbers as the harness wrote them.
func transcriptEntry(e agents.Entry) (*TranscriptEntry, error) {
	d := json.NewDecoder(bytes.NewReader(e.Body))
	d.UseNumber()
	var body map[string]any
	if err := d.Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding entry %d: %w", e.Seq, err)
	}
	return &TranscriptEntry{Seq: e.Seq, Kind: TranscriptEntryKind(strings.ToUpper(string(e.Kind))), Body: body, CreatedAt: e.CreatedAt.UTC()}, nil
}

// entryInput is in as the domain takes it.
func entryInput(in TranscriptEntryInput) (agents.Entry, error) {
	body, err := json.Marshal(in.Body)
	if err != nil {
		return agents.Entry{}, fmt.Errorf("encoding entry %d: %w", in.Seq, err)
	}
	return agents.Entry{Seq: in.Seq, Kind: agents.EntryKind(strings.ToLower(string(in.Kind))), Body: body}, nil
}
