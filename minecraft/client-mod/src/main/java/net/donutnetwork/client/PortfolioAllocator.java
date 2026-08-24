package net.donutnetwork.client;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

final class PortfolioAllocator {
    record Selection(CandidateFeedClient.Candidate candidate, int batches) {}
    record Allocation(long balance, long deployable, int reserveBps, int availableOrderSlots,
                      int availableAuctionSlots, long riskAdjustedProfitDay, List<Selection> selections,
                      boolean timedOut) {}

    private static final int MAX_CANDIDATES = 30;
    private static final Duration SOLVER_BUDGET = Duration.ofMillis(100);

    Allocation allocate(List<CandidateFeedClient.Candidate> input, long balance, int usedOrderSlots, int usedAuctionSlots) {
        List<CandidateFeedClient.Candidate> candidates = input.stream()
                .filter(candidate -> candidate.state().equals("READY") && candidate.acquisitionCost() > 0
                        && candidate.conservativeProfit() > 0 && candidate.riskAdjustedProfitDay() > 0
                        && candidate.executableBatches() > 0)
                .sorted(Comparator.comparingLong(CandidateFeedClient.Candidate::riskAdjustedProfitDay).reversed()
                        .thenComparing(CandidateFeedClient.Candidate::id))
                .limit(MAX_CANDIDATES).toList();
        int quality = candidates.isEmpty() ? 0 : (int) candidates.stream()
                .mapToInt(candidate -> Math.min(candidate.confidenceBps(), candidate.completionBps())).average().orElse(0);
        int reserveBps = Math.max(1_500, Math.min(3_500, 3_500 - quality * 2_000 / 10_000));
        long deployable = safeMultiply(balance, 10_000 - reserveBps) / 10_000;
        int orderSlots = Math.max(0, 20 - usedOrderSlots);
        int auctionSlots = Math.max(0, 18 - usedAuctionSlots);
        Search search = new Search(candidates, deployable, orderSlots, auctionSlots,
                System.nanoTime() + SOLVER_BUDGET.toNanos());
        search.run(0, 0, 0, 0, 0, new int[candidates.size()], new HashMap<>(), new HashMap<>());
        List<Selection> selected = new ArrayList<>();
        for (int index = 0; index < search.best.length; index++) {
            if (search.best[index] > 0) selected.add(new Selection(candidates.get(index), search.best[index]));
        }
        return new Allocation(balance, deployable, reserveBps, orderSlots, auctionSlots,
                search.bestScore, List.copyOf(selected), search.timedOut);
    }

    private static final class Search {
        private final List<CandidateFeedClient.Candidate> candidates;
        private final long cashLimit;
        private final int orderLimit;
        private final int auctionLimit;
        private final long deadline;
        private final int[] best;
        private long bestScore;
        private boolean timedOut;

        private Search(List<CandidateFeedClient.Candidate> candidates, long cashLimit, int orderLimit,
                       int auctionLimit, long deadline) {
            this.candidates = candidates; this.cashLimit = cashLimit; this.orderLimit = orderLimit;
            this.auctionLimit = auctionLimit; this.deadline = deadline; this.best = new int[candidates.size()];
        }

        private void run(int index, long cash, int orders, int auctions, long score, int[] counts,
                         Map<String, Long> exactExposure, Map<String, Long> baseExposure) {
            if (System.nanoTime() >= deadline) { timedOut = true; return; }
            if (score > bestScore) { bestScore = score; System.arraycopy(counts, 0, best, 0, counts.length); }
            if (index >= candidates.size()) return;
            long optimistic = score;
            for (int rest = index; rest < candidates.size(); rest++) optimistic = safeAdd(optimistic,
                    safeMultiply(candidates.get(rest).riskAdjustedProfitDay(), candidates.get(rest).executableBatches()));
            if (optimistic <= bestScore) return;
            CandidateFeedClient.Candidate candidate = candidates.get(index);
            long exactCap = cashLimit / 4;
            long baseCap = cashLimit * 2 / 5;
            long exactUsed = exactExposure.getOrDefault(candidate.signature(), 0L);
            String base = candidate.itemId();
            long baseUsed = baseExposure.getOrDefault(base, 0L);
            int maximum = candidate.executableBatches();
            if (candidate.acquisitionCost() > 0) maximum = Math.min(maximum, (int) Math.min(Integer.MAX_VALUE, (cashLimit - cash) / candidate.acquisitionCost()));
            if (candidate.orderSlots() > 0) maximum = Math.min(maximum, (orderLimit - orders) / candidate.orderSlots());
            if (candidate.auctionSlots() > 0) maximum = Math.min(maximum, (auctionLimit - auctions) / candidate.auctionSlots());
            maximum = Math.min(maximum, (int) Math.max(0, (exactCap - exactUsed) / candidate.acquisitionCost()));
            maximum = Math.min(maximum, (int) Math.max(0, (baseCap - baseUsed) / candidate.acquisitionCost()));
            for (int count = maximum; count >= 0; count--) {
                counts[index] = count;
                long capital = safeMultiply(candidate.acquisitionCost(), count);
                if (count > 0) {
                    exactExposure.put(candidate.signature(), exactUsed + capital);
                    baseExposure.put(base, baseUsed + capital);
                }
                run(index + 1, cash + capital, orders + candidate.orderSlots() * count,
                        auctions + candidate.auctionSlots() * count,
                        safeAdd(score, safeMultiply(candidate.riskAdjustedProfitDay(), count)), counts,
                        exactExposure, baseExposure);
                if (count > 0) {
                    if (exactUsed == 0) exactExposure.remove(candidate.signature()); else exactExposure.put(candidate.signature(), exactUsed);
                    if (baseUsed == 0) baseExposure.remove(base); else baseExposure.put(base, baseUsed);
                }
            }
            counts[index] = 0;
        }
    }

    private static long safeMultiply(long value, long multiplier) {
        if (value <= 0 || multiplier <= 0) return 0;
        return value > Long.MAX_VALUE / multiplier ? Long.MAX_VALUE : value * multiplier;
    }
    private static long safeAdd(long left, long right) { return left > Long.MAX_VALUE - right ? Long.MAX_VALUE : left + right; }
}
