package net.donutnetwork.client;

import java.util.List;
import java.util.Map;

/** Immutable, mapping-independent projection made by the version-specific Minecraft adapter. */
public record ItemStackView(String itemId, int quantity, String displayName, List<String> lore,
                            Map<String,Integer> enchantments, String seller, String authoritativeId,
                            int durability, int maxDurability, String trimPattern, String trimMaterial,
                            Map<String,String> components) {
    public ItemStackView {
        lore = List.copyOf(lore);
        enchantments = Map.copyOf(enchantments);
        components = Map.copyOf(components);
    }

    public ItemStackView(String itemId, int quantity, String displayName, List<String> lore,
                         Map<String,Integer> enchantments, String seller, String authoritativeId) {
        this(itemId, quantity, displayName, lore, enchantments, seller, authoritativeId,
                0, 0, "", "", Map.of());
    }
}
