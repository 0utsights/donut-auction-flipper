package net.donutnetwork.client;

import java.util.List;
import java.util.Locale;
import java.util.Map;

/** Coarse end-to-end probe for regressions; use JMH before making hardware-level claims. */
public final class AuctionPipelineBenchmark {
    private AuctionPipelineBenchmark() {}

    public static void main(String[] args) {
        ClientCore core = new ClientCore();
        for (int slot = 0; slot < 8; slot++) {
            core.onSlotChanged("baseline:" + slot, item(100_000_000L + slot * 1_000_000L));
        }
        int warmup = 20_000;
        int iterations = 200_000;
        for (int i = 0; i < warmup; i++) {
            core.onSlotChanged("candidate", item(70_000_000L + i % 10_000));
        }
        long started = System.nanoTime();
        for (int i = 0; i < iterations; i++) {
            core.onSlotChanged("candidate", item(70_000_000L + i % 10_000));
        }
        long elapsed = System.nanoTime() - started;
        double nanosPerOperation = (double) elapsed / iterations;
        double operationsPerSecond = 1_000_000_000.0 / nanosPerOperation;
        System.out.printf(Locale.ROOT, "auction_pipeline ns/op=%.1f ops/sec=%.0f iterations=%d%n",
                nanosPerOperation, operationsPerSecond, iterations);
    }

    private static ItemStackView item(long price) {
        return new ItemStackView("minecraft:elytra", 1, "",
                List.of("Price: $" + price, "Seller: benchmark"), Map.of(), "", "");
    }
}
