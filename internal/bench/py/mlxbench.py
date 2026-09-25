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

    import pkgutil

    import mlx_lm.models as models
    from mlx_lm.utils import MODEL_REMAPPING

    # mlx_lm loads architecture X by importing mlx_lm.models.X, after MODEL_REMAPPING (see utils._get_classes)
    supported = sorted({m.name for m in pkgutil.iter_modules(models.__path__)} | set(MODEL_REMAPPING))
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
        supported_model_types=supported,
    )


def _warm(fn, seconds):
    """Run fn until `seconds` have passed: GPU clocks, allocator and page residency need time, not a fixed iteration count."""
    import mlx.core as mx

    end = time.perf_counter() + seconds
    while time.perf_counter() < end:
        mx.eval(fn())


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
    # memory bandwidth FIRST, in a clean allocator state: buffers left over from the matmul tests fragment MLX's cache and
    # made this number swing by 5-30% (measured); in a clean process it is stable to about 1%.
    # Reads N bytes and writes N bytes per elementwise op.
    elems = args.bw_mib * 1024 * 1024 // 4
    x = mx.ones((elems,), dtype=mx.float32)
    mx.eval(x)
    _warm(lambda: x + 1.0, 1.5)
    trials = []
    for i in range(args.trials):
        iters = 10
        dt = _timed(lambda: x + 1.0, iters)
        trials.append(2 * elems * 4 * iters / dt / 1e9)
        emit(event="trial", suite="gpu", metric="mem_bandwidth", value=trials[-1], unit="GB/s", i=i + 1)
    emit(event="result", suite="gpu", engine="mlx", metric="mem_bandwidth", unit="GB/s", trials=trials)
    del x

    for dtype, name in ((mx.float16, "fp16"), (mx.float32, "fp32")):
        a = mx.random.normal((n, n)).astype(dtype)
        b = mx.random.normal((n, n)).astype(dtype)
        mx.eval(a, b)
        _warm(lambda: a @ b, 1.0)
        trials = []
        for i in range(args.trials):
            iters = 20
            dt = _timed(lambda: a @ b, iters)
            trials.append(2 * n**3 * iters / dt / 1e12)
            emit(event="trial", suite="gpu", metric=f"matmul_{name}", value=trials[-1], unit="TFLOPS", i=i + 1)
        emit(event="result", suite="gpu", engine="mlx", metric=f"matmul_{name}", unit="TFLOPS", trials=trials)
        del a, b


    # sustained run LAST (it heats the GPU, so it must not precede any other measurement): continuous fp16 matmul, one
    # throughput sample per second. A machine that throttles shows a falling series; the report derives the drop from
    # the first vs last third.
    a = mx.random.normal((n, n)).astype(mx.float16)
    b = mx.random.normal((n, n)).astype(mx.float16)
    mx.eval(a, b)
    series = []
    for sec in range(args.sustain_seconds):
        t0, iters = time.perf_counter(), 0
        while time.perf_counter() - t0 < 1.0:
            mx.eval(a @ b)
            iters += 1
        series.append(2 * n**3 * iters / (time.perf_counter() - t0) / 1e12)
        emit(event="trial", suite="gpu", metric="sustained_matmul_fp16", value=series[-1], unit="TFLOPS", i=sec + 1)
    emit(event="result", suite="gpu", engine="mlx", metric="sustained_matmul_fp16", unit="TFLOPS", trials=series)
    del a, b


def fetch(args):
    from huggingface_hub import snapshot_download

    log(f"downloading {args.model} (weights/config/tokenizer only)")
    path = snapshot_download(
        args.model,
        allow_patterns=["*.json", "*.safetensors", "*.model", "*.tiktoken", "*.txt", "*.jinja", "*.jsonl"],
        ignore_patterns=["*.py", "*.bin", "*.pt", "*.pth", "*.pkl", "*.pickle", "*.ckpt"],
    )
    emit(event="fetched", model=args.model, path=path)


def download(args):
    """Save a model into a chosen folder. Patterns come from the caller (single source of truth in Go)."""
    from huggingface_hub import snapshot_download

    log(f"downloading {args.model} to {args.dir}")
    snapshot_download(
        args.model,
        local_dir=args.dir,
        allow_patterns=json.loads(args.allow),
        ignore_patterns=json.loads(args.ignore),
    )
    emit(event="downloaded", model=args.model, dir=args.dir)


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


def sweep(args):
    """Prefill speed at several prompt lengths and decode speed at a deep context, on one loaded model."""
    import mlx.core as mx
    from mlx_lm import load, stream_generate

    mx.random.seed(0)
    model, tok, cfg = load(args.model, return_config=True, tokenizer_config={"trust_remote_code": False})
    tok._eos_token_ids = {}
    vocab = cfg.get("vocab_size") or cfg["text_config"]["vocab_size"]

    def run(prompt_tokens, gen_tokens):
        prompt = mx.random.randint(0, vocab, (1, prompt_tokens)).tolist()[0]
        last = None
        for last in stream_generate(model, tok, prompt, max_tokens=gen_tokens, prefill_step_size=args.prefill_step_size):
            pass
        return last

    run(256, 4)  # warmup
    for p_len in args.prefill:
        vals = [run(p_len, 4).prompt_tps for _ in range(args.trials)]
        for i, v in enumerate(vals):
            emit(event="trial", suite="llm", metric=f"prefill_{p_len}_tps", value=v, unit="tok/s", i=i + 1)
        emit(event="result", suite="llm", engine="mlx", metric=f"prefill_{p_len}_tps", unit="tok/s", trials=vals, model=args.model, prompt_tokens=p_len)
    for depth in args.decode_depth:
        vals = [run(depth, args.gen_tokens).generation_tps for _ in range(args.trials)]
        for i, v in enumerate(vals):
            emit(event="trial", suite="llm", metric=f"decode_{depth}_tps", value=v, unit="tok/s", i=i + 1)
        emit(event="result", suite="llm", engine="mlx", metric=f"decode_{depth}_tps", unit="tok/s", trials=vals, model=args.model, prompt_tokens=depth)


def main():
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)
    sub.add_parser("probe").set_defaults(fn=probe)
    g = sub.add_parser("gpu")
    g.add_argument("--trials", type=int, default=5)
    g.add_argument("--matmul-n", type=int, default=4096)
    g.add_argument("--bw-mib", type=int, default=1024)
    g.add_argument("--sustain-seconds", type=int, default=20)
    g.set_defaults(fn=gpu)
    f = sub.add_parser("fetch")
    f.add_argument("--model", required=True)
    f.set_defaults(fn=fetch)
    d = sub.add_parser("download")
    d.add_argument("--model", required=True)
    d.add_argument("--dir", required=True)
    d.add_argument("--allow", required=True)
    d.add_argument("--ignore", required=True)
    d.set_defaults(fn=download)
    w = sub.add_parser("sweep")
    w.add_argument("--model", required=True)
    w.add_argument("--trials", type=int, default=3)
    w.add_argument("--prefill", type=int, nargs="+", default=[256, 1024, 4096])
    w.add_argument("--decode-depth", type=int, nargs="+", default=[4096])
    w.add_argument("--gen-tokens", type=int, default=128)
    w.add_argument("--prefill-step-size", type=int, default=2048)
    w.set_defaults(fn=sweep)
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
