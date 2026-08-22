package net.donutnetwork.client;

public record ListingEvaluation(ParsedListing listing, Source source, boolean opportunity,
                                long referenceTotal, long expectedProfit, int marginBps,
                                int comparableListings, long screenMedianTotal,
                                long officialQuickSellTotal, int sourceSpreadBps,
                                long latencyNanos, String reason) {
    public enum Source { OFFICIAL_SNAPSHOT, AUCTION_SCREEN, NONE }
}
