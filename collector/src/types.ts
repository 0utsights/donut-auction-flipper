export const SCHEMA_VERSION = 'orders-v1'
export const PARSER_VERSION = 'mineflayer-orders-1.5.1'

export interface AccountConfig {
  id: string
  microsoftUsername: string
  proxyLabel: string
  proxyUrl: string
  expectedEgressIp: string
  profilesFolder: string
}

export interface CollectorConfig {
  backendUrl: string
  observerToken: string
  minecraftHost: string
  minecraftPort: number
  version: '1.21.11'
  expectedEgressCheckUrl: string
  menuSchemasFile?: string
  accounts: AccountConfig[]
}

export type TaskKind = 'discovery' | 'focused_watch' | 'schema_probe' | 'verification'

export interface ObserverTask {
  id: string
  kind: TaskKind
  signature?: string
  priority: number
  desired_freshness_ms: number
  parser_schema: string
  lease_expires_at: string
  lease_token: string
}

export interface ParsedOrder {
  order_key: string
  item_id: string
  signature: string
  display_name?: string
  quantity: number
  max_stack_size: number
  unit_reward_cents: number
  requested_quantity: number
  remaining_quantity: number
  owner?: string
  expires_at?: string
  price_position?: number
  slot: number
  raw_field_hash: string
  signature_complete: boolean
  identity_verified: boolean
}

export interface ScanBatch {
  schema_version: typeof SCHEMA_VERSION
  observer_id: string
  task_id?: string
  lease_token?: string
  session_id: string
  content_hash: string
  screen_title: string
  page: number
  complete: boolean
  observed_at: string
  orders: ParsedOrder[]
  unknown_schema?: boolean
  schema_reason?: string
}

export interface MenuSchema {
  id: string
  title: RegExp
  listingSlots: ReadonlySet<number>
  controls: ReadonlyMap<number, ControlRule>
}

export interface RuntimeConfig {
  backendUrl: string
  observerToken: string
  minecraftHost: string
  minecraftPort: number
  version: '1.21.11'
  expectedEgressCheckUrl: string
  menuSchemasFile?: string
  account: AccountConfig
}

export type SafeControl = 'next_page' | 'previous_page' | 'refresh' | 'filter' | 'search'

export interface ControlRule {
  kind: SafeControl
  itemName: string
  label: RegExp
}

export interface ItemView {
  slot: number
  itemId: string
  count: number
  maxStackSize: number
  displayName: string
  text: string[]
  raw: unknown
}
