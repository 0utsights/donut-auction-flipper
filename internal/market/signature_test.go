package market

import "testing"

func TestCanonicalSignatureStable(t *testing.T) {
	a := Item{ID: "Netherite_Sword", Quantity: 1, Enchantments: map[string]int{"minecraft:unbreaking": 3, "minecraft:sharpness": 5}, Components: map[string]string{"ignored": "noise", "donut:rarity": "MYTHIC"}}
	b := Item{ID: "minecraft:netherite_sword", Quantity: 64, Enchantments: map[string]int{"minecraft:sharpness": 5, "minecraft:unbreaking": 3}, Components: map[string]string{"donut:rarity": "mythic"}}
	sa, sb := CanonicalSignature(a), CanonicalSignature(b)
	if sa != sb {
		t.Fatalf("signature depends on ordering or quantity:\n%+v\n%+v", sa, sb)
	}
	want := "minecraft:netherite_sword|minecraft:sharpness=5;minecraft:unbreaking=3;donut:rarity=mythic"
	if sa.Exact != want {
		t.Fatalf("got %q want %q", sa.Exact, want)
	}
}
func TestFingerprintStableAndSensitive(t *testing.T) {
	s := CanonicalSignature(Item{ID: "elytra"})
	a := Fingerprint("", "Seller", s, 10, 1)
	b := Fingerprint("", "seller", s, 10, 1)
	if a != b {
		t.Fatal("seller casing should not change fingerprint")
	}
	if a == Fingerprint("", "seller", s, 11, 1) {
		t.Fatal("price change must change fingerprint")
	}
	if Fingerprint("auction-1", "x", s, 1, 1) != "id:auction-1" {
		t.Fatal("authoritative ID not preferred")
	}
}
func TestDurabilityBuckets(t *testing.T) {
	a := CanonicalSignature(Item{ID: "elytra", Durability: 91, MaxDurability: 100})
	b := CanonicalSignature(Item{ID: "elytra", Durability: 99, MaxDurability: 100})
	c := CanonicalSignature(Item{ID: "elytra", Durability: 89, MaxDurability: 100})
	if a != b {
		t.Fatal("same durability bucket should match")
	}
	if a == c {
		t.Fatal("different durability buckets should differ")
	}
}

func TestEnchantmentNamesNormalizeWithoutLosingLevels(t *testing.T) {
	s := CanonicalSignature(Item{ID: "ELYTRA", Enchantments: map[string]int{"Minecraft:MENDING": 1}})
	if s.Exact != "minecraft:elytra|minecraft:mending=1" {
		t.Fatalf("got %q", s.Exact)
	}
}
