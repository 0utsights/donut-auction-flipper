import { createHash } from 'node:crypto'
import { simplify } from 'prismarine-nbt'
import type { ItemView, ParsedOrder } from './types.js'

const moneyPatterns = [
  /\$\s*([0-9][0-9,]*(?:\.[0-9]+)?)\s*([kmbt])?\s*(?:each|per item)\b/i,
  /(?:reward|unit reward|price|paying)\s*[:=-]?\s*\$?\s*([0-9][0-9,]*(?:\.[0-9]+)?)\s*([kmbt])?/i
]
const remainingPattern = /(?:remaining|amount left|quantity left)\s*[:=-]?\s*([0-9][0-9,]*)/i
const requestedPattern = /(?:requested|total amount|quantity)\s*[:=-]?\s*([0-9][0-9,]*)/i
const deliveredPattern = /([0-9][0-9,]*(?:\.[0-9]+)?)\s*([kmbt])?\s*\/\s*([0-9][0-9,]*(?:\.[0-9]+)?)\s*([kmbt])?\s+delivered\b/i
const ownerPattern = /(?:owner|buyer|created by)\s*[:=-]\s*([A-Za-z0-9_]{1,32})/i
const idPattern = /(?:order id|order #|id)\s*[:#=-]?\s*([A-Za-z0-9_-]{3,128})/i
const positionPattern = /(?:position|queue|rank)\s*[:#=-]?\s*#?([0-9][0-9,]*)/i
const suffixes: Record<string, bigint> = { '': 1n, k: 1_000n, m: 1_000_000n, b: 1_000_000_000n, t: 1_000_000_000_000n }
// Only base items whose market identity cannot normally carry economically
// meaningful variants may join base-item auction evidence. Everything else
// remains research-only until a canonical component signature is implemented.
const baseOnlyCommodities = new Set([
  'minecraft:ancient_debris', 'minecraft:amethyst_shard', 'minecraft:apple', 'minecraft:armadillo_scute',
  'minecraft:blaze_powder', 'minecraft:blaze_rod', 'minecraft:bone', 'minecraft:bone_meal',
  'minecraft:blue_ice', 'minecraft:breeze_rod',
  'minecraft:charcoal', 'minecraft:coal', 'minecraft:coal_block', 'minecraft:cobblestone',
  'minecraft:copper_ingot', 'minecraft:crying_obsidian', 'minecraft:diamond', 'minecraft:diamond_block',
  'minecraft:dirt', 'minecraft:emerald', 'minecraft:emerald_block', 'minecraft:end_crystal',
  'minecraft:ender_eye', 'minecraft:ender_pearl', 'minecraft:enchanted_golden_apple', 'minecraft:experience_bottle',
  'minecraft:feather', 'minecraft:fermented_spider_eye', 'minecraft:ghast_tear', 'minecraft:glass',
  'minecraft:glow_ink_sac', 'minecraft:gold_ingot', 'minecraft:gold_nugget', 'minecraft:golden_apple',
  'minecraft:golden_carrot', 'minecraft:gravel', 'minecraft:gunpowder', 'minecraft:heart_of_the_sea',
  'minecraft:honey_block', 'minecraft:honeycomb', 'minecraft:honeycomb_block', 'minecraft:ink_sac',
  'minecraft:gilded_blackstone', 'minecraft:hopper', 'minecraft:iron_block', 'minecraft:iron_ingot', 'minecraft:iron_nugget', 'minecraft:lapis_block', 'minecraft:lapis_lazuli',
  'minecraft:leather', 'minecraft:magma_cream', 'minecraft:nether_quartz_ore', 'minecraft:netherite_block',
  'minecraft:netherite_ingot', 'minecraft:netherite_scrap', 'minecraft:obsidian', 'minecraft:phantom_membrane',
  'minecraft:dragon_head', 'minecraft:nether_star', 'minecraft:netherite_upgrade_smithing_template',
  'minecraft:prismarine_crystals', 'minecraft:prismarine_shard', 'minecraft:quartz', 'minecraft:quartz_block',
  'minecraft:rabbit_foot', 'minecraft:rabbit_hide', 'minecraft:raw_copper', 'minecraft:raw_copper_block',
  'minecraft:raw_gold', 'minecraft:raw_gold_block', 'minecraft:raw_iron', 'minecraft:raw_iron_block',
  'minecraft:red_sand', 'minecraft:redstone', 'minecraft:redstone_block', 'minecraft:rotten_flesh',
  'minecraft:sand', 'minecraft:scute', 'minecraft:slime_ball', 'minecraft:spider_eye', 'minecraft:sponge',
  'minecraft:stone', 'minecraft:string', 'minecraft:totem_of_undying',
  'minecraft:anvil', 'minecraft:blast_furnace', 'minecraft:bone_block', 'minecraft:bookshelf',
  'minecraft:carved_pumpkin', 'minecraft:cauldron', 'minecraft:chipped_anvil', 'minecraft:cobweb',
  'minecraft:dead_fire_coral_fan', 'minecraft:diamond_ore', 'minecraft:fletching_table', 'minecraft:glass_bottle',
  'minecraft:glowstone', 'minecraft:glowstone_dust', 'minecraft:ice', 'minecraft:jukebox', 'minecraft:lever',
  'minecraft:note_block', 'minecraft:oxidized_copper_bulb', 'minecraft:pale_oak_shelf',
  'minecraft:polished_blackstone', 'minecraft:quartz_stairs', 'minecraft:rail', 'minecraft:redstone_lamp',
  'minecraft:redstone_torch', 'minecraft:sculk_catalyst', 'minecraft:sea_lantern', 'minecraft:slime_block',
  'minecraft:sticky_piston', 'minecraft:stripped_acacia_log', 'minecraft:target', 'minecraft:tinted_glass',
  'minecraft:warped_trapdoor', 'minecraft:waxed_oxidized_copper_bulb', 'minecraft:wind_charge',
  'minecraft:white_wool', 'minecraft:red_wool', 'minecraft:gray_wool', 'minecraft:lime_concrete',
  'minecraft:yellow_concrete', 'minecraft:black_glazed_terracotta', 'minecraft:green_glazed_terracotta'
])
const modifierMarkers = /^(attribute_modifiers|bundle_contents|charged_projectiles|container|custom_model_data|damage|dyed_color|enchantments|firework_explosion|fireworks|instrument|map_decorations|map_id|potion_contents|stored_enchantments|trim)$/i

export function projectItem(item: unknown, slot: number): ItemView | undefined {
  if (item === null || typeof item !== 'object') return undefined
  const value = item as Record<string, unknown>
  const itemId = normalizeItemId(stringValue(value.name ?? value.type ?? value.itemId))
  if (!itemId) return undefined
  const count = integer(value.count, 1)
  const maxStackSize = integer(value.stackSize ?? value.maxStackSize, 64)
  const componentValues = mapComponentValues(value.componentMap)
  const simplifiedNbt = simplifyItemNbt(value.nbt)
  const readableNbt = simplifiedNbt ?? value.nbt
  const customName = value.customName ?? componentValues.custom_name ?? findNamedValue(readableNbt, 'custom_name')
  const customLore = value.customLore ?? componentValues.lore ?? findNamedValue(readableNbt, 'lore')
  const displayName = plainText(customName) || plainText(value.displayName) || stripFormatting(stringValue(value.name))
  const componentText = [customName, customLore].flatMap(collectComponentText)
  const fallbackText = collectStrings([componentValues, readableNbt, value.components]).map(plainText)
  const text = [...componentText, ...fallbackText, displayName].map(stripFormatting).filter((entry, index, all) => entry.length > 0 && all.indexOf(entry) === index).slice(0, 64)
  return { slot, itemId, count: clamp(count, 1, 64), maxStackSize: clamp(maxStackSize, 1, 64), displayName, text, raw: item }
}

export function parseOrder(view: ItemView): ParsedOrder | undefined {
  const text = view.text.join('\n')
  const money = moneyPatterns.map(pattern => pattern.exec(text)).find(match => match !== null)
  const remaining = remainingPattern.exec(text)
  const requested = requestedPattern.exec(text)
  const delivered = deliveredPattern.exec(text)
  if (!money || (!remaining && !delivered)) return undefined
  const unitRewardCents = parseScaledInteger(money[1] ?? '', money[2] ?? '', 100n)
  const deliveredQuantity = parseScaledInteger(delivered?.[1] ?? '', delivered?.[2] ?? '', 1n)
  const deliveredTotal = parseScaledInteger(delivered?.[3] ?? '', delivered?.[4] ?? '', 1n)
  const remainingQuantity = delivered ? deliveredTotal - deliveredQuantity : parseInteger(remaining?.[1] ?? '')
  const requestedQuantity = delivered ? deliveredTotal : (requested ? parseInteger(requested[1] ?? '') : remainingQuantity)
  if (unitRewardCents <= 0 || remainingQuantity < 0 || requestedQuantity < remainingQuantity) return undefined
  const owner = ownerPattern.exec(text)?.[1] ?? ''
  const rawHash = hash(JSON.stringify({ itemId: view.itemId, count: view.count, displayName: view.displayName, text: view.text }))
	const explicitId = idPattern.exec(text)?.[1]
	const pricePosition = parseInteger(positionPattern.exec(text)?.[1] ?? '')
  const orderKey = explicitId ?? hash(JSON.stringify({ itemId: view.itemId, displayName: view.displayName, unitRewardCents, requestedQuantity, owner, pricePosition, slot: view.slot }))
  return {
    order_key: orderKey, item_id: view.itemId, signature: view.itemId, ...(view.displayName ? { display_name: view.displayName } : {}),
    quantity: view.count, max_stack_size: view.maxStackSize, unit_reward_cents: unitRewardCents,
		requested_quantity: requestedQuantity, remaining_quantity: remainingQuantity, ...(owner ? { owner } : {}),
		...(pricePosition > 0 ? { price_position: pricePosition } : {}),
    slot: view.slot, raw_field_hash: rawHash,
    signature_complete: baseSignatureComplete(view),
    identity_verified: explicitId !== undefined || owner !== ''
  }
}

// Donut's "Most Per Item" mode sorts by the unit reward, not by total order
// value. Requiring a fully parsed page with descending rewards lets us verify
// the actual server behavior even when menu-color metadata is unavailable.
export function isMostPerItemOrder(orders: readonly ParsedOrder[], expectedListings: number): boolean {
  if (expectedListings < 10 || orders.length !== expectedListings) return false
	return hasDescendingUnitRewards(orders)
}

// A canonical item search can legitimately contain only a handful of orders.
// The global page proves the active sort mode first; this second check proves
// the filtered result did not violate that descending unit-reward ordering.
export function isFilteredMostPerItemOrder(orders: readonly ParsedOrder[], expectedListings: number): boolean {
  if (expectedListings < 1 || orders.length !== expectedListings) return false
	return hasDescendingUnitRewards(orders)
}

function hasDescendingUnitRewards(orders: readonly ParsedOrder[]): boolean {
  for (let index = 1; index < orders.length; index++) {
    if (orders[index]!.unit_reward_cents > orders[index - 1]!.unit_reward_cents) return false
  }
  return true
}

export function baseSignatureComplete(view: ItemView): boolean {
  if (!baseOnlyCommodities.has(view.itemId)) return false
  if (view.text.some(value => modifierMarkers.test(value.trim()))) return false
  return !hasModifierComponent(view.raw)
}

function hasModifierComponent(value: unknown, depth = 0): boolean {
  if (depth > 8 || value === null || typeof value !== 'object') return false
  if (value instanceof Map) {
    for (const [key, child] of value.entries()) {
      if (modifierKey(key) || hasModifierComponent(child, depth + 1)) return true
    }
    return false
  }
  if (Array.isArray(value)) return value.some(child => hasModifierComponent(child, depth + 1))
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    if (modifierKey(key) || hasModifierComponent(child, depth + 1)) return true
  }
  return false
}

function modifierKey(value: unknown): boolean {
  if (typeof value !== 'string') return false
  const normalized = value.toLowerCase().replace(/^minecraft:/, '')
  return modifierMarkers.test(normalized)
}

export function fingerprintWindow(title: string, views: readonly ItemView[]): string {
  return hash(JSON.stringify({ title, items: views.map(view => [view.slot, view.itemId, view.count, view.displayName, view.text]) }))
}

export function hash(value: string): string { return createHash('sha256').update(value).digest('hex') }

export function plainText(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if ((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
      try { return plainText(JSON.parse(trimmed)) } catch { /* use the literal string */ }
    }
    return stripFormatting(value)
  }
  if (Array.isArray(value)) return value.map(plainText).filter(Boolean).join('')
  if (typeof value !== 'object') return String(value)
  const component = value as Record<string, unknown>
  if (component.type === 'string' && typeof component.value === 'string') return plainText(component.value)
  if ((component.type === 'compound' || component.type === 'list') && component.value !== undefined) return plainText(component.value)
  const direct = plainText(component.text)
  const translated = direct || plainText(component.translate)
  const argumentsText = plainText(component.with)
  const extra = plainText(component.extra)
  return `${translated}${argumentsText}${extra}`
}

function collectComponentText(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(plainText).filter(Boolean)
  const text = plainText(value)
  return text ? [text] : []
}

function mapComponentValues(value: unknown): Record<string, unknown> {
  if (!(value instanceof Map)) return {}
  const result: Record<string, unknown> = {}
  for (const [key, component] of value.entries()) {
    if (typeof key !== 'string' || component === null || typeof component !== 'object') continue
    result[key] = (component as Record<string, unknown>).data
  }
  return result
}

function simplifyItemNbt(value: unknown): unknown {
  if (value === null || typeof value !== 'object') return undefined
  try { return simplify(value as Parameters<typeof simplify>[0]) } catch { return undefined }
}

function findNamedValue(value: unknown, name: string, depth = 0): unknown {
  if (depth > 8 || value === null || typeof value !== 'object') return undefined
  if (Array.isArray(value)) {
    for (const child of value) { const found = findNamedValue(child, name, depth + 1); if (found !== undefined) return found }
    return undefined
  }
  const record = value as Record<string, unknown>
  if (record[name] !== undefined) return record[name]
  for (const child of Object.values(record)) { const found = findNamedValue(child, name, depth + 1); if (found !== undefined) return found }
  return undefined
}

function collectStrings(value: unknown, depth = 0, result: string[] = []): string[] {
  if (depth > 6 || result.length >= 128 || value === null || value === undefined) return result
  if (typeof value === 'string') { result.push(value); return result }
  if (Array.isArray(value)) { for (const child of value) collectStrings(child, depth + 1, result); return result }
  if (typeof value === 'object') {
    for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
      if (['socket', '_client', 'registry'].includes(key)) continue
      collectStrings(child, depth + 1, result)
    }
  }
  return result
}

function parseScaledInteger(number: string, suffix: string, outputScale: bigint): number {
  const normalized = number.replace(/,/g, '')
  if (!/^\d+(?:\.\d+)?$/.test(normalized)) return -1
  const [whole = '0', fractional = ''] = normalized.split('.')
  const multiplier = suffixes[suffix.toLowerCase()]
  if (multiplier === undefined) return -1
  const scale = 10n ** BigInt(fractional.length)
  const raw = BigInt(whole) * scale + BigInt(fractional || '0')
  const result = raw * multiplier * outputScale
  if (result % scale !== 0n || result / scale > BigInt(Number.MAX_SAFE_INTEGER)) return -1
  return Number(result / scale)
}
function parseInteger(value: string): number { const parsed = Number(value.replace(/,/g, '')); return Number.isSafeInteger(parsed) ? parsed : -1 }
function integer(value: unknown, fallback: number): number { return typeof value === 'number' && Number.isInteger(value) ? value : fallback }
function stringValue(value: unknown): string { return typeof value === 'string' ? value : '' }
function normalizeItemId(value: string): string { const normalized = value.trim().toLowerCase(); if (!/^[a-z0-9_.-]+(?::[a-z0-9_./-]+)?$/.test(normalized)) return ''; return normalized.includes(':') ? normalized : `minecraft:${normalized}` }
function stripFormatting(value: string): string { return value.replace(/§[0-9A-FK-OR]/gi, '').replace(/[\r\n\0]/g, ' ').trim() }
function clamp(value: number, minimum: number, maximum: number): number { return Math.max(minimum, Math.min(maximum, value)) }
