package bench

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A stand-in interpreter that prints what mlxbench.py's modelbench prints, out of order, to check parsing and ordering.
func TestModelBenchParsesAndOrdersCells(t *testing.T) {
	dir := t.TempDir()
	py := filepath.Join(dir, "python")
	script := `#!/bin/sh
echo '{"event":"loaded","load_s":1.5}'
echo '{"event":"result","suite":"modelbench","kv_bits":8,"prompt_tokens":4096,"prefill_tps":[90,110],"decode_tps":[40,44],"peak_gb":9.5}'
echo '{"event":"result","suite":"modelbench","kv_bits":0,"prompt_tokens":4096,"prefill_tps":[100,120],"decode_tps":[50,54],"peak_gb":9}'
echo '{"event":"result","suite":"modelbench","kv_bits":0,"prompt_tokens":1024,"prefill_tps":[130],"decode_tps":[60],"peak_gb":8}'
echo 'noise that is not json'
echo "4096 tokens of context" >&2
`
	if err := os.WriteFile(py, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var logged []string
	res, err := MLX{Python: py, Dir: dir}.ModelBench(context.Background(), func(e Event) { logged = append(logged, e.Message) }, "/models/x")
	if err != nil {
		t.Fatal(err)
	}
	if res.LoadS != 1.5 || len(res.Runs) != 3 {
		t.Fatalf("%+v", res)
	}
	order := [][2]int{{0, 1024}, {0, 4096}, {8, 4096}}
	for i, want := range order {
		if res.Runs[i].KVBits != want[0] || res.Runs[i].PromptTokens != want[1] {
			t.Fatalf("cell %d: %+v", i, res.Runs[i])
		}
	}
	if c, ok := res.Cell(4096, 0); !ok || c.PrefillTPS != 110 || c.DecodeTPS != 52 || c.PeakGB != 9 {
		t.Fatalf("medians: %+v", c)
	}
	if _, ok := res.Cell(16384, 0); ok {
		t.Fatal("a cell that was not measured must be absent")
	}
	if len(logged) == 0 {
		t.Fatal("stderr progress must reach the caller")
	}
}

func TestModelBenchWithoutMeasurementsIsAnError(t *testing.T) {
	dir := t.TempDir()
	py := filepath.Join(dir, "python")
	os.WriteFile(py, []byte("#!/bin/sh\necho '{\"event\":\"loaded\",\"load_s\":1}'\n"), 0o755)
	if _, err := (MLX{Python: py, Dir: dir}).ModelBench(context.Background(), func(Event) {}, "/m"); err == nil {
		t.Fatal("no measurements must not be reported as success")
	}
}
