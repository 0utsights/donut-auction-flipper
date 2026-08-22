package net.donutnetwork.client;

import net.minecraft.component.DataComponentTypes;
import net.minecraft.component.type.CustomModelDataComponent;
import net.minecraft.component.type.DyedColorComponent;
import net.minecraft.component.type.ItemEnchantmentsComponent;
import net.minecraft.component.type.LoreComponent;
import net.minecraft.item.ItemStack;
import net.minecraft.item.equipment.trim.ArmorTrim;
import net.minecraft.registry.Registries;
import net.minecraft.text.Text;

import java.util.HashMap;
import java.util.List;
import java.util.Map;

/** Yarn/Fabric 1.21.11 projection into the mapping-independent parser input. */
final class MinecraftItemStackAdapter {
    record StackFingerprint(String itemId, int count, int componentsHash) {
        private static final StackFingerprint EMPTY = new StackFingerprint("", 0, 0);
    }

    private MinecraftItemStackAdapter() {}

    static ItemStackView project(ItemStack stack, String fallbackListingId) {
        LoreComponent loreComponent = stack.get(DataComponentTypes.LORE);
        List<String> lore = loreComponent == null ? List.of()
                : loreComponent.lines().stream().map(Text::getString).toList();
        Map<String, Integer> enchantments = enchantments(stack.getEnchantments());
        String customName = stack.contains(DataComponentTypes.CUSTOM_NAME) ? stack.getName().getString() : "";
        int maxDurability = stack.isDamageable() ? stack.getMaxDamage() : 0;
        int durability = maxDurability == 0 ? 0 : Math.max(0, maxDurability - stack.getDamage());

        ArmorTrim trim = stack.get(DataComponentTypes.TRIM);
        String trimPattern = trim == null ? "" : trim.pattern().getKey()
                .map(key -> key.getValue().getPath()).orElse("");
        String trimMaterial = trim == null ? "" : trim.material().getKey()
                .map(key -> key.getValue().getPath()).orElse("");

        Map<String, String> components = new HashMap<>();
        CustomModelDataComponent model = stack.get(DataComponentTypes.CUSTOM_MODEL_DATA);
        if (model != null) {
            components.put("minecraft:custom_model_data", model.toString());
        }
        DyedColorComponent color = stack.get(DataComponentTypes.DYED_COLOR);
        if (color != null) {
            components.put("minecraft:dyed_color", Integer.toString(color.rgb()));
        }
        return new ItemStackView(Registries.ITEM.getId(stack.getItem()).toString(), stack.getCount(), customName,
                lore, enchantments, "", fallbackListingId, durability, maxDurability,
                trimPattern, trimMaterial, components);
    }

    static StackFingerprint fingerprint(ItemStack stack) {
        if (stack.isEmpty()) {
            return StackFingerprint.EMPTY;
        }
        return new StackFingerprint(Registries.ITEM.getId(stack.getItem()).toString(), stack.getCount(),
                stack.getComponents().hashCode());
    }

    private static Map<String, Integer> enchantments(ItemEnchantmentsComponent component) {
        Map<String, Integer> result = new HashMap<>();
        for (var entry : component.getEnchantmentEntries()) {
            result.put(entry.getKey().getIdAsString(), entry.getIntValue());
        }
        return result;
    }
}
