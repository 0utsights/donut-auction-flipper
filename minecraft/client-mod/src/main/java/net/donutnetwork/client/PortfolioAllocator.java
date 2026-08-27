package net.donutnetwork.client;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.LinkedHashMap;
import java.util.Set;

final class PortfolioAllocator {
    record Selection(CandidateFeedClient.Candidate candidate, int batches) {
        int orderQuantity() { return Math.multiplyExact(candidate.quantity(), batches); }
        long capital() { return safeMultiply(candidate.acquisitionCost(), batches); }
        long conservativeProfit() { return safeMultiply(candidate.conservativeProfit(), batches); }
        long riskAdjustedProfitDay() { return safeMultiply(candidate.riskAdjustedProfitDay(), batches); }
    }
    record Allocation(long balance, long deployable, int reserveBps, int availableOrderSlots,
                      int availableAuctionSlots, long riskAdjustedProfitDay, List<Selection> selections) {}

    private static final int MAX_CANDIDATES = 30;

    Allocation allocate(List<CandidateFeedClient.Candidate> input, long balance, int usedOrderSlots, int usedAuctionSlots) {
        return allocate(input, balance, usedOrderSlots, usedAuctionSlots, Set.of());
    }

    Allocation allocate(List<CandidateFeedClient.Candidate> input, long balance, int usedOrderSlots, int usedAuctionSlots,
                        Set<String> activeOrderItems) {
        List<CandidateFeedClient.Candidate> ranked = input.stream()
                .filter(candidate -> candidate.route().equals("ORDER_TO_AUCTION") && candidate.state().equals("READY") && candidate.acquisitionCost() > 0
                        && candidate.conservativeProfit() > 0 && candidate.riskAdjustedProfitDay() > 0
                        && candidate.executableBatches() > 0 && !activeOrderItems.contains(candidate.itemId()))
                .sorted(Comparator.comparingLong(CandidateFeedClient.Candidate::riskAdjustedProfitDay).reversed()
                        .thenComparing(CandidateFeedClient.Candidate::id))
                .toList();
        // A Donut account may have only one acquisition order for an item. Keep
        // the strongest route if malformed or future feeds contain duplicates.
        Map<String, CandidateFeedClient.Candidate> unique = new LinkedHashMap<>();
        for (CandidateFeedClient.Candidate candidate : ranked) unique.putIfAbsent(candidate.itemId(), candidate);
        List<CandidateFeedClient.Candidate> candidates = unique.values().stream().limit(MAX_CANDIDATES).toList();
        int quality = candidates.isEmpty() ? 0 : (int) candidates.stream()
                .mapToInt(candidate -> Math.min(candidate.confidenceBps(), candidate.completionBps())).average().orElse(0);
        int reserveBps = Math.max(1_500, Math.min(3_500, 3_500 - quality * 2_000 / 10_000));
        long deployable = safeMultiply(balance, 10_000 - reserveBps) / 10_000;
        int orderSlots = Math.max(0, 20 - usedOrderSlots);
        int auctionSlots = Math.max(0, 18 - usedAuctionSlots);
        int usefulItems = Math.max(1, Math.min(candidates.size(), Math.min(orderSlots, auctionSlots)));
        int maxBatchesPerItem = auctionSlots <= 0 ? 0 : Math.max(1, (auctionSlots + usefulItems - 1) / usefulItems);
        Node best = optimize(candidates, deployable, orderSlots, auctionSlots, maxBatchesPerItem);
        List<Selection> selected = new ArrayList<>();
        for (int index = 0; index < best.counts().length; index++) {
            if (best.counts()[index] > 0) selected.add(new Selection(candidates.get(index), best.counts()[index]));
        }
        return new Allocation(balance, deployable, reserveBps, orderSlots, auctionSlots,
                best.score(), List.copyOf(selected));
    }

    /** Exact multiple-choice knapsack over the tiny Donut slot frontier. */
    private static Node optimize(List<CandidateFeedClient.Candidate> candidates, long cashLimit,
                                 int orderLimit, int auctionLimit, int maxBatchesPerItem) {
        Map<Integer, List<Node>> frontier = new HashMap<>();
        frontier.put(0, List.of(new Node(0, 0, 0, 0, new int[candidates.size()])));
        long exactExposureCap = cashLimit / 4;
        for (int index = 0; index < candidates.size(); index++) {
            CandidateFeedClient.Candidate candidate = candidates.get(index);
            int maximum = Math.min(candidate.executableBatches(), maxBatchesPerItem);
            if (candidate.auctionSlots() > 0) maximum = Math.min(maximum, auctionLimit / candidate.auctionSlots());
            maximum = Math.min(maximum, candidate.acquisitionCost() <= 0 ? 0
                    : (int) Math.min(Integer.MAX_VALUE, exactExposureCap / candidate.acquisitionCost()));
            Map<Integer, List<Node>> expanded = new HashMap<>();
            for (List<Node> nodes : frontier.values()) {
                for (Node node : nodes) {
                    for (int count = 0; count <= maximum; count++) {
                        int orders = node.orders() + (count > 0 ? candidate.orderSlots() : 0);
                        int auctions = node.auctions() + candidate.auctionSlots() * count;
                        if (orders > orderLimit || auctions > auctionLimit) continue;
                        long capital = safeMultiply(candidate.acquisitionCost(), count);
                        long cash = safeAdd(node.cash(), capital);
                        if (cash > cashLimit) continue;
                        int[] counts = node.counts().clone();
                        counts[index] = count;
                        long score = safeAdd(node.score(), safeMultiply(candidate.riskAdjustedProfitDay(), count));
                        int key = orders * (auctionLimit + 1) + auctions;
                        expanded.computeIfAbsent(key, ignored -> new ArrayList<>())
                                .add(new Node(cash, score, orders, auctions, counts));
                    }
                }
            }
            frontier = prune(expanded);
        }
        return frontier.values().stream().flatMap(List::stream)
                .max(Comparator.comparingLong(Node::score)
                        .thenComparing(Comparator.comparingLong(Node::cash).reversed()))
                .orElseGet(() -> new Node(0, 0, 0, 0, new int[candidates.size()]));
    }

    /** Remove states that cost at least as much and score no better at equal slot use. */
    private static Map<Integer, List<Node>> prune(Map<Integer, List<Node>> values) {
        Map<Integer, List<Node>> result = new HashMap<>(values.size());
        for (Map.Entry<Integer, List<Node>> entry : values.entrySet()) {
            List<Node> sorted = entry.getValue().stream().sorted(Comparator.comparingLong(Node::cash)
                    .thenComparing(Comparator.comparingLong(Node::score).reversed())).toList();
            List<Node> kept = new ArrayList<>();
            long bestScore = -1;
            for (Node node : sorted) {
                if (node.score() <= bestScore) continue;
                kept.add(node);
                bestScore = node.score();
            }
            result.put(entry.getKey(), kept);
        }
        return result;
    }

    private record Node(long cash, long score, int orders, int auctions, int[] counts) {}

    private static long safeMultiply(long value, long multiplier) {
        if (value <= 0 || multiplier <= 0) return 0;
        return value > Long.MAX_VALUE / multiplier ? Long.MAX_VALUE : value * multiplier;
    }
    private static long safeAdd(long left, long right) { return left > Long.MAX_VALUE - right ? Long.MAX_VALUE : left + right; }
}
