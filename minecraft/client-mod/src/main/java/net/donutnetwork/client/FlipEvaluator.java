package net.donutnetwork.client;

public final class FlipEvaluator {
    public record Thresholds(long minProfit,int minMarginBps,int minConfidenceBps,long maxPurchasePrice,int minVolume24h) {}
    public record Decision(boolean flip,long profit,int marginBps,long latencyNanos,String reason) {}
    private final PriceSnapshotCache cache; private final Thresholds thresholds;
    public FlipEvaluator(PriceSnapshotCache cache,Thresholds thresholds){this.cache=cache;this.thresholds=thresholds;}
    public Decision evaluate(ParsedListing listing,long observedNanos){PriceSnapshotCache.Value value=cache.get(listing.signature());if(value==null)return no(observedNanos,"no valuation");if(value.confidenceBps()<thresholds.minConfidenceBps())return no(observedNanos,"low confidence");if(value.volume24h()<thresholds.minVolume24h())return no(observedNanos,"illiquid");if(listing.totalPrice()>thresholds.maxPurchasePrice())return no(observedNanos,"price cap");long profit;try{long resale=Math.multiplyExact(value.quickSellValue(),Math.max(1,listing.quantity()));profit=Math.subtractExact(resale,listing.totalPrice());}catch(ArithmeticException overflow){return no(observedNanos,"price overflow");}int margin=ratioBps(profit,listing.totalPrice());boolean flip=profit>=thresholds.minProfit()&&margin>=thresholds.minMarginBps();return new Decision(flip,profit,margin,System.nanoTime()-observedNanos,flip?"thresholds met":"insufficient edge");}
    private static int ratioBps(long numerator,long denominator){if(denominator<=0)return 0;double ratio=(double)numerator/(double)denominator*10_000.0;if(ratio>=Integer.MAX_VALUE)return Integer.MAX_VALUE;if(ratio<=Integer.MIN_VALUE)return Integer.MIN_VALUE;return(int)ratio;}
    private static Decision no(long started,String reason){return new Decision(false,0,0,System.nanoTime()-started,reason);}
}
