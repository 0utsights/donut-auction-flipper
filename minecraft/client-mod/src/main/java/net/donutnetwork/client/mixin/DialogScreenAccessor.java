package net.donutnetwork.client.mixin;

import net.minecraft.client.gui.screen.dialog.DialogScreen;
import net.minecraft.dialog.type.Dialog;
import org.spongepowered.asm.mixin.Mixin;
import org.spongepowered.asm.mixin.gen.Accessor;

@Mixin(DialogScreen.class)
public interface DialogScreenAccessor {
    @Accessor("dialog")
    Dialog donut$getDialog();
}
