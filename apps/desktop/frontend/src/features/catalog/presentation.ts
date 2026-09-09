import { isProtocol } from "./protocols"
import { protocolPresentation } from "@/features/protocols/registry"
export function caseTypeLabel(type: string, fallback = type): string { return isProtocol(type) ? protocolPresentation(type).label : fallback }
