package net.donutnetwork.client;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

public final class ListingParser {
    private static final Pattern PRICE = Pattern.compile(
            "(?i)(?:\\b(?:price|cost|buy(?:\\s+it)?\\s+now|bin)\\b\\s*[:=-]?\\s*\\$?" +
                    "|\\$\\s*)([0-9][0-9, ]*(?:\\.[0-9]+)?)\\s*([kmbt])?\\b");
    private static final Pattern SELLER = Pattern.compile(
            "(?i)\\b(?:seller|sold\\s+by|owner)\\b\\s*[:=-]\\s*([A-Za-z0-9_]{1,32})");
    private static final Pattern LISTING_ID = Pattern.compile(
            "(?i)\\b(?:auction|listing)\\s*(?:id|#)\\s*[:#=-]?\\s*([A-Za-z0-9_-]{3,80})");
    private static final Pattern UNIT_PRICE = Pattern.compile(
            "(?i)(?:\\bper\\s+(?:item|unit)\\b|/\\s*(?:item|unit)\\b|\\beach\\b)");
    private static final Pattern LEGACY_FORMATTING = Pattern.compile("§[0-9A-FK-ORa-fk-or]");
    private static final Map<String, Long> MULTIPLIERS = Map.of(
            "k", 1_000L, "m", 1_000_000L, "b", 1_000_000_000L, "t", 1_000_000_000_000L);

    private ListingParser() {}

    public static Optional<ParsedListing> tryParse(ItemStackView item) {
        try {
            return Optional.of(parse(item));
        } catch (IllegalArgumentException | ArithmeticException ignored) {
            return Optional.empty();
        }
    }

    public static ParsedListing parse(ItemStackView item) {
        if (item == null || item.itemId().isBlank()) {
            throw new IllegalArgumentException("auction item is missing");
        }
        int quantity = Math.max(1, item.quantity());
        long price = totalPrice(item.lore(), quantity);
        String seller = item.seller().isBlank() ? find(item.lore(), SELLER).orElse("") : item.seller();
        String listingId = find(item.lore(), LISTING_ID).orElse(item.authoritativeId());
        return new ParsedListing(listingId, signature(item), price, quantity, seller);
    }

    public static long parsePrice(String text) {
        Matcher matcher = PRICE.matcher(clean(text));
        if (!matcher.find()) {
            return -1;
        }
        String decimal = matcher.group(1).replace(",", "").replace(" ", "");
        String suffix = matcher.group(2) == null ? "" : matcher.group(2).toLowerCase(Locale.ROOT);
        long multiplier = suffix.isEmpty() ? 1L : MULTIPLIERS.get(suffix);
        return new BigDecimal(decimal).multiply(BigDecimal.valueOf(multiplier))
                .setScale(0, RoundingMode.UNNECESSARY).longValueExact();
    }

    private static long totalPrice(Iterable<String> lines, int quantity) {
        for (String line : lines) {
            long parsed = parsePrice(line);
            if (parsed > 0) {
                return UNIT_PRICE.matcher(clean(line)).find() ? Math.multiplyExact(parsed, quantity) : parsed;
            }
        }
        throw new IllegalArgumentException("auction price missing from item lore");
    }

    static String signature(ItemStackView item) {
        ArrayList<String> modifiers = new ArrayList<>();
        item.enchantments().entrySet().stream()
                .sorted(Comparator.comparing(entry -> normalizeText(entry.getKey())))
                .forEach(entry -> modifiers.add(normalizeText(entry.getKey()) + "=" + entry.getValue()));
        if (!item.trimPattern().isBlank()) {
            modifiers.add("trim=" + normalizeText(item.trimPattern()) + ":" + normalizeText(item.trimMaterial()));
        }
        if (!item.displayName().isBlank()) {
            modifiers.add("name=" + normalizeText(item.displayName()));
        }
        if (item.durability() > 0 && item.maxDurability() > 0) {
            modifiers.add("durability=" + (item.durability() * 10 / item.maxDurability()));
        }
        item.components().entrySet().stream()
                .filter(entry -> relevantComponent(entry.getKey()))
                .sorted(Map.Entry.comparingByKey())
                .forEach(entry -> modifiers.add(normalizeText(entry.getKey()) + "=" + normalizeText(entry.getValue())));
        String base = normalizeId(item.itemId());
        return modifiers.isEmpty() ? base : base + "|" + String.join(";", modifiers);
    }

    private static boolean relevantComponent(String key) {
        return key.equals("donut:rarity") || key.equals("donut:level") ||
                key.equals("minecraft:custom_model_data") || key.equals("minecraft:dyed_color");
    }

    private static Optional<String> find(Iterable<String> lines, Pattern pattern) {
        for (String line : lines) {
            Matcher matcher = pattern.matcher(clean(line));
            if (matcher.find()) {
                return Optional.of(matcher.group(1));
            }
        }
        return Optional.empty();
    }

    private static String clean(String text) {
        return LEGACY_FORMATTING.matcher(text == null ? "" : text).replaceAll("").trim();
    }

    private static String normalizeId(String id) {
        String value = id.trim().toLowerCase(Locale.ROOT);
        return value.contains(":") ? value : "minecraft:" + value;
    }

    private static String normalizeText(String value) {
        String normalized = value.trim().toLowerCase(Locale.ROOT).replaceAll("\\s+", "_");
        return normalized.replaceAll("[^a-z0-9:_-]", "").replaceAll("^_+|_+$", "");
    }
}
