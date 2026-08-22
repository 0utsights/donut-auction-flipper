package net.donutnetwork.client;

public interface PurchaseController {
    enum Mode { NOTIFY_ONLY, ASSISTED, SIMULATED, LIVE_INTERACTION }
    record Request(int expectedSyncId,String expectedSignature,long expectedPrice,String expectedSeller,int slot) {}
    record VisibleState(int syncId,String signature,long price,String seller,int slot,boolean confirmationOpen) {}
    record Result(boolean attempted,boolean success,String reason) {}
    Result execute(Request request,VisibleState visible);
    static Result revalidate(Request r,VisibleState v){if(r.expectedSyncId()!=v.syncId())return new Result(false,false,"stale screen sync id");if(r.slot()!=v.slot())return new Result(false,false,"wrong slot");if(!r.expectedSignature().equals(v.signature()))return new Result(false,false,"item mismatch");if(r.expectedPrice()!=v.price())return new Result(false,false,"price changed");if(!r.expectedSeller().isBlank()&&!r.expectedSeller().equals(v.seller()))return new Result(false,false,"seller mismatch");return new Result(true,false,"validated");}
}
