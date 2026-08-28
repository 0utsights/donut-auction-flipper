package net.donutnetwork.client;

import java.time.Duration;

/** Emits a changed failure immediately and identical failures at a bounded cadence. */
final class RepeatedFailureLimiter {
    record Decision(boolean emit, int suppressed) { }
    record Recovery(boolean recovered, int suppressed) { }

    private final long intervalNanos;
    private String lastMessage = "";
    private long lastEmissionAt;
    private boolean hasEmission;
    private int suppressed;

    RepeatedFailureLimiter(Duration interval) {
        if (interval == null || interval.isZero() || interval.isNegative()) throw new IllegalArgumentException("interval must be positive");
        intervalNanos = interval.toNanos();
    }

    synchronized Decision record(String message, long nowNanos) {
        String safe = message == null ? "" : message;
        boolean changed = !safe.equals(lastMessage);
        // Subtraction is the overflow-safe comparison for values returned by
        // System.nanoTime(); the interval is tiny beside its wrap period.
        boolean intervalElapsed = hasEmission && nowNanos - lastEmissionAt >= intervalNanos;
        if (!hasEmission || changed || intervalElapsed) {
            int skipped = suppressed;
            lastMessage = safe;
            lastEmissionAt = nowNanos;
            hasEmission = true;
            suppressed = 0;
            return new Decision(true, skipped);
        }
        if (suppressed < Integer.MAX_VALUE) suppressed++;
        return new Decision(false, 0);
    }

    synchronized Recovery recover() {
        boolean recovered = !lastMessage.isEmpty();
        int skipped = suppressed;
        lastMessage = "";
        lastEmissionAt = 0;
        hasEmission = false;
        suppressed = 0;
        return new Recovery(recovered, skipped);
    }
}
