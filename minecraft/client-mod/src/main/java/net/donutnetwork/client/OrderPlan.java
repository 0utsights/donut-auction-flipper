package net.donutnetwork.client;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.text.Normalizer;
import java.util.Locale;
import java.util.Optional;
import java.util.OptionalLong;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Immutable transaction values captured when manual or session-scoped consent arms one order. */
record OrderPlan(String candidateId, String signature, String itemId, String itemName, int batchQuantity, int batches,
                 int quantity, long observedUnitRewardCents, long unitRewardCents, long competitiveUnitRewardCents,
                 long bidStepCents, long totalCents, long escrowDollars, long targetListPrice,
                 long expectedProceedsPerBatch) {
    record OrderProgress(int delivered, int total) {}
    private static final Pattern MONEY = Pattern.compile("(?i)\\$?\\s*([0-9][0-9,]*(?:\\.[0-9]+)?)\\s*([KMBT]?)(?![A-Za-z])");
    private static final Pattern UNIT_MONEY = Pattern.compile("(?i)\\$\\s*([0-9][0-9,]*(?:\\.[0-9]+)?)\\s*([KMBT]?)\\s*(?:each|per item|chacun|par (?:objet|article))\\b");
    private static final Pattern LABELED_QUANTITY = Pattern.compile("(?i)\\b(?:amount|quantity|quantit[eé])\\s*:?\\s*([0-9][0-9,]*)\\b");
    private static final Pattern DELIVERED = Pattern.compile("(?i)([0-9][0-9,]*(?:\\.[0-9]+)?)\\s*([KMBT]?)\\s*/\\s*"
            + "([0-9][0-9,]*(?:\\.[0-9]+)?)\\s*([KMBT]?)\\s+(?:delivered|livr[eé]s?)\\b");
    private static final Pattern DONUT_ITEM_RESULT = Pattern.compile(
            "(?i)^\\[(?:item|block)/([a-z0-9_./-]+)(?:@[a-z0-9_.:/-]+)?]\\s+(.+)$");

    static OrderPlan from(CandidateFeedClient.Candidate candidate) { return from(candidate, 1); }

    static OrderPlan from(PortfolioAllocator.Selection selection) {
        if (selection == null) throw new IllegalArgumentException("portfolio selection is missing");
        return from(selection.candidate(), selection.batches());
    }

    static OrderPlan from(CandidateFeedClient.Candidate candidate, int batches) {
        if (!"ORDER_TO_AUCTION".equals(candidate.route()) || !"READY".equals(candidate.state())) {
            throw new IllegalArgumentException("candidate is not a ready order-to-auction trade");
        }
        if (candidate.quantity() <= 0 || candidate.observedOrderUnitRewardCents() <= 0
                || candidate.orderUnitRewardCents() <= candidate.observedOrderUnitRewardCents()
                || candidate.acquisitionCost() <= 0) {
            throw new IllegalArgumentException("candidate has invalid order economics");
        }
        if (batches <= 0 || batches > candidate.executableBatches()) {
            throw new IllegalArgumentException("selected stack count exceeds conservative executable volume");
        }
        int quantity = Math.multiplyExact(candidate.quantity(), batches);
        long safeBatchCents = Math.multiplyExact(candidate.orderUnitRewardCents(), candidate.quantity());
        if (centsToDollarsCeiling(safeBatchCents) != candidate.acquisitionCost()) {
            throw new IllegalArgumentException("candidate escrow does not match its competitive price and batch quantity");
        }
        long initialUnitReward = Math.addExact(candidate.observedOrderUnitRewardCents(), 1);
        if (initialUnitReward > candidate.orderUnitRewardCents()) initialUnitReward = candidate.orderUnitRewardCents();
        long totalCents = Math.multiplyExact(initialUnitReward, quantity);
        return new OrderPlan(candidate.id(), candidate.signature(), candidate.itemId(), candidate.itemName(),
                candidate.quantity(), batches, quantity, candidate.observedOrderUnitRewardCents(), initialUnitReward,
                candidate.orderUnitRewardCents(), candidate.orderUnitRewardCents() - candidate.observedOrderUnitRewardCents(),
                totalCents, centsToDollarsCeiling(totalCents), candidate.targetListPrice(), candidate.expectedProceeds());
    }

    boolean matches(CandidateFeedClient.Candidate candidate) {
        return candidate != null && "READY".equals(candidate.state()) && "ORDER_TO_AUCTION".equals(candidate.route())
                && candidateId.equals(candidate.id()) && signature.equals(candidate.signature()) && itemId.equals(candidate.itemId())
                && batchQuantity == candidate.quantity() && batches <= candidate.executableBatches()
                && quantity == Math.multiplyExact(candidate.quantity(), batches)
                && observedUnitRewardCents == candidate.observedOrderUnitRewardCents()
                && competitiveUnitRewardCents == candidate.orderUnitRewardCents()
                && unitRewardCents > observedUnitRewardCents && unitRewardCents <= competitiveUnitRewardCents
                && candidate.acquisitionCost() == centsToDollarsCeiling(Math.multiplyExact(competitiveUnitRewardCents, batchQuantity))
                && targetListPrice == candidate.targetListPrice() && expectedProceedsPerBatch == candidate.expectedProceeds();
    }

    OptionalLong nextUnitReward(long escrowCapDollars, long minimumProfitDollars) {
        long next;
        try {
            next = unitRewardCents < competitiveUnitRewardCents
                    ? competitiveUnitRewardCents : Math.addExact(unitRewardCents, bidStepCents);
            long nextEscrow = centsToDollarsCeiling(Math.multiplyExact(next, quantity));
            long proceeds = Math.multiplyExact(expectedProceedsPerBatch, batches);
            if (nextEscrow > escrowCapDollars || proceeds - nextEscrow < minimumProfitDollars) return OptionalLong.empty();
        } catch (ArithmeticException error) {
            return OptionalLong.empty();
        }
        return OptionalLong.of(next);
    }

    OrderPlan withUnitReward(long nextUnitRewardCents) {
        if (nextUnitRewardCents <= unitRewardCents) throw new IllegalArgumentException("replacement bid must increase");
        long nextTotal = Math.multiplyExact(nextUnitRewardCents, quantity);
        return new OrderPlan(candidateId, signature, itemId, itemName, batchQuantity, batches, quantity,
                observedUnitRewardCents, nextUnitRewardCents, competitiveUnitRewardCents, bidStepCents,
                nextTotal, centsToDollarsCeiling(nextTotal), targetListPrice, expectedProceedsPerBatch);
    }

    long conservativeProfitDollars() {
        try { return Math.subtractExact(Math.multiplyExact(expectedProceedsPerBatch, batches), escrowDollars); }
        catch (ArithmeticException error) { return Long.MIN_VALUE; }
    }

    String priceInput() {
        long dollars = unitRewardCents / 100;
        long cents = unitRewardCents % 100;
        return cents == 0 ? Long.toString(dollars) : dollars + "." + String.format(Locale.ROOT, "%02d", cents);
    }

    String itemPathQuery() {
        int separator = itemId.indexOf(':');
        return separator < 0 ? itemId : itemId.substring(separator + 1);
    }

    static String normalizeLabel(String value) {
        return value == null ? "" : Normalizer.normalize(value.replace("§", ""), Normalizer.Form.NFKD)
                .replaceAll("\\p{M}+", "").toLowerCase(Locale.ROOT)
                .replaceAll("[^a-z0-9]+", " ").strip();
    }

    static boolean equivalentItemLabel(String actual, String expectedRegistryName, String fallbackName) {
        String normalized = normalizeLabel(actual);
        if (normalized.equals(normalizeLabel(expectedRegistryName)) || normalized.equals(normalizeLabel(fallbackName))) return true;
        return normalizedTokenSet(normalized).equals(normalizedTokenSet(normalizeLabel(expectedRegistryName)));
    }

    static boolean exactItemResultLabel(String actual, String expectedItemId, String expectedRegistryName, String fallbackName) {
        Matcher donut = DONUT_ITEM_RESULT.matcher(actual == null ? "" : actual.strip());
        if (!donut.matches()) return equivalentItemLabel(actual, expectedRegistryName, fallbackName);
        int separator = expectedItemId == null ? -1 : expectedItemId.indexOf(':');
        String expectedPath = separator < 0 ? expectedItemId : expectedItemId.substring(separator + 1);
        if (expectedPath == null) return false;
        String displayedPath = donut.group(1);
        if (displayedPath.equalsIgnoreCase(expectedPath)) return true;

        // Captured 1.21.11 Donut fixture:
        // [item/golden_apple@items] Enchanted Golden Apple
        // The bracket path selects the button's icon in this one menu and is
        // not the semantic item ID. Accept the alias only when the complete
        // visible label independently proves the expected item. Do not make
        // this a generic path-mismatch fallback: that would reintroduce fuzzy
        // selection bugs such as Ice versus Packed Ice.
        return expectedPath.equals("enchanted_golden_apple")
                && displayedPath.equalsIgnoreCase("golden_apple")
                && equivalentItemLabel(donut.group(2), expectedRegistryName, fallbackName);
    }

    private static String normalizedTokenSet(String value) {
        return value.replace(" of ", " ").lines().flatMap(line -> Pattern.compile(" +").splitAsStream(line))
                .filter(token -> !token.isBlank()).sorted().reduce((left, right) -> left + " " + right).orElse("");
    }

    static boolean textContainsMoney(String text, long expectedCents) {
        String cleaned = clean(text);
        Matcher matcher = MONEY.matcher(cleaned);
        while (matcher.find()) {
            if (!isMoneyContext(cleaned, matcher)) continue;
            if (moneyValueMatches(matcher.group(1), matcher.group(2), expectedCents)) return true;
        }
        return false;
    }

    static boolean textContainsUnitReward(String text, long expectedCents) {
        Matcher matcher = UNIT_MONEY.matcher(clean(text));
        while (matcher.find()) if (moneyValueMatches(matcher.group(1), matcher.group(2), expectedCents)) return true;
        return false;
    }

    static OptionalLong firstUnitRewardCents(String text) {
        Matcher matcher = UNIT_MONEY.matcher(clean(text));
        while (matcher.find()) {
            try {
                BigDecimal value = new BigDecimal(matcher.group(1).replace(",", "")).multiply(multiplier(matcher.group(2)))
                        .multiply(BigDecimal.valueOf(100));
                return OptionalLong.of(value.setScale(0, RoundingMode.UNNECESSARY).longValueExact());
            } catch (ArithmeticException | NumberFormatException ignored) { }
        }
        return OptionalLong.empty();
    }

    private static boolean moneyValueMatches(String raw, String suffix, long expectedCents) {
        BigDecimal displayed = new BigDecimal(raw.replace(",", ""));
        BigDecimal multiplier = multiplier(suffix);
        BigDecimal lowerCents = displayed.multiply(multiplier).multiply(BigDecimal.valueOf(100));
        if (suffix == null || suffix.isEmpty()) {
            try { return lowerCents.setScale(0, RoundingMode.UNNECESSARY).longValueExact() == expectedCents; }
            catch (ArithmeticException ignored) { return false; }
        }
        BigDecimal widthCents = multiplier.movePointLeft(displayed.scale()).multiply(BigDecimal.valueOf(100));
        BigDecimal expected = BigDecimal.valueOf(expectedCents);
        return expected.compareTo(lowerCents) >= 0 && expected.compareTo(lowerCents.add(widthCents)) < 0;
    }

    static boolean textContainsQuantity(String text, int expected) {
        Matcher matcher = LABELED_QUANTITY.matcher(clean(text));
        while (matcher.find()) {
            try {
                if (Long.parseLong(matcher.group(1).replace(",", "")) == expected) return true;
            } catch (NumberFormatException ignored) { }
        }
        return false;
    }

    static boolean textContainsOrderProgress(String text, int expectedTotal, boolean requireZeroDelivered) {
        Optional<OrderProgress> progress = firstOrderProgress(text);
        return progress.isPresent() && progress.get().total() == expectedTotal
                && (!requireZeroDelivered || progress.get().delivered() == 0);
    }

    static Optional<OrderProgress> firstOrderProgress(String text) {
        Matcher matcher = DELIVERED.matcher(clean(text));
        while (matcher.find()) {
            try {
                long delivered = scaledWhole(matcher.group(1), matcher.group(2));
                long total = scaledWhole(matcher.group(3), matcher.group(4));
                if (delivered >= 0 && delivered <= total && total > 0 && total <= Integer.MAX_VALUE) {
                    return Optional.of(new OrderProgress(Math.toIntExact(delivered), Math.toIntExact(total)));
                }
            } catch (ArithmeticException | NumberFormatException ignored) { }
        }
        return Optional.empty();
    }

    private static long scaledWhole(String raw, String suffix) {
        return new BigDecimal(raw.replace(",", "")).multiply(multiplier(suffix))
                .setScale(0, RoundingMode.UNNECESSARY).longValueExact();
    }

    private static BigDecimal multiplier(String suffix) {
        return switch (suffix == null ? "" : suffix.toUpperCase(Locale.ROOT)) {
            case "K" -> BigDecimal.valueOf(1_000);
            case "M" -> BigDecimal.valueOf(1_000_000);
            case "B" -> BigDecimal.valueOf(1_000_000_000L);
            case "T" -> BigDecimal.valueOf(1_000_000_000_000L);
            default -> BigDecimal.ONE;
        };
    }

    private static long centsToDollarsCeiling(long cents) { return Math.addExact(cents, 99) / 100; }

    private static boolean isMoneyContext(String text, Matcher matcher) {
        if (matcher.group().stripLeading().startsWith("$")) return true;
        String prefix = text.substring(Math.max(0, matcher.start() - 28), matcher.start()).toLowerCase(Locale.ROOT);
        return prefix.matches("(?s).*(?:price|reward|paying|total|cost|prix|r[eé]compense|co[uû]t)\\s*[:=\\-]?\\s*$");
    }

    private static String clean(String value) {
        return value == null ? "" : value.replaceAll("§[0-9A-FK-ORa-fk-or]", "").replace("§", "");
    }
}
