package rag

import (
	"reflect"
	"testing"
)

func TestSplitKeywords(t *testing.T) {
	kws := SplitKeywords("我的理想L6刹车异响怎么解决", 8)
	want := []string{"我的", "的理", "理想", "想L", "L6", "6刹", "刹车", "车异"}
	if !reflect.DeepEqual(kws, want) {
		t.Fatalf("got %v, want %v", kws, want)
	}
}

func TestSplitKeywordsSkipsPunct(t *testing.T) {
	kws := SplitKeywords("怎么,解决！这个问题", 4)
	for _, k := range kws {
		if k == "" {
			t.Fatalf("出现空关键词")
		}
	}
	if len(kws) > 4 {
		t.Fatalf("超过 max 限制: %v", kws)
	}
}

func TestRRF(t *testing.T) {
	// 排名 0 的分必须大于排名 1 的分
	if rrf(0, 60) <= rrf(1, 60) {
		t.Fatalf("rrf 排名序错误")
	}
}
