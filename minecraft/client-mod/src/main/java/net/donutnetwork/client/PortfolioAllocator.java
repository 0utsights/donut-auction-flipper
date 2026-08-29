package net.donutnetwork.client;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

final class PortfolioAllocator {
    record Selection(CandidateFeedClient.Candidate candidate, int batches) {
        int orderQuantity() { return Math.multiplyExact(candidate.quantity(), batches); }
        long capital() { return safeMultiply(candidate.acquisitionCost(), batches); }
        long conservativeProfit() { return safeMultiply(candidate.conservativeProfit(), batches); }
        long riskAdjustedProfitDay() { return safeMultiply(candidate.riskAdjustedProfitDay(), batches); }
    }

    record Allocation(long balance, long deployable, int reserveBps, int availableOrderSlots,
                      int availableAuctionSlots, long minimumProfitPerExit,
                      long riskAdjustedProfitDay, List<Selection> selections) {
        long selectedCapital() { return selections.stream().mapToLong(Selection::capital).reduce(0, PortfolioAllocator::safeAdd); }
        int selectedOrderSlots() { return selections.stream().mapToInt(value -> value.candidate().orderSlots()).sum(); }
        int totalExitBatches() { return selections.stream().mapToInt(Selection::batches).sum(); }
    }

    private static final int MAX_CANDIDATES = 30;

    Allocation allocate(List<CandidateFeedClient.Candidate> input, long balance, int usedOrderSlots, int usedAuctionSlots) {
        return allocate(input, balance, usedOrderSlots, usedAuctionSlots, Set.of());
    }

    Allocation allocate(List<CandidateFeedClient.Candidate> input, long balance, int usedOrderSlots, int usedAuctionSlots,
                        Set<String> activeOrderItems) {
        List<CandidateFeedClient.Candidate> eligible = input.stream()
                .filter(candidate -> candidate.route().equals("ORDER_TO_AUCTION") && candidate.state().equals("READY")
                        && candidate.acquisitionCost() > 0 && candidate.conservativeProfit() > 0
                        && candidate.riskAdjustedProfitDay() > 0 && candidate.executableBatches() > 0
                        && !activeOrderItems.contains(candidate.itemId()))
                .toList();

        int quality = eligible.isEmpty() ? 0 : (int) eligible.stream()
                .mapToInt(candidate -> Math.min(candidate.confidenceBps(), candidate.completionBps())).average().orElse(0);
        int reserveBps = Math.max(1_500, Math.min(3_500, 3_500 - quality * 2_000 / 10_000));
        long deployable = safeMultiply(balance, 10_000 - reserveBps) / 10_000;
        int orderSlots = Math.max(0, 20 - usedOrderSlots);
        int auctionSlots = Math.max(0, 18 - usedAuctionSlots);

        // Every candidate is one exact exit listing (at most 64 items). Keep a
        // balance-scaled 1% per-slot target for UI explanation and ranking
        // context, but do not use it as a hard gate while order slots are idle.
        // The optimizer already prefers the strongest opportunity frontier;
        // weaker positive trades are useful temporary fillers and can later be
        // cancelled when better markets qualify.
        int planningAuctionSlots = Math.max(1, auctionSlots);
        long minimumProfitPerExit = safeMultiply(deployable / planningAuctionSlots, 100) / 10_000;
        List<CandidateFeedClient.Candidate> ranked = eligible.stream()
                .sorted(Comparator.comparingLong(PortfolioAllocator::marketOpportunity).reversed()
                        .thenComparing(Comparator.comparingLong(CandidateFeedClient.Candidate::riskAdjustedProfitDay).reversed())
                        .thenComparing(Comparator.comparingLong(CandidateFeedClient.Candidate::conservativeProfit).reversed())
                        .thenComparing(CandidateFeedClient.Candidate::id))
                .toList();
        Map<String, CandidateFeedClient.Candidate> unique = new LinkedHashMap<>();
        for (CandidateFeedClient.Candidate candidate : ranked) unique.putIfAbsent(candidate.itemId(), candidate);
        List<CandidateFeedClient.Candidate> candidates = unique.values().stream().limit(MAX_CANDIDATES).toList();
        if (candidates.isEmpty() || deployable <= 0 || orderSlots <= 0) {
            return new Allocation(balance, deployable, reserveBps, orderSlots, auctionSlots, minimumProfitPerExit, 0, List.of());
        }

        // A large acquisition order creates sequential exact-stack exits; it
        // does not permanently consume one auction slot per future listing.
        // Auction availability remains visible, but only local cash and measured
        // volume size the acquisition order in this release.
        long perItemExposure = Math.max(1, deployable / 4);
        int[] maxima = new int[candidates.size()];
        for (int index = 0; index < candidates.size(); index++) {
            CandidateFeedClient.Candidate candidate = candidates.get(index);
            maxima[index] = (int) Math.min(candidate.executableBatches(), perItemExposure / candidate.acquisitionCost());
        }

        // Maximize distinct profitable offers first, then choose the strongest
        // total market opportunities among equally broad sets.
        Node activated = activate(candidates, maxima, deployable, orderSlots);
        int[] counts = activated.counts().clone();
        long cash = activated.cash();

        // With the item set fixed, batches are linear. Fund them in
        // risk-adjusted-profit/day per dollar order, in bulk, without iterating
        // once per stack. This supports million-unit order quantities.
        List<Integer> selected = new ArrayList<>();
        for (int index = 0; index < counts.length; index++) if (counts[index] > 0) selected.add(index);
        selected.sort((left, right) -> {
            CandidateFeedClient.Candidate a = candidates.get(left), b = candidates.get(right);
            int ratio = Double.compare((double) b.riskAdjustedProfitDay() / b.acquisitionCost(),
                    (double) a.riskAdjustedProfitDay() / a.acquisitionCost());
            if (ratio != 0) return ratio;
            return Long.compare(marketOpportunity(b), marketOpportunity(a));
        });
        for (int index : selected) {
            CandidateFeedClient.Candidate candidate = candidates.get(index);
            int additional = Math.min(maxima[index] - counts[index],
                    (int) Math.min(Integer.MAX_VALUE, (deployable - cash) / candidate.acquisitionCost()));
            if (additional <= 0) continue;
            counts[index] += additional;
            cash = safeAdd(cash, safeMultiply(candidate.acquisitionCost(), additional));
        }

        List<Selection> selections = new ArrayList<>();
        long score = 0;
        for (int index = 0; index < counts.length; index++) {
            if (counts[index] <= 0) continue;
            Selection value = new Selection(candidates.get(index), counts[index]);
            selections.add(value);
            score = safeAdd(score, value.riskAdjustedProfitDay());
        }
        selections.sort(Comparator.comparingLong(Selection::riskAdjustedProfitDay).reversed()
                .thenComparing(value -> value.candidate().id()));
        return new Allocation(balance, deployable, reserveBps, orderSlots, auctionSlots, minimumProfitPerExit, score, List.copyOf(selections));
    }

    private static Node activate(List<CandidateFeedClient.Candidate> candidates, int[] maxima,
                                 long cashLimit, int orderLimit) {
        Map<Integer, List<Node>> frontier = new HashMap<>();
        frontier.put(0, List.of(new Node(0, 0, 0, new int[candidates.size()])));
        for (int index = 0; index < candidates.size(); index++) {
            CandidateFeedClient.Candidate candidate = candidates.get(index);
            Map<Integer, List<Node>> expanded = new HashMap<>();
            for (List<Node> nodes : frontier.values()) {
                for (Node node : nodes) {
                    expanded.computeIfAbsent(node.orders(), ignored -> new ArrayList<>()).add(node);
                    int orders = node.orders() + candidate.orderSlots();
                    long cash = safeAdd(node.cash(), candidate.acquisitionCost());
                    if (maxima[index] < 1 || orders > orderLimit || cash > cashLimit) continue;
                    int[] counts = node.counts().clone();
                    counts[index] = 1;
                    expanded.computeIfAbsent(orders, ignored -> new ArrayList<>())
                            .add(new Node(cash, safeAdd(node.score(), marketOpportunity(candidate)), orders, counts));
                }
            }
            frontier = prune(expanded);
        }
        return frontier.values().stream().flatMap(List::stream)
                .max(Comparator.comparingInt(Node::orders).thenComparingLong(Node::score)
                        .thenComparing(Comparator.comparingLong(Node::cash).reversed()))
                .orElseGet(() -> new Node(0, 0, 0, new int[candidates.size()]));
    }

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

    private static long marketOpportunity(CandidateFeedClient.Candidate candidate) {
        return safeMultiply(candidate.riskAdjustedProfitDay(), candidate.executableBatches());
    }

    private record Node(long cash, long score, int orders, int[] counts) {}

    private static long safeMultiply(long value, long multiplier) {
        if (value <= 0 || multiplier <= 0) return 0;
        return value > Long.MAX_VALUE / multiplier ? Long.MAX_VALUE : value * multiplier;
    }
    private static long safeAdd(long left, long right) {
        return left > Long.MAX_VALUE - right ? Long.MAX_VALUE : left + right;
    }
}
