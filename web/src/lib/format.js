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

// 262144 -> "262K", 99739 -> "99.7K", 40960 -> "41K"
export function fmtTokens(n) {
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 100_000) return `${Math.round(n / 1000)}K`;
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10_000 ? 0 : 1)}K`.replace('.0K', 'K');
  return String(n);
}
export const fmtGB = (v) => `${v >= 10 ? v.toFixed(0) : v.toFixed(1)} GB`;
export const fmtSpeed = (bps) => (bps >= 1024 ** 2 ? `${(bps / 1024 ** 2).toFixed(0)} MB/s` : `${Math.round(bps / 1024)} KB/s`);
