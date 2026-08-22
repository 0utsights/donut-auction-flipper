package net.donutnetwork.client;

import net.fabricmc.fabric.api.client.screen.v1.ScreenEvents;
import net.minecraft.client.MinecraftClient;
import net.minecraft.client.gui.screen.ingame.HandledScreen;
import net.minecraft.entity.player.PlayerInventory;
import net.minecraft.item.ItemStack;
import net.minecraft.screen.ScreenHandler;
import net.minecraft.screen.slot.Slot;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;

/** Read-only screen lifecycle adapter. Scans only changed server-owned slots once per screen tick. */
final class FabricAuctionScreenObserver {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private final ClientCore core;

    FabricAuctionScreenObserver(ClientCore core) {
        this.core = core;
    }

    void register() {
        ScreenEvents.AFTER_INIT.register((client, screen, width, height) -> {
            if (!(screen instanceof HandledScreen<?> handled)) {
                return;
            }
            ScreenHandler handler = handled.getScreenHandler();
            int marketSlots = countMarketSlots(client, handler);
            if (!AuctionScreenClassifier.isListingScreen(screen.getTitle().getString(), marketSlots)) {
                return;
            }
            Session session = new Session(handler.syncId);
            LOGGER.info("Observing auction screen title={} syncId={} marketSlots={}",
                    screen.getTitle().getString(), handler.syncId, marketSlots);
            ScreenEvents.afterTick(screen).register(ignored -> session.scan(client, handler));
            ScreenEvents.remove(screen).register(ignored -> core.onScreenClosed(session.prefix));
        });
    }

    private static int countMarketSlots(MinecraftClient client, ScreenHandler handler) {
        if (client.player == null) {
            return 0;
        }
        PlayerInventory playerInventory = client.player.getInventory();
        int count = 0;
        for (Slot slot : handler.slots) {
            if (slot.inventory != playerInventory) {
                count++;
            }
        }
        return count;
    }

    private final class Session {
        private final String prefix;
        private final Map<Integer, MinecraftItemStackAdapter.StackFingerprint> fingerprints = new HashMap<>();
        private final LinkedHashMap<String, Boolean> notified = new LinkedHashMap<>();

        private Session(int syncId) {
            this.prefix = "screen:" + syncId + ":";
        }

        private void scan(MinecraftClient client, ScreenHandler handler) {
            if (client.player == null || handler.syncId < 0) {
                return;
            }
            PlayerInventory playerInventory = client.player.getInventory();
            for (Slot slot : handler.slots) {
                if (slot.inventory == playerInventory || !slot.isEnabled()) {
                    continue;
                }
                ItemStack stack = slot.getStack();
                MinecraftItemStackAdapter.StackFingerprint fingerprint = MinecraftItemStackAdapter.fingerprint(stack);
                MinecraftItemStackAdapter.StackFingerprint previous = fingerprints.put(slot.id, fingerprint);
                if (fingerprint.equals(previous)) {
                    continue;
                }
                String location = prefix + "slot:" + slot.id;
                if (stack.isEmpty()) {
                    core.onSlotCleared(location);
                    continue;
                }
                ItemStackView view = MinecraftItemStackAdapter.project(stack, location);
                core.onSlotChanged(location, view).ifPresent(evaluation -> notify(client, evaluation));
            }
        }

        private void notify(MinecraftClient client, ListingEvaluation evaluation) {
            String notificationKey = evaluation.listing().listingId() + '\0' + evaluation.listing().signature()
                    + '\0' + evaluation.listing().totalPrice() + '\0' + evaluation.listing().seller();
            if (!evaluation.opportunity() || client.player == null || notified.containsKey(notificationKey)) {
                return;
            }
            notified.put(notificationKey, true);
            if (notified.size() > 512) {
                notified.remove(notified.keySet().iterator().next());
            }
            FlipChatNotifier.sendParsed(client, evaluation);
            LOGGER.info("Auction opportunity listing={} signature={} price={} profit={} source={} latencyNs={}",
                    evaluation.listing().listingId(), evaluation.listing().signature(),
                    evaluation.listing().totalPrice(), evaluation.expectedProfit(), evaluation.source(),
                    evaluation.latencyNanos());
        }
    }

}
