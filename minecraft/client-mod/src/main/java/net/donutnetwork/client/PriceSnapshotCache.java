package net.donutnetwork.client;

import java.time.Duration;
import java.time.Instant;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;

public final class PriceSnapshotCache {
    public record Value(long fairValue,long quickSellValue,int confidenceBps,int volume24h) {}
    public record Snapshot(long version,Instant generatedAt,Map<String,Value> values) { public Snapshot { values=Map.copyOf(values); } }
    private final AtomicReference<Snapshot> current = new AtomicReference<>(new Snapshot(0,Instant.EPOCH,Map.of()));
    private final Duration staleAfter;
    public PriceSnapshotCache(Duration staleAfter){this.staleAfter=staleAfter;}
    public boolean replace(Snapshot next){while(true){Snapshot old=current.get();if(next.version()<=old.version()&&!next.generatedAt().isAfter(old.generatedAt()))return false;if(current.compareAndSet(old,next))return true;}}
    public Value get(String signature){var values=current.get().values();var exact=values.get(signature);if(exact!=null)return exact;int separator=signature.indexOf('|');return separator>0?values.get(signature.substring(0,separator)):null;}
    public boolean stale(Instant now){return current.get().generatedAt().plus(staleAfter).isBefore(now);}
    public long version(){return current.get().version();}
}
