// Which downloaded model to suggest. Speed alone would always pick the smallest model, which writes poor code, so the
// suggestion is the largest model that is still comfortably interactive (writes at least INTERACTIVE_TPS tokens/s with
// a 4K-token context). If none is that fast, the fastest one.
export const INTERACTIVE_TPS = 40;

export const decodeAt = (result, n = 4096) => result?.runs?.find((c) => c.kv_bits === 0 && c.prompt_tokens === n)?.decode_tps ?? 0;

const measured = (items) => items.filter((i) => i.result && !i.result.error && decodeAt(i.result) > 0);

export function fastestRepo(items) {
  return measured(items).sort((a, b) => decodeAt(b.result) - decodeAt(a.result))[0]?.repo ?? '';
}

export function recommendedRepo(items) {
  const ok = measured(items).filter((i) => decodeAt(i.result) >= INTERACTIVE_TPS);
  return ok.sort((a, b) => b.size_gb - a.size_gb)[0]?.repo ?? fastestRepo(items);
}
