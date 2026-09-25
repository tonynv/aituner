package connect

// chatScript is a tiny terminal chat client for the gateway (Python standard library only, run with aituner's own
// Python). It gives the tmux layout a chat pane without loading a second copy of the model.
const chatScript = `#!/usr/bin/env python3
# ` + Marker + `: terminal chat client for the local model
import json, os, sys, urllib.request

base = os.environ["AITUNER_BASE"]
key = open(os.environ["AITUNER_KEY_FILE"]).read().strip()
model = os.environ.get("AITUNER_MODEL", "local")
history = []

def stream(messages):
    req = urllib.request.Request(base + "/chat/completions", method="POST",
        data=json.dumps({"model": model, "messages": messages, "stream": True}).encode(),
        headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
    out = []
    with urllib.request.urlopen(req, timeout=600) as r:
        for raw in r:
            line = raw.decode("utf-8", "replace").strip()
            if not line.startswith("data:"):
                continue
            data = line[5:].strip()
            if data == "[DONE]":
                break
            try:
                delta = json.loads(data)["choices"][0]["delta"].get("content") or ""
            except (KeyError, IndexError, ValueError):
                continue
            out.append(delta)
            sys.stdout.write(delta)
            sys.stdout.flush()
    print()
    return "".join(out)

print("aituner chat with " + model + "   (/reset clears the conversation, /exit quits)")
while True:
    try:
        text = input("\nyou> ").strip()
    except (EOFError, KeyboardInterrupt):
        print()
        break
    if text in ("/exit", "/quit"):
        break
    if text == "/reset":
        history.clear()
        print("(conversation cleared)")
        continue
    if not text:
        continue
    history.append({"role": "user", "content": text})
    try:
        sys.stdout.write("\nmodel> ")
        history.append({"role": "assistant", "content": stream(history)})
    except Exception as e:
        history.pop()
        print("\n[error] " + str(e))
`
