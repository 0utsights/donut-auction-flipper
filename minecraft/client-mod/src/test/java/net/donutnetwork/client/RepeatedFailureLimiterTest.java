package net.donutnetwork.client;

import org.junit.jupiter.api.Test;

import java.time.Duration;

import static org.junit.jupiter.api.Assertions.*;

class RepeatedFailureLimiterTest {
    @Test void emitsFirstChangedAndPeriodicFailuresButSuppressesTheRest() {
        RepeatedFailureLimiter limiter = new RepeatedFailureLimiter(Duration.ofSeconds(30));
        assertTrue(limiter.record("bad candidate", 0).emit());
        assertFalse(limiter.record("bad candidate", 1).emit());
        assertFalse(limiter.record("bad candidate", Duration.ofSeconds(29).toNanos()).emit());
        RepeatedFailureLimiter.Decision periodic = limiter.record("bad candidate", Duration.ofSeconds(30).toNanos());
        assertTrue(periodic.emit());
        assertEquals(2, periodic.suppressed());
        assertTrue(limiter.record("different failure", Duration.ofSeconds(31).toNanos()).emit());
    }

    @Test void reportsRecoveryAndResetsState() {
        RepeatedFailureLimiter limiter = new RepeatedFailureLimiter(Duration.ofSeconds(30));
        limiter.record("bad candidate", 0);
        limiter.record("bad candidate", 1);
        RepeatedFailureLimiter.Recovery recovery = limiter.recover();
        assertTrue(recovery.recovered());
        assertEquals(1, recovery.suppressed());
        assertFalse(limiter.recover().recovered());
        assertTrue(limiter.record("bad candidate", 2).emit());
    }

    @Test void toleratesNanoTimeWrappingWithoutDelayingTheNextEmission() {
        RepeatedFailureLimiter limiter = new RepeatedFailureLimiter(Duration.ofNanos(10));
        assertTrue(limiter.record("bad candidate", Long.MAX_VALUE - 4).emit());
        assertFalse(limiter.record("bad candidate", Long.MAX_VALUE - 1).emit());
        assertTrue(limiter.record("bad candidate", Long.MIN_VALUE + 6).emit());
    }
}
