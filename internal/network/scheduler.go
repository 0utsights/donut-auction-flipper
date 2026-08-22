package network

import (
	"math"
	"sort"
	"time"
)

type WorkerState struct {
	WorkerID          string    `json:"worker_id"`
	Username          string    `json:"username"`
	Online            bool      `json:"online"`
	PingMS            int       `json:"ping_ms"`
	CurrentSearch     string    `json:"current_search"`
	AvailableBalance  int64     `json:"available_balance"`
	InventoryCapacity int       `json:"inventory_capacity"`
	SuccessRateBPS    int       `json:"success_rate_bps"`
	Region            string    `json:"region,omitempty"`
	Capabilities      []string  `json:"capabilities,omitempty"`
	LastHeartbeat     time.Time `json:"last_heartbeat"`
}
type SearchTarget struct {
	ID                string  `json:"id"`
	Query             string  `json:"query"`
	Category          string  `json:"category"`
	ExpectedProfit    int64   `json:"expected_profit"`
	ListingsPerMinute float64 `json:"listings_per_minute"`
	CompetitionBPS    int     `json:"competition_bps"`
	MinBalance        int64   `json:"min_balance"`
	DesiredRedundancy int     `json:"desired_redundancy"`
}
type Assignment struct {
	WorkerID   string       `json:"worker_id"`
	Target     SearchTarget `json:"target"`
	Score      int64        `json:"score"`
	AssignedAt time.Time    `json:"assigned_at"`
}

func Schedule(workers []WorkerState, targets []SearchTarget, now time.Time) []Assignment {
	type candidate struct {
		wi, ti int
		score  int64
	}
	candidates := []candidate{}
	for wi, w := range workers {
		if !w.Online || now.Sub(w.LastHeartbeat) > 30*time.Second || w.InventoryCapacity <= 0 {
			continue
		}
		for ti, t := range targets {
			if w.AvailableBalance < t.MinBalance {
				continue
			}
			profitScore := t.ExpectedProfit / 1000
			frequency := int64(t.ListingsPerMinute * 100)
			latencyPenalty := int64(w.PingMS * 15)
			competitionPenalty := int64(t.CompetitionBPS * 2)
			successBonus := int64(w.SuccessRateBPS * 3)
			switchPenalty := int64(0)
			if w.CurrentSearch != "" && w.CurrentSearch != t.ID {
				switchPenalty = 500
			}
			candidates = append(candidates, candidate{wi, ti, profitScore + frequency + successBonus - latencyPenalty - competitionPenalty - switchPenalty})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	usedW := map[int]bool{}
	coverage := map[int]int{}
	out := []Assignment{}
	for _, c := range candidates {
		desired := max(1, targets[c.ti].DesiredRedundancy)
		if usedW[c.wi] || coverage[c.ti] >= desired {
			continue
		}
		usedW[c.wi] = true
		coverage[c.ti]++
		out = append(out, Assignment{WorkerID: workers[c.wi].WorkerID, Target: targets[c.ti], Score: c.score, AssignedAt: now})
	}
	return out
}

type ShardingResult struct {
	Workers                int     `json:"workers"`
	BroadMedianLatencyMS   float64 `json:"broad_median_latency_ms"`
	ShardedMedianLatencyMS float64 `json:"sharded_median_latency_ms"`
	CoveragePercent        float64 `json:"coverage_percent"`
}

func CompareSharding(workerCount, itemTypes int, listingsPerSecond float64) ShardingResult {
	if workerCount < 1 {
		workerCount = 1
	}
	if itemTypes < 1 {
		itemTypes = 1
	}
	broad := 20 + listingsPerSecond*float64(itemTypes)*0.08
	sharded := 20 + listingsPerSecond*float64(itemTypes)/float64(workerCount)*0.08
	coverage := math.Min(100, float64(workerCount)/float64(itemTypes)*100)
	return ShardingResult{workerCount, broad, sharded, coverage}
}
