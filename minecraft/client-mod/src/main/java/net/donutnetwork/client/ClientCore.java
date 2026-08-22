package net.donutnetwork.client;

import java.time.Duration;
import java.time.Instant;
import java.util.Optional;
import java.util.concurrent.ArrayBlockingQueue;

public final class ClientCore {
    private final PriceSnapshotCache prices = new PriceSnapshotCache(Duration.ofMinutes(2));
    private final AuctionMarketIndex localMarket = new AuctionMarketIndex();
    private final ArrayBlockingQueue<TelemetryEvent> telemetry = new ArrayBlockingQueue<>(2048);
    private final FlipEvaluator.Thresholds thresholds =
            new FlipEvaluator.Thresholds(1_000_000L, 400, 5_000, 900_000_000L, 1);
    private final FlipEvaluator evaluator = new FlipEvaluator(prices, thresholds);

    public void initialize() {
        // The entrypoint installs the version-specific adapter. This core stays mapping-independent and testable.
    }

    public FlipEvaluator.Decision onListingChanged(ItemStackView item) {
        long observed = System.nanoTime();
        ParsedListing listing = ListingParser.parse(item);
        FlipEvaluator.Decision decision = evaluator.evaluate(listing, observed);
        telemetry.offer(TelemetryEvent.observed(listing, decision, observed));
        return decision;
    }

    public Optional<ListingEvaluation> onSlotChanged(String location, ItemStackView item) {
        long started = System.nanoTime();
        Optional<ParsedListing> parsed = ListingParser.tryParse(item);
        if (parsed.isEmpty()) {
            localMarket.remove(location);
            return Optional.empty();
        }
        ParsedListing listing = parsed.get();
        AuctionMarketIndex.Quote localQuote = localMarket.upsertAndQuote(location, listing);
        PriceSnapshotCache.Value official = prices.stale(Instant.now()) ? null : prices.get(listing.signature());
        ListingEvaluation evaluation = official == null
                ? evaluateLocal(listing, localQuote, started)
                : evaluateOfficial(listing, official, localQuote, started);
        telemetry.offer(new TelemetryEvent(evaluation.opportunity() ? "FLIP_DETECTED" : "LISTING_OBSERVED",
                listing.listingId(), listing.signature(), listing.totalPrice(), evaluation.latencyNanos(),
                System.currentTimeMillis()));
        return Optional.of(evaluation);
    }

    public void onSlotCleared(String location) {
        localMarket.remove(location);
    }

    public void onScreenClosed(String screenPrefix) {
        localMarket.clearScreen(screenPrefix);
    }

    public PriceSnapshotCache prices() {
        return prices;
    }

    public TelemetryEvent pollTelemetry() {
        return telemetry.poll();
    }

    int indexedListings() {
        return localMarket.size();
    }

    private ListingEvaluation evaluateOfficial(ParsedListing listing, PriceSnapshotCache.Value value,
                                                 AuctionMarketIndex.Quote localQuote, long started) {
        FlipEvaluator.Decision decision = evaluator.evaluate(listing, started);
        long officialTotal = safeMultiply(value.quickSellValue(), listing.quantity());
        long screenTotal = localQuote.comparableListings() >= 3
                ? safeMultiply(localQuote.medianUnitPrice(), listing.quantity()) : 0;
        return new ListingEvaluation(listing, ListingEvaluation.Source.OFFICIAL_SNAPSHOT, decision.flip(),
                officialTotal, decision.profit(), decision.marginBps(), localQuote.comparableListings(),
                screenTotal, officialTotal, spreadBps(screenTotal, officialTotal),
                decision.latencyNanos(), decision.reason());
    }

    private ListingEvaluation evaluateLocal(ParsedListing listing, AuctionMarketIndex.Quote quote, long started) {
        if (quote.comparableListings() < 3 || quote.medianUnitPrice() <= 0) {
            return new ListingEvaluation(listing, ListingEvaluation.Source.NONE, false, 0, 0, 0,
                    quote.comparableListings(), 0, 0, 0, System.nanoTime() - started,
                    "learning screen market");
        }
        long referenceTotal;
        long profit;
        try {
            referenceTotal = Math.multiplyExact(quote.medianUnitPrice(), Math.max(1, listing.quantity()));
            profit = Math.subtractExact(referenceTotal, listing.totalPrice());
        } catch (ArithmeticException overflow) {
            return new ListingEvaluation(listing, ListingEvaluation.Source.AUCTION_SCREEN, false, 0, 0, 0,
                    quote.comparableListings(), 0, 0, 0, System.nanoTime() - started, "price overflow");
        }
        int margin = listing.totalPrice() > 0
                ? (int) Math.max(Integer.MIN_VALUE, Math.min(Integer.MAX_VALUE,
                profit * 10_000L / listing.totalPrice())) : 0;
        boolean opportunity = listing.totalPrice() <= thresholds.maxPurchasePrice()
                && profit >= thresholds.minProfit() && margin >= thresholds.minMarginBps();
        return new ListingEvaluation(listing, ListingEvaluation.Source.AUCTION_SCREEN, opportunity,
                referenceTotal, profit, margin, quote.comparableListings(), referenceTotal, 0, 0,
                System.nanoTime() - started, opportunity ? "below screen median" : "insufficient screen edge");
    }

    private static long safeMultiply(long unitValue, int quantity) {
        try {
            return Math.multiplyExact(unitValue, Math.max(1, quantity));
        } catch (ArithmeticException overflow) {
            return 0;
        }
    }

    private static int spreadBps(long screenTotal, long officialTotal) {
        if (screenTotal <= 0 || officialTotal <= 0) {
            return 0;
        }
        long difference = screenTotal - officialTotal;
        if (Math.abs(difference) > Long.MAX_VALUE / 10_000L) {
            return difference > 0 ? Integer.MAX_VALUE : Integer.MIN_VALUE;
        }
        long spread = difference * 10_000L / officialTotal;
        return (int) Math.max(Integer.MIN_VALUE, Math.min(Integer.MAX_VALUE, spread));
    }
}
