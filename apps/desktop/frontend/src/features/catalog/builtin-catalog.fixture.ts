import builtinCatalog from "virtual:builtin-catalog"
import { parseCatalogSnapshot } from "./data"

// Preview data uses the desktop's file loaders, validation and DTO projection.
export const BUILTIN_CATALOG = parseCatalogSnapshot(builtinCatalog)
