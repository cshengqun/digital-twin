package server

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"digital-twin/internal/rag"
	"digital-twin/internal/store"
)

var citationMarker = regexp.MustCompile(`\[([1-9][0-9]{0,2})\]`)

func citationContext(items []rag.RetrievalItem, corrections []store.MemoryRecord) (string, []store.Citation) {
	if len(items) == 0 && len(corrections) == 0 {
		return "", nil
	}
	var data []store.Citation
	seen := make(map[int64]bool)
	for _, m := range corrections {
		data = append(data, store.Citation{
			Kind: "memory", ID: m.ID, Title: store.MemoryTypeLabel(m.Type), Text: m.Text,
			SourceRef: m.SourceRef, Revision: m.Revision, Corrected: true,
		})
		seen[m.ID] = true
	}
	for _, item := range items {
		if item.Kind == "memory" && seen[item.ID] {
			continue
		}
		data = append(data, item.Citation)
	}
	var context strings.Builder
	for i := range data {
		c := &data[i]
		c.Number = i + 1
		line, _ := json.Marshal(struct {
			Title     string `json:"title"`
			Text      string `json:"text"`
			Confirmed bool   `json:"confirmed_correction,omitempty"`
		}{c.Title, c.Text, c.Corrected})
		fmt.Fprintf(&context, "[%d] %s\n", c.Number, line)
	}
	return context.String(), data
}

// usedCitations accepts only references offered to the model and records each once.
func usedCitations(answer string, offered []store.Citation) []store.Citation {
	var used []store.Citation
	seen := make(map[int]bool)
	for _, match := range citationMarker.FindAllStringSubmatch(answer, -1) {
		number, _ := strconv.Atoi(match[1])
		if number > len(offered) || seen[number] {
			continue
		}
		used = append(used, offered[number-1])
		seen[number] = true
	}
	return used
}
