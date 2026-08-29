package net.donutnetwork.client;

import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import net.fabricmc.loader.api.FabricLoader;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.time.Instant;
import java.util.Collection;
import java.util.LinkedHashMap;
import java.util.Map;

/** Small atomic JSON store; rejects malformed state instead of guessing transaction data. */
final class OrderPositionStore {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final int MAX_BYTES = 1 << 20;
    private final Path path;

    OrderPositionStore(Path path) { this.path = path.toAbsolutePath().normalize(); }
    static OrderPositionStore inConfigDirectory() {
        return new OrderPositionStore(FabricLoader.getInstance().getConfigDir().resolve("donut-network-positions.json"));
    }

    Map<String, LocalOrderPosition> load() {
        if (!Files.exists(path)) return Map.of();
        try {
            byte[] encoded = Files.readAllBytes(path);
            if (encoded.length > MAX_BYTES) throw new IllegalArgumentException("position store exceeds 1 MiB");
            JsonArray array = JsonParser.parseString(new String(encoded, StandardCharsets.UTF_8)).getAsJsonArray();
            if (array.size() > 20) throw new IllegalArgumentException("position store exceeds 20 order slots");
            Map<String, LocalOrderPosition> result = new LinkedHashMap<>();
            for (JsonElement element : array) {
                LocalOrderPosition position = decode(element.getAsJsonObject());
                if (result.putIfAbsent(position.itemId(), position) != null) {
                    throw new IllegalArgumentException("position store contains a duplicate item");
                }
            }
            return Map.copyOf(result);
        } catch (RuntimeException | IOException error) {
            LOGGER.warn("Could not load local order positions from {}: {}", path, safe(error));
            return Map.of();
        }
    }

    boolean save(Collection<LocalOrderPosition> positions) {
        if (positions == null || positions.size() > 20) throw new IllegalArgumentException("invalid position count");
        JsonArray array = new JsonArray();
        positions.stream().sorted(java.util.Comparator.comparing(LocalOrderPosition::itemId))
                .map(OrderPositionStore::encode).forEach(array::add);
        byte[] encoded = array.toString().getBytes(StandardCharsets.UTF_8);
        if (encoded.length > MAX_BYTES) throw new IllegalArgumentException("encoded positions exceed 1 MiB");
        try {
            Files.createDirectories(path.getParent());
            Path temporary = path.resolveSibling(path.getFileName() + ".tmp");
            Files.write(temporary, encoded);
            try {
                Files.move(temporary, path, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
            } catch (IOException unsupportedAtomicMove) {
                Files.move(temporary, path, StandardCopyOption.REPLACE_EXISTING);
            }
            return true;
        } catch (IOException error) {
            LOGGER.warn("Could not save local order positions to {}: {}", path, safe(error));
            return false;
        }
    }

    private static JsonObject encode(LocalOrderPosition value) {
        JsonObject object = new JsonObject();
        object.addProperty("candidate_id", value.candidateId()); object.addProperty("signature", value.signature());
        object.addProperty("item_id", value.itemId()); object.addProperty("item_name", value.itemName());
        object.addProperty("batch_quantity", value.batchQuantity()); object.addProperty("max_stack_size", value.maxStackSize());
        object.addProperty("batches", value.batches()); object.addProperty("total_quantity", value.totalQuantity());
        object.addProperty("unit_reward_cents", value.unitRewardCents()); object.addProperty("escrow_dollars", value.escrowDollars());
        object.addProperty("target_list_price", value.targetListPrice());
        object.addProperty("expected_proceeds_per_batch", value.expectedProceedsPerBatch());
        object.addProperty("delivered_quantity", value.deliveredQuantity());
        object.addProperty("claimed_quantity", value.claimedQuantity());
        object.addProperty("packaged_quantity", value.packagedQuantity());
        object.addProperty("listed_quantity", value.listedQuantity());
        object.addProperty("state", value.state().name());
        object.addProperty("created_at", value.createdAt().toString()); object.addProperty("updated_at", value.updatedAt().toString());
        return object;
    }

    private static LocalOrderPosition decode(JsonObject value) {
        return new LocalOrderPosition(text(value, "candidate_id"), text(value, "signature"), text(value, "item_id"),
                text(value, "item_name"), number(value, "batch_quantity"), number(value, "max_stack_size"),
                number(value, "batches"), number(value, "total_quantity"), longNumber(value, "unit_reward_cents"),
                longNumber(value, "escrow_dollars"), longNumber(value, "target_list_price"),
                longNumber(value, "expected_proceeds_per_batch"), number(value, "delivered_quantity"),
                optionalNumber(value, "claimed_quantity"), optionalNumber(value, "packaged_quantity"),
                optionalNumber(value, "listed_quantity"),
                LocalOrderPosition.State.valueOf(text(value, "state")), Instant.parse(text(value, "created_at")),
                Instant.parse(text(value, "updated_at")));
    }

    private static String text(JsonObject value, String key) { return value.get(key).getAsString(); }
    private static int number(JsonObject value, String key) { return Math.toIntExact(longNumber(value, key)); }
    private static int optionalNumber(JsonObject value, String key) {
        return value.has(key) ? number(value, key) : 0;
    }
    private static long longNumber(JsonObject value, String key) { return value.get(key).getAsLong(); }
    private static String safe(Exception error) {
        String message = error.getMessage() == null ? error.getClass().getSimpleName() : error.getMessage();
        return message.replace('\r', ' ').replace('\n', ' ').substring(0, Math.min(200, message.length()));
    }
}
