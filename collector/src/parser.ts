import { createHash } from 'node:crypto'
import type { ItemView, ParsedOrder } from './types.js'

const moneyPattern = /(?:reward|unit reward|price|paying|each)\s*[:=-]?\s*\$?([0-9][0-9,]*(?:\.[0-9]+)?)\s*([kmbt])?/i
const remainingPattern = /(?:remaining|amount left|quantity left)\s*[:=-]?\s*([0-9][0-9,]*)/i
const requestedPattern = /(?:requested|total amount|quantity)\s*[:=-]?\s*([0-9][0-9,]*)/i
const ownerPattern = /(?:owner|buyer|created by)\s*[:=-]\s*([A-Za-z0-9_]{1,32})/i
const idPattern = /(?:order id|order #|id)\s*[:#=-]?\s*([A-Za-z0-9_-]{3,128})/i
const positionPattern = /(?:position|queue|rank)\s*[:#=-]?\s*#?([0-9][0-9,]*)/i
const suffixes: Record<string, bigint> = { '': 1n, k: 1_000n, m: 1_000_000n, b: 1_000_000_000n, t: 1_000_000_000_000n }

export function projectItem(item: unknown, slot: number): ItemView | undefined {
  if (item === null || typeof item !== 'object') return undefined
  const value = item as Record<string, unknown>
  const itemId = normalizeItemId(stringValue(value.name ?? value.type ?? value.itemId))
  if (!itemId) return undefined
  const count = integer(value.count, 1)
  const maxStackSize = integer(value.stackSize ?? value.maxStackSize, 64)
  const displayName = stripFormatting(stringValue(value.displayName ?? value.customName ?? value.name))
  const text = collectStrings(value).map(stripFormatting).filter((entry, index, all) => entry.length > 0 && all.indexOf(entry) === index).slice(0, 64)
  return { slot, itemId, count: clamp(count, 1, 1728), maxStackSize: clamp(maxStackSize, 1, 99), displayName, text, raw: item }
}

export function parseOrder(view: ItemView): ParsedOrder | undefined {
  const text = view.text.join('\n')
  const money = moneyPattern.exec(text)
  const remaining = remainingPattern.exec(text)
  const requested = requestedPattern.exec(text)
  if (!money || !remaining) return undefined
  const unitReward = parseMoney(money[1] ?? '', money[2] ?? '')
  const remainingQuantity = parseInteger(remaining[1] ?? '')
  const requestedQuantity = requested ? parseInteger(requested[1] ?? '') : remainingQuantity
  if (unitReward <= 0 || remainingQuantity < 0 || requestedQuantity < remainingQuantity) return undefined
  const owner = ownerPattern.exec(text)?.[1] ?? ''
  const rawHash = hash(JSON.stringify({ itemId: view.itemId, count: view.count, displayName: view.displayName, text: view.text }))
	const explicitId = idPattern.exec(text)?.[1]
	const pricePosition = parseInteger(positionPattern.exec(text)?.[1] ?? '')
  const orderKey = explicitId ?? rawHash
  return {
    order_key: orderKey, item_id: view.itemId, signature: view.itemId, ...(view.displayName ? { display_name: view.displayName } : {}),
    quantity: view.count, max_stack_size: view.maxStackSize, unit_reward: unitReward,
		requested_quantity: requestedQuantity, remaining_quantity: remainingQuantity, ...(owner ? { owner } : {}),
		...(pricePosition > 0 ? { price_position: pricePosition } : {}),
    slot: view.slot, raw_field_hash: rawHash,
    // The generic compatibility parser cannot yet prove equivalence with the
    // backend's modifier-aware signature. Real versioned menu fixtures must
    // provide that mapping before an item can graduate beyond research.
    signature_complete: false
  }
}

export function fingerprintWindow(title: string, views: readonly ItemView[]): string {
  return hash(JSON.stringify({ title, items: views.map(view => [view.slot, view.itemId, view.count, view.displayName, view.text]) }))
}

export function hash(value: string): string { return createHash('sha256').update(value).digest('hex') }

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

function parseMoney(number: string, suffix: string): number {
  const normalized = number.replace(/,/g, '')
  if (!/^\d+(?:\.\d+)?$/.test(normalized)) return -1
  const [whole = '0', fractional = ''] = normalized.split('.')
  const multiplier = suffixes[suffix.toLowerCase()]
  if (multiplier === undefined) return -1
  const scale = 10n ** BigInt(fractional.length)
  const raw = BigInt(whole) * scale + BigInt(fractional || '0')
  const result = raw * multiplier
  if (result % scale !== 0n || result / scale > BigInt(Number.MAX_SAFE_INTEGER)) return -1
  return Number(result / scale)
}
function parseInteger(value: string): number { const parsed = Number(value.replace(/,/g, '')); return Number.isSafeInteger(parsed) ? parsed : -1 }
function integer(value: unknown, fallback: number): number { return typeof value === 'number' && Number.isInteger(value) ? value : fallback }
function stringValue(value: unknown): string { return typeof value === 'string' ? value : '' }
function normalizeItemId(value: string): string { const normalized = value.trim().toLowerCase(); if (!/^[a-z0-9_.-]+(?::[a-z0-9_./-]+)?$/.test(normalized)) return ''; return normalized.includes(':') ? normalized : `minecraft:${normalized}` }
function stripFormatting(value: string): string { return value.replace(/§[0-9A-FK-OR]/gi, '').replace(/[\r\n\0]/g, ' ').trim() }
function clamp(value: number, minimum: number, maximum: number): number { return Math.max(minimum, Math.min(maximum, value)) }
