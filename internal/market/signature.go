package market

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"unicode"
)

var relevantComponents = map[string]bool{
	"donut:rarity": true, "minecraft:custom_model_data": true,
	"minecraft:dyed_color": true, "donut:level": true,
}

func CanonicalSignature(item Item) Signature {
	return canonicalSignature(item, 0)
}

func canonicalSignature(item Item, depth int) Signature {
	base := strings.ToLower(strings.TrimSpace(item.ID))
	if !strings.Contains(base, ":") {
		base = "minecraft:" + base
	}
	parts := make([]string, 0, len(item.Enchantments)+5)
	type enchantment struct {
		name  string
		level int
	}
	enchantments := make([]enchantment, 0, len(item.Enchantments))
	for k, level := range item.Enchantments {
		enchantments = append(enchantments, enchantment{strings.ToLower(strings.TrimSpace(k)), level})
	}
	sort.Slice(enchantments, func(i, j int) bool { return enchantments[i].name < enchantments[j].name })
	for _, enchant := range enchantments {
		parts = append(parts, fmt.Sprintf("%s=%d", enchant.name, enchant.level))
	}
	if item.TrimPattern != "" {
		parts = append(parts, "trim="+normalizeText(item.TrimPattern)+":"+normalizeText(item.TrimMaterial))
	}
	if item.DisplayName != "" {
		parts = append(parts, "name="+normalizeText(item.DisplayName))
	}
	if item.Durability > 0 && item.MaxDurability > 0 {
		bucket := item.Durability * 10 / item.MaxDurability
		parts = append(parts, fmt.Sprintf("durability=%d", bucket))
	}
	componentKeys := make([]string, 0)
	for k := range item.Components {
		if relevantComponents[k] {
			componentKeys = append(componentKeys, k)
		}
	}
	sort.Strings(componentKeys)
	for _, k := range componentKeys {
		parts = append(parts, normalizeText(k)+"="+normalizeText(item.Components[k]))
	}
	if len(item.Contents) > 0 {
		limit := min(len(item.Contents), 128)
		contents := make([]string, 0, limit+1)
		for _, child := range item.Contents[:limit] {
			quantity := child.Quantity
			if quantity <= 0 {
				quantity = 1
			}
			if depth >= 3 {
				contents = append(contents, fmt.Sprintf("%dx%s", quantity, strings.ToLower(strings.TrimSpace(child.ID))))
			} else {
				contents = append(contents, fmt.Sprintf("%dx%s", quantity, canonicalSignature(child, depth+1).Exact))
			}
		}
		if len(item.Contents) > limit {
			contents = append(contents, fmt.Sprintf("truncated=%d", len(item.Contents)-limit))
		}
		sort.Strings(contents)
		parts = append(parts, "contents="+strings.Join(contents, ","))
	}
	mods := strings.Join(parts, ";")
	exact := base
	if mods != "" {
		exact += "|" + mods
	}
	return Signature{Exact: exact, Base: base, Modifiers: mods}
}

func normalizeText(s string) string {
	return strings.Trim(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ':' || r == '_' || r == '-' {
			return unicode.ToLower(r)
		}
		if unicode.IsSpace(r) {
			return '_'
		}
		return -1
	}, s), "_")
}

func Fingerprint(authoritativeID, seller string, signature Signature, totalPrice int64, quantity int) string {
	if authoritativeID != "" {
		return "id:" + authoritativeID
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d", strings.ToLower(seller), signature.Exact, totalPrice, quantity)
	return fmt.Sprintf("f:%016x", h.Sum64())
}

func NormalizeListing(l Listing) Listing {
	if l.Item.Quantity <= 0 {
		l.Item.Quantity = 1
	}
	l.Signature = CanonicalSignature(l.Item)
	l.UnitPrice = l.TotalPrice / int64(l.Item.Quantity)
	if l.Fingerprint == "" {
		l.Fingerprint = Fingerprint(l.AuthoritativeID, l.SellerUUID+"/"+l.SellerName, l.Signature, l.TotalPrice, l.Item.Quantity)
	}
	return l
}

func NormalizeTransaction(t Transaction) Transaction {
	if t.Item.Quantity <= 0 {
		t.Item.Quantity = 1
	}
	t.Signature = CanonicalSignature(t.Item)
	t.UnitPrice = t.TotalPrice / int64(t.Item.Quantity)
	if t.Fingerprint == "" {
		t.Fingerprint = Fingerprint("", t.SellerUUID+"/"+t.SellerName, t.Signature, t.TotalPrice, t.Item.Quantity)
	}
	return t
}
