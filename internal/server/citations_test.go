package server

import (
	"strings"
	"testing"

	"digital-twin/internal/rag"
	"digital-twin/internal/store"
)

func TestCitationContextAndUsedReferences(t *testing.T) {
	corrections := []store.MemoryRecord{{ID: 3, Type: "fact", Text: "周日整理笔记", Revision: 2}}
	items := []rag.RetrievalItem{
		{Citation: store.Citation{Kind: "memory", ID: 3, Text: "周五整理笔记"}},
		{Citation: store.Citation{Kind: "knowledge", ID: 3, Text: "文档原文", Title: "笔记方法"}},
	}
	context, offered := citationContext(items, corrections)
	if len(offered) != 2 || strings.Contains(context, "周五") || !strings.Contains(context, "confirmed_correction") {
		t.Fatalf("unexpected prompt and citations: %s %+v", context, offered)
	}
	used := usedCitations("先看文档 [2]，再看纠正 [1]。重复 [2]。无效 [3]。", offered)
	if len(used) != 2 || used[0].Number != 2 || used[1].Number != 1 {
		t.Fatalf("unexpected used citations: %+v", used)
	}
}
