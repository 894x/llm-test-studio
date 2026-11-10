export function MetricValue({ value, unit }: { value: string; unit?: string }) {
  if (!unit || value === "—") return value

  return <span className="inline-block max-w-full align-middle leading-tight tabular-nums">
    <span className="block [overflow-wrap:anywhere]">{value}</span>{" "}
    <span className="block whitespace-nowrap text-[10px] font-normal leading-3 text-muted-foreground">{unit}</span>
  </span>
}
