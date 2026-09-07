import { PROTOCOLS, type ProtocolID } from "./protocols.generated"

export { PROTOCOLS }
export type { ProtocolID }

export const protocolOptions = PROTOCOLS.map(({ id, label }) => [id, label] as const)
export const PROTOCOL_LABELS = Object.fromEntries(protocolOptions) as Record<ProtocolID, string>

export function isProtocol(value: unknown): value is ProtocolID {
  return PROTOCOLS.some(({ id }) => id === value)
}
