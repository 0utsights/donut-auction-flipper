import { readFileSync } from 'node:fs'
import type { ControlRule, MenuSchema, SafeControl } from './types.js'

interface EncodedSchema {
  id: string
  titlePattern: string
  listingSlots: number[]
  controls: Array<{ slot: number; kind: SafeControl; itemName: string; labelPattern: string }>
}

export function loadSchemas(path: string | undefined): MenuSchema[] {
  if (!path) return []
  const document = JSON.parse(readFileSync(path, 'utf8')) as { schemas?: EncodedSchema[] }
  if (!Array.isArray(document.schemas) || document.schemas.length > 20) throw new Error('invalid order schema document')
  return document.schemas.map(encoded => {
    if (!/^[A-Za-z0-9_-]{1,64}$/.test(encoded.id) || encoded.titlePattern.length > 200 || encoded.listingSlots.length > 256 || encoded.controls.length > 20) throw new Error('invalid order schema')
    const listingSlots = new Set<number>()
    for (const slot of encoded.listingSlots) { if (!Number.isInteger(slot) || slot < 0 || slot > 1000) throw new Error('invalid listing slot'); listingSlots.add(slot) }
    const controls = new Map<number, ControlRule>()
    for (const control of encoded.controls) {
      if (!Number.isInteger(control.slot) || control.slot < 0 || control.slot > 1000 || controls.has(control.slot)) throw new Error('invalid control slot')
      if (!['next_page', 'previous_page', 'refresh', 'filter', 'search'].includes(control.kind) || !/^[a-z0-9_:-]{1,128}$/.test(control.itemName) || control.labelPattern.length > 200) throw new Error('invalid control rule')
      controls.set(control.slot, { kind: control.kind, itemName: control.itemName, label: new RegExp(control.labelPattern, 'i') })
    }
    return { id: encoded.id, title: new RegExp(encoded.titlePattern, 'i'), listingSlots, controls }
  })
}
