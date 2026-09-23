"""aituner MLX workloads. Emits one JSON object per line on stdout; human logs go to stderr.

The LLM method mirrors mlx-lm's own `mlx_lm.benchmark` (random-token prompt, EOS disabled, one warmup,
then N timed trials via stream_generate) but reports structured per-trial numbers.
Downloads fetch weights/config/tokenizer only: never *.py (remote code) or pickle-based files.
"""
import argparse
import json
import sys
import time


def emit(**kw):
    print(json.dumps(kw), flush=True)


def log(msg):
    print(msg, file=sys.stderr, flush=True)


def probe(_):
    import importlib.metadata as md

    import mlx.core as mx

    di = mx.device_info()
    emit(
        event="probe",
        mlx=md.version("mlx"),
        mlx_lm=md.version("mlx-lm"),
        device=di.get("device_name"),
        architecture=di.get("architecture"),
        memory_size=di.get("memory_size"),
        max_recommended_working_set_size=di.get("max_recommended_working_set_size"),
        max_buffer_length=di.get("max_buffer_length"),
    )


def _timed(fn, iters):
    import mlx.core as mx

    t0 = time.perf_counter()
    for _ in range(iters):
        mx.eval(fn())
    return time.perf_counter() - t0


def gpu(args):
    import mlx.core as mx

    mx.random.seed(0)
    n = args.matmul_n
    for dtype, name in ((mx.float16, "fp16"), (mx.float32, "fp32")):
        a = mx.random.normal((n, n)).astype(dtype)
        b = mx.random.normal((n, n)).astype(dtype)
        mx.eval(a, b)
        _timed(lambda: a @ b, 3)  # warmup
        trials = []
        for i in range(args.trials):
            iters = 20
            dt = _timed(lambda: a @ b, iters)
            trials.append(2 * n**3 * iters / dt / 1e12)
            emit(event="trial", suite="gpu", metric=f"matmul_{name}", value=trials[-1], unit="TFLOPS", i=i + 1)
        emit(event="result", suite="gpu", engine="mlx", metric=f"matmul_{name}", unit="TFLOPS", trials=trials)
        del a, b

    # memory-bound elementwise op: reads N bytes and writes N bytes
    elems = args.bw_mib * 1024 * 1024 // 4
    x = mx.ones((elems,), dtype=mx.float32)
    mx.eval(x)
    _timed(lambda: x + 1.0, 3)
    trials = []
    for i in range(args.trials):
        iters = 10
        dt = _timed(lambda: x + 1.0, iters)
        trials.append(2 * elems * 4 * iters / dt / 1e9)
        emit(event="trial", suite="gpu", metric="mem_bandwidth", value=trials[-1], unit="GB/s", i=i + 1)
    emit(event="result", suite="gpu", engine="mlx", metric="mem_bandwidth", unit="GB/s", trials=trials)


def fetch(args):
    from huggingface_hub import snapshot_download

    log(f"downloading {args.model} (weights/config/tokenizer only)")
    path = snapshot_download(
        args.model,
        allow_patterns=["*.json", "*.safetensors", "*.model", "*.tiktoken", "*.txt", "*.jinja", "*.jsonl"],
        ignore_patterns=["*.py", "*.bin", "*.pt", "*.pth", "*.pkl", "*.pickle", "*.ckpt"],
    )
    emit(event="fetched", model=args.model, path=path)


def llm(args):
    import mlx.core as mx
    from mlx_lm import load, stream_generate

    mx.random.seed(0)
    model, tok, cfg = load(args.model, return_config=True, tokenizer_config={"trust_remote_code": False})
    tok._eos_token_ids = {}  # never stop early
    vocab = cfg.get("vocab_size") or cfg["text_config"]["vocab_size"]
    prompt = mx.random.randint(0, vocab, (1, args.prompt_tokens)).tolist()[0]

    def once():
        last = None
        for last in stream_generate(model, tok, prompt, max_tokens=args.gen_tokens, prefill_step_size=args.prefill_step_size):
            pass
        return last

    log("warmup")
    once()
    pp, tg, mem = [], [], []
    for i in range(args.trials):
        r = once()
        pp.append(r.prompt_tps)
        tg.append(r.generation_tps)
        mem.append(r.peak_memory)
        emit(event="trial", suite="llm", metric="trial", i=i + 1, prompt_tps=r.prompt_tps, generation_tps=r.generation_tps, peak_memory_gb=r.peak_memory)
    for metric, unit, vals in (("prompt_tps", "tok/s", pp), ("generation_tps", "tok/s", tg), ("peak_memory", "GB", mem)):
        emit(event="result", suite="llm", engine="mlx", metric=metric, unit=unit, trials=vals, model=args.model,
             prompt_tokens=args.prompt_tokens, gen_tokens=args.gen_tokens)


def main():
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)
    sub.add_parser("probe").set_defaults(fn=probe)
    g = sub.add_parser("gpu")
    g.add_argument("--trials", type=int, default=5)
    g.add_argument("--matmul-n", type=int, default=4096)
    g.add_argument("--bw-mib", type=int, default=1024)
    g.set_defaults(fn=gpu)
    f = sub.add_parser("fetch")
    f.add_argument("--model", required=True)
    f.set_defaults(fn=fetch)
    m = sub.add_parser("llm")
    m.add_argument("--model", required=True)
    m.add_argument("--prompt-tokens", type=int, default=512)
    m.add_argument("--gen-tokens", type=int, default=256)
    m.add_argument("--trials", type=int, default=3)
    m.add_argument("--prefill-step-size", type=int, default=2048)
    m.set_defaults(fn=llm)
    a = p.parse_args()
    a.fn(a)


if __name__ == "__main__":
    main()
