package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

class ListingParserTest {
    @Test
    void parsesFormattedAndDecimalPrices() {
        assertEquals(281_000_000L, ListingParser.parsePrice("§aPrice: $281m"));
        assertEquals(1_234_567L, ListingParser.parsePrice("Price: 1,234,567"));
        assertEquals(1_500_000L, ListingParser.parsePrice("Buy Now: $1.5m"));
        assertEquals(-1, ListingParser.parsePrice("Page 2 of 9"));
    }

    @Test
    void convertsExplicitPerItemPriceToTotalStackPrice() {
        ItemStackView item = new ItemStackView("totem_of_undying", 8, "",
                List.of("Price per item: $2.5m"), Map.of(), "seller", "listing-1");
        ParsedListing listing = ListingParser.parse(item);
        assertEquals(20_000_000L, listing.totalPrice());
        assertEquals(2_500_000L, listing.unitPrice());
    }

    @Test
    void extractsSellerAndAuthoritativeListingId() {
        ItemStackView item = new ItemStackView("elytra", 1, "", List.of(
                "§aPrice: $281m", "Seller: DonutFan_7", "Auction ID: abc-123"),
                Map.of(), "", "screen:4:slot:10");
        ParsedListing listing = ListingParser.parse(item);
        assertEquals("DonutFan_7", listing.seller());
        assertEquals("abc-123", listing.listingId());
    }

    @Test
    void signatureOrderingAndModifiersMatchBackendShape() {
        ItemStackView a = new ItemStackView("netherite_sword", 1, "Blade", List.of("Price: $10m"),
                Map.of("unbreaking", 3, "sharpness", 5), "seller", "", 90, 100,
                "spire", "gold", Map.of("minecraft:dyed_color", "42"));
        ItemStackView b = new ItemStackView("minecraft:netherite_sword", 1, "Blade", List.of("Price: $10m"),
                Map.of("sharpness", 5, "unbreaking", 3), "seller", "", 90, 100,
                "spire", "gold", Map.of("minecraft:dyed_color", "42"));
        assertEquals(ListingParser.parse(a).signature(), ListingParser.parse(b).signature());
        assertEquals("minecraft:netherite_sword|sharpness=5;unbreaking=3;" +
                "trim=spire:gold;name=blade;durability=9;minecraft:dyed_color=42",
                ListingParser.parse(a).signature());
    }

    @Test
    void rejectsMissingPrice() {
        ItemStackView item = new ItemStackView("elytra", 1, "", List.of("Seller: Alex"),
                Map.of(), "Alex", "");
        assertThrows(IllegalArgumentException.class, () -> ListingParser.parse(item));
        assertTrue(ListingParser.tryParse(item).isEmpty());
    }

    @Test
    void priceCacheFallsBackToBaseSignature() {
        PriceSnapshotCache cache = new PriceSnapshotCache(Duration.ofMinutes(1));
        PriceSnapshotCache.Value value = new PriceSnapshotCache.Value(100, 90, 7000, 5);
        cache.replace(new PriceSnapshotCache.Snapshot(1, Instant.now(),
                Map.of("minecraft:netherite_sword", value)));
        assertSame(value, cache.get("minecraft:netherite_sword|sharpness=5"));
    }
}
