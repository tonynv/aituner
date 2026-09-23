export const GiB = 1024 ** 3;
export const fmtBytes = (b) => (b >= GiB ? `${(b / GiB).toFixed(b >= 10 * GiB ? 0 : 1)} GB` : `${Math.round(b / 1024 ** 2)} MB`);
export const fmtNum = (v, d = 1) => (v >= 100 ? v.toFixed(0) : v.toFixed(d));

const LABELS = {
  'memory/cpu/copy_bandwidth': 'CPU memory copy bandwidth',
  'memory/cpu/read_bandwidth': 'CPU memory read bandwidth',
  'gpu/mlx/matmul_fp16': 'GPU compute, fp16',
  'gpu/mlx/matmul_fp32': 'GPU compute, fp32',
  'gpu/mlx/mem_bandwidth': 'GPU memory bandwidth',
  'llm/mlx/prompt_tps': 'MLX prompt processing',
  'llm/mlx/generation_tps': 'MLX text generation',
  'llm/mlx/peak_memory': 'MLX peak memory',
  'llm/ollama/prompt_tps': 'Ollama prompt processing',
  'llm/ollama/generation_tps': 'Ollama text generation',
};
export const metricKey = (m) => `${m.suite}/${m.engine}/${m.metric}`;
export const metricLabel = (m) => LABELS[metricKey(m)] || metricKey(m);
export const spread = (trials) => {
  if (!trials || trials.length < 2) return 0;
  const s = [...trials].sort((a, b) => a - b);
  const med = s[Math.floor(s.length / 2)];
  return med ? ((s[s.length - 1] - s[0]) / 2 / med) * 100 : 0;
};
