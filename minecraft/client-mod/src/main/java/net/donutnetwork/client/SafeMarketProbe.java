package net.donutnetwork.client;

import net.minecraft.client.MinecraftClient;
import net.minecraft.client.gui.screen.ingame.GenericContainerScreen;
import net.minecraft.component.DataComponentTypes;
import net.minecraft.component.type.LoreComponent;
import net.minecraft.component.type.ContainerComponent;
import net.minecraft.item.ItemStack;
import net.minecraft.registry.Registries;
import net.minecraft.screen.GenericContainerScreenHandler;
import net.minecraft.screen.slot.SlotActionType;
import net.minecraft.text.Text;
import net.minecraft.util.Identifier;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Duration;
import java.time.Instant;

/**
 * One-shot, explicitly configured schema probe. It may open public market
 * screens and the verified Your Orders control, but it never clicks a market
 * row, personal order, claim, purchase, confirmation, or inventory slot.
 */
final class SafeMarketProbe {
    private static final Logger LOGGER = LoggerFactory.getLogger("donut-network-client");
    private static final Duration TIMEOUT = Duration.ofSeconds(12);
    private enum Phase { WAITING, ORDER_BOARD, YOUR_ORDERS, COMPLETED_ORDER_CONTROLS,
        SHULKER_AUCTIONS, SHULKER_PREVIEW, DONE }

    private final boolean enabled;
    private Phase phase = Phase.WAITING;
    private Instant phaseAt;
    private int connectedTicks;

    SafeMarketProbe(boolean enabled) { this.enabled = enabled; }

    boolean exclusive() { return enabled && phase != Phase.DONE; }

    void tick(MinecraftClient client) {
        if (!enabled || phase == Phase.DONE || client.player == null || client.world == null
                || client.getNetworkHandler() == null) return;
        if (phaseAt != null && Duration.between(phaseAt, Instant.now()).compareTo(TIMEOUT) > 0) {
            LOGGER.warn("[DN-PROBE] timed out in {}; no transactional control was clicked", phase);
            close(client);
            phase = Phase.DONE;
            return;
        }
        switch (phase) {
            case WAITING -> {
                if (++connectedTicks < 100 || client.currentScreen != null) return;
                client.getNetworkHandler().sendChatCommand("orders");
                transition(Phase.ORDER_BOARD);
            }
            case ORDER_BOARD -> inspectOrderBoard(client);
            case YOUR_ORDERS -> inspectYourOrders(client);
            case COMPLETED_ORDER_CONTROLS -> inspectCompletedOrderControls(client);
            case SHULKER_AUCTIONS -> inspectShulkerAuctions(client);
            case SHULKER_PREVIEW -> inspectShulkerPreview(client);
            case DONE -> { }
        }
    }

    private void inspectOrderBoard(MinecraftClient client) {
        if (!(client.currentScreen instanceof GenericContainerScreen) || !(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return;
        String title = client.currentScreen.getTitle().getString();
        if (!title.matches("Orders \\(Page [0-9]+\\)")) return;
        LOGGER.info("[DN-PROBE] order board {}", fingerprint(handler));
        ItemStack yourOrders = menuStack(handler, 51);
        if (!OrderPlan.normalizeLabel(yourOrders.getName().getString()).equals("your orders")) {
            LOGGER.warn("[DN-PROBE] Your Orders control was not exact; stopping without a click");
            close(client); phase = Phase.DONE; return;
        }
        client.interactionManager.clickSlot(handler.syncId, 51, 0, SlotActionType.PICKUP, client.player);
        transition(Phase.YOUR_ORDERS);
    }

    private void inspectYourOrders(MinecraftClient client) {
        if (!(client.currentScreen instanceof GenericContainerScreen) || !(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return;
        String title = client.currentScreen.getTitle().getString();
        if (!title.equals("Orders -> Your Orders")) return;
        LOGGER.info("[DN-PROBE] your orders {}", fingerprint(handler));
        java.util.List<Integer> completed = new java.util.ArrayList<>();
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        for (int slot = 0; slot < limit; slot++) {
            ItemStack stack = handler.getSlot(slot).getStack();
            if (!stack.isEmpty() && stackText(stack).contains("Order Completed")) completed.add(slot);
        }
        if (completed.size() != 1) {
            LOGGER.warn("[DN-PROBE] expected one completed order control, found {}; not clicking a row", completed.size());
            openShulkerAuctions(client);
            return;
        }
        client.interactionManager.clickSlot(handler.syncId, completed.getFirst(), 0, SlotActionType.PICKUP, client.player);
        transition(Phase.COMPLETED_ORDER_CONTROLS);
    }

    private void inspectCompletedOrderControls(MinecraftClient client) {
        if (!(client.currentScreen instanceof GenericContainerScreen)
                || !(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return;
        if (!client.currentScreen.getTitle().getString().equals("Orders -> Edit Order")) return;
        LOGGER.info("[DN-PROBE] completed order controls title=[{}] {}", safe(client.currentScreen.getTitle().getString()), fingerprint(handler));
        openShulkerAuctions(client);
    }

    private void openShulkerAuctions(MinecraftClient client) {
        close(client);
        client.getNetworkHandler().sendChatCommand("auction shulker_box");
        transition(Phase.SHULKER_AUCTIONS);
    }

    private void inspectShulkerAuctions(MinecraftClient client) {
        if (!(client.currentScreen instanceof GenericContainerScreen) || !(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return;
        String title = client.currentScreen.getTitle().getString();
        if (!title.matches("Auction \\(Page [0-9]+\\)")) return;
        LOGGER.info("[DN-PROBE] shulker auction title=[{}] {}", safe(title), fingerprint(handler));
        ItemStack first = menuStack(handler, 0);
        if (!Registries.ITEM.getId(first.getItem()).toString().equals("minecraft:shulker_box")
                || !stackText(first).contains("Right-Click to preview")) {
            LOGGER.warn("[DN-PROBE] first shulker row was not a verified preview control; stopping without a click");
            close(client); phase = Phase.DONE; return;
        }
        client.interactionManager.clickSlot(handler.syncId, 0, 1, SlotActionType.PICKUP, client.player);
        transition(Phase.SHULKER_PREVIEW);
    }

    private void inspectShulkerPreview(MinecraftClient client) {
        if (!(client.currentScreen instanceof GenericContainerScreen)
                || !(client.player.currentScreenHandler instanceof GenericContainerScreenHandler handler)) return;
        String title = client.currentScreen.getTitle().getString();
        if (title.matches("Auction \\(Page [0-9]+\\)")) return;
        LOGGER.info("[DN-PROBE] shulker preview title=[{}] {}", safe(title), fingerprint(handler));
        close(client);
        phase = Phase.DONE;
        LOGGER.info("[DN-PROBE] capture complete; market_probe can be returned to false");
    }

    private static ItemStack menuStack(GenericContainerScreenHandler handler, int slot) {
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        return slot >= 0 && slot < limit ? handler.getSlot(slot).getStack() : ItemStack.EMPTY;
    }

    private static String fingerprint(GenericContainerScreenHandler handler) {
        StringBuilder result = new StringBuilder("menu_slots=").append(handler.getInventory().size());
        int limit = Math.min(handler.getInventory().size(), handler.slots.size());
        for (int slot = 0; slot < limit && result.length() < 12_000; slot++) {
            ItemStack stack = handler.getSlot(slot).getStack();
            if (stack.isEmpty()) continue;
            Identifier id = Registries.ITEM.getId(stack.getItem());
            result.append(" | ").append(slot).append('=').append(id).append('x').append(stack.getCount())
                    .append(" name=[").append(safe(stack.getName().getString())).append(']');
            LoreComponent lore = stack.get(DataComponentTypes.LORE);
            if (lore != null && !lore.lines().isEmpty()) {
                result.append(" lore=[");
                for (Text line : lore.lines()) result.append(safe(line.getString())).append(" / ");
                result.append(']');
            }
            ContainerComponent container = stack.get(DataComponentTypes.CONTAINER);
            if (container != null) result.append(" container_items=").append(container.streamNonEmpty().count());
            result.append(" components=").append(stack.getComponentChanges().size());
        }
        return result.toString();
    }

    private static String stackText(ItemStack stack) {
        StringBuilder value = new StringBuilder(stack.getName().getString());
        LoreComponent lore = stack.get(DataComponentTypes.LORE);
        if (lore != null) for (Text line : lore.lines()) value.append(' ').append(line.getString());
        return value.toString();
    }

    private void transition(Phase next) { phase = next; phaseAt = Instant.now(); }
    private static void close(MinecraftClient client) {
        if (client.player != null && client.currentScreen != null) client.player.closeHandledScreen();
    }
    private static String safe(String value) {
        if (value == null) return "";
        value = value.replace('\r', ' ').replace('\n', ' ').replace('§', ' ').strip();
        return value.substring(0, Math.min(300, value.length()));
    }
}
