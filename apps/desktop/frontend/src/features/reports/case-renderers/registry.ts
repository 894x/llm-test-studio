import type {
  CaseRendererDefinition,
  CaseRendererModule,
  CaseRendererRegistry,
} from "./types"

export function createCaseRendererRegistry(
  definitions: readonly CaseRendererDefinition[],
): CaseRendererRegistry {
  const genericDefinitions = definitions.filter(({ match }) => "generic" in match)
  if (genericDefinitions.length !== 1) {
    throw new Error("case renderer registry requires exactly one generic renderer")
  }
  const matchKeys = definitions
    .map(({ match }) => matchKey(match))
    .filter((key): key is string => key !== undefined)
  if (new Set(matchKeys).size !== matchKeys.length) {
    throw new Error("case renderer registry contains a duplicate exact or family match")
  }

  const generic = genericDefinitions[0]
  const candidates: CaseRendererRegistry["candidates"] = (caseType, caseTypeVersion, dataVersion) => {
    const supported = definitions.filter((definition) =>
      supportsVersion(definition.caseTypeVersions, caseTypeVersion) &&
      supportsVersion(definition.dataVersions, dataVersion),
    )
    if (!caseType) return [supported.find(({ match }) => "generic" in match) ?? generic]

    const exact = supported.filter(({ match }) =>
      "caseType" in match && match.caseType === caseType,
    )
    const families = supported
      .filter(({ match }) =>
        "caseTypeFamily" in match && belongsToFamily(caseType, match.caseTypeFamily),
      )
      .sort((left, right) => familyLength(right) - familyLength(left))
    const supportedGeneric = supported.find(({ match }) => "generic" in match) ?? generic
    return [...exact, ...families, supportedGeneric]
  }
  return {
    generic,
    candidates,
    resolve(caseType, caseTypeVersion, dataVersion) {
      return candidates(caseType, caseTypeVersion, dataVersion)[0]
    },
  }
}

function supportsVersion(
  versions: readonly number[] | undefined,
  actual: number | undefined,
): boolean {
  return versions === undefined || (actual !== undefined && versions.includes(actual))
}

function belongsToFamily(caseType: string, family: string): boolean {
  return caseType === family || caseType.startsWith(`${family}.`)
}

function familyLength(definition: CaseRendererDefinition): number {
  return "caseTypeFamily" in definition.match ? definition.match.caseTypeFamily.length : 0
}

function matchKey(match: CaseRendererDefinition["match"]): string | undefined {
  if ("caseType" in match) return `exact:${match.caseType}`
  if ("caseTypeFamily" in match) return `family:${match.caseTypeFamily}`
  return undefined
}

const rendererModules = import.meta.glob<CaseRendererModule>("./*.renderer.tsx", { eager: true })

export const discoveredCaseRenderers = Object.values(rendererModules).map(({ default: renderer }) => renderer)
export const caseRendererRegistry = createCaseRendererRegistry(discoveredCaseRenderers)
