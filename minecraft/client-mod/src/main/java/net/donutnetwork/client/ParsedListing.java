package net.donutnetwork.client;

public record ParsedListing(String listingId, String signature, long totalPrice, int quantity, String seller) {
    public long unitPrice() {
        return totalPrice / Math.max(1, quantity);
    }

    public String baseSignature() {
        int separator = signature.indexOf('|');
        return separator < 0 ? signature : signature.substring(0, separator);
    }
}
