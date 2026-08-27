package net.donutnetwork.client;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.util.Locale;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Immutable transaction values captured when a player explicitly arms one order. */
record OrderPlan(String candidateId, String signature, String itemId, String itemName, int batchQuantity, int batches,
                 int quantity, long unitRewardCents, long totalCents, long escrowDollars, long targetListPrice) {
    private static final Pattern MONEY = Pattern.compile("(?i)\\$?\\s*([0-9][0-9,]*(?:\\.[0-9]+)?)\\s*([KMBT]?)(?![A-Za-z])");
    private static final Pattern LABELED_QUANTITY = Pattern.compile("(?i)\\b(?:amount|quantity)\\s*:?\\s*([0-9][0-9,]*)\\b");

    static OrderPlan from(CandidateFeedClient.Candidate candidate) {
        return from(candidate, 1);
    }

    static OrderPlan from(PortfolioAllocator.Selection selection) {
        if (selection == null) throw new IllegalArgumentException("portfolio selection is missing");
        return from(selection.candidate(), selection.batches());
    }

    static OrderPlan from(CandidateFeedClient.Candidate candidate, int batches) {
        if (!"ORDER_TO_AUCTION".equals(candidate.route()) || !"READY".equals(candidate.state())) {
            throw new IllegalArgumentException("candidate is not a ready order-to-auction trade");
        }
        if (candidate.quantity() <= 0 || candidate.orderUnitRewardCents() <= 0 || candidate.acquisitionCost() <= 0) {
            throw new IllegalArgumentException("candidate has invalid order economics");
        }
        if (batches <= 0 || batches > candidate.executableBatches()) {
            throw new IllegalArgumentException("selected stack count exceeds conservative executable volume");
        }
        int quantity = Math.multiplyExact(candidate.quantity(), batches);
        long batchCents = Math.multiplyExact(candidate.orderUnitRewardCents(), candidate.quantity());
        long batchEscrow = Math.addExact(batchCents, 99) / 100;
        if (batchEscrow != candidate.acquisitionCost()) {
            throw new IllegalArgumentException("candidate escrow does not match its unit price and batch quantity");
        }
        long totalCents = Math.multiplyExact(candidate.orderUnitRewardCents(), quantity);
        long escrow = Math.addExact(totalCents, 99) / 100;
        return new OrderPlan(candidate.id(), candidate.signature(), candidate.itemId(), candidate.itemName(),
                candidate.quantity(), batches, quantity, candidate.orderUnitRewardCents(), totalCents, escrow, candidate.targetListPrice());
    }

    boolean matches(CandidateFeedClient.Candidate candidate) {
        return candidate != null && "READY".equals(candidate.state()) && "ORDER_TO_AUCTION".equals(candidate.route())
                && candidateId.equals(candidate.id()) && signature.equals(candidate.signature()) && itemId.equals(candidate.itemId())
                && batchQuantity == candidate.quantity() && batches <= candidate.executableBatches()
                && quantity == Math.multiplyExact(candidate.quantity(), batches) && unitRewardCents == candidate.orderUnitRewardCents()
                && candidate.acquisitionCost() == (Math.addExact(Math.multiplyExact(unitRewardCents, batchQuantity), 99) / 100)
                && targetListPrice == candidate.targetListPrice();
    }

    String priceInput() {
        long dollars = unitRewardCents / 100;
        long cents = unitRewardCents % 100;
        return cents == 0 ? Long.toString(dollars) : dollars + "." + String.format(Locale.ROOT, "%02d", cents);
    }

    String itemPathQuery() {
        int separator = itemId.indexOf(':');
        // Donut's item search accepts registry-style paths and distinguishes
        // them more reliably than display-name text (redstone_block, not
        // "redstone block"). The exact result is still registry-verified.
        return separator < 0 ? itemId : itemId.substring(separator + 1);
    }

    static String normalizeLabel(String value) {
        return value == null ? "" : value.replace("§", "").toLowerCase(Locale.ROOT)
                .replaceAll("[^a-z0-9]+", " ").strip();
    }

    static boolean equivalentItemLabel(String actual, String expectedRegistryName, String fallbackName) {
        String normalized = normalizeLabel(actual);
        if (normalized.equals(normalizeLabel(expectedRegistryName)) || normalized.equals(normalizeLabel(fallbackName))) return true;
        // Donut and vanilla occasionally reverse names such as "Diamond Block" and "Block of Diamond".
        return normalizedTokenSet(normalized).equals(normalizedTokenSet(normalizeLabel(expectedRegistryName)));
    }

    private static String normalizedTokenSet(String value) {
        return value.replace(" of ", " ").lines().flatMap(line -> Pattern.compile(" +").splitAsStream(line))
                .filter(token -> !token.isBlank()).sorted().reduce((left, right) -> left + " " + right).orElse("");
    }

    static boolean textContainsMoney(String text, long expectedCents) {
        Matcher matcher = MONEY.matcher(text == null ? "" : text.replace("§", ""));
        while (matcher.find()) {
            if (!matcher.group().stripLeading().startsWith("$") && matcher.group(2).isEmpty()) continue;
            BigDecimal multiplier = switch (matcher.group(2).toUpperCase(Locale.ROOT)) {
                case "K" -> BigDecimal.valueOf(1_000);
                case "M" -> BigDecimal.valueOf(1_000_000);
                case "B" -> BigDecimal.valueOf(1_000_000_000L);
                case "T" -> BigDecimal.valueOf(1_000_000_000_000L);
                default -> BigDecimal.ONE;
            };
            BigDecimal value = new BigDecimal(matcher.group(1).replace(",", "")).multiply(multiplier)
                    .multiply(BigDecimal.valueOf(100));
            long parsed;
            try { parsed = value.setScale(0, RoundingMode.HALF_UP).longValueExact(); }
            catch (ArithmeticException ignored) { continue; }
            if (matcher.group(2).isEmpty()) {
                if (parsed == expectedCents) return true;
            } else {
                // Abbreviated server text is accepted only when the expected exact value rounds to the displayed value.
                BigDecimal expected = BigDecimal.valueOf(expectedCents, 2).divide(multiplier, 12, RoundingMode.HALF_UP);
                int displayedScale = Math.max(0, new BigDecimal(matcher.group(1).replace(",", "")).scale());
                if (expected.setScale(displayedScale, RoundingMode.HALF_UP)
                        .compareTo(new BigDecimal(matcher.group(1).replace(",", ""))) == 0) return true;
            }
        }
        return false;
    }

    static boolean textContainsQuantity(String text, int expected) {
        Matcher matcher = LABELED_QUANTITY.matcher(text == null ? "" : text.replace("§", ""));
        while (matcher.find()) {
            try {
                if (Long.parseLong(matcher.group(1).replace(",", "")) == expected) return true;
            } catch (NumberFormatException ignored) { }
        }
        return false;
    }
}
