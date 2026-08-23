package knowledge

import "testing"

func TestInsightPriorityAndReviewOrdering(t *testing.T) {
	if insightPriority("retracted", "current", false) >= insightPriority("stale", "current", false) {
		t.Fatal("retracted decisions must rank before stale decisions")
	}
	if insightPriority("active", "stale", false) >= insightPriority("active", "current", true) {
		t.Fatal("stale decisions must rank before historical changes")
	}

	items := []insightReviewItem{
		{Kind: "article_drift", FactID: "fact-b", ArticleID: "article-b"},
		{Kind: "retracted", FactID: "fact-z", Version: 2},
		{Kind: "stale", FactID: "fact-a", Version: 1},
		{Kind: "superseded", FactID: "fact-a", Version: 1},
	}
	sortInsightReviewItems(items)
	if items[0].Kind != "retracted" || items[1].Kind != "stale" || items[2].Kind != "superseded" || items[3].Kind != "article_drift" {
		t.Fatalf("review ordering = %+v", items)
	}
}

func TestInsightSensitivityRanking(t *testing.T) {
	if sensitivityRankInsight("normal") != 0 || sensitivityRankInsight("sensitive") != 1 || sensitivityRankInsight("restricted") != 2 {
		t.Fatal("sensitivity ranking is not ordered")
	}
}
