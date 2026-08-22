package net.donutnetwork.client;

import java.util.Locale;
import java.util.regex.Pattern;

/** Conservative title-based recognition; false negatives are safer than parsing an unrelated chest. */
public final class AuctionScreenClassifier {
    private static final Pattern AUCTION_TITLE = Pattern.compile(
            "\\b(?:auction(?:s| house)?|browse auctions|market listings)\\b");
    private static final Pattern NON_LISTING_TITLE = Pattern.compile(
            "\\b(?:confirm|purchase|buying|create auction|your auctions|expired)\\b");

    private AuctionScreenClassifier() {}

    public static boolean isListingScreen(String title, int nonPlayerSlots) {
        String normalized = title == null ? "" : title.strip().toLowerCase(Locale.ROOT);
        return nonPlayerSlots >= 9 && AUCTION_TITLE.matcher(normalized).find()
                && !NON_LISTING_TITLE.matcher(normalized).find();
    }
}
